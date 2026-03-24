package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/baekenough/customclaw/internal/llm"
)

const (
	maxMessages      = 10
	maxMessageRunes  = 500
	extractionPrompt = `Analyze the following conversation and extract important information.
Extract ONLY genuinely important items in these categories:
- fact: Factual information about the project, user, or system
- decision: Decisions made during the conversation
- preference: User preferences about how things should be done
- action: Actions taken or requested during the conversation
- context: Important contextual information for future reference

Rules:
- Skip trivial exchanges, greetings, and filler content
- Each item must be self-contained and meaningful without the full conversation
- Be concise but complete
- Only extract items that would genuinely help in future conversations

Respond with a JSON array ONLY. No markdown, no explanation. Format:
[{"category": "fact", "content": "..."}, {"category": "decision", "content": "..."}]

If nothing important was discussed, respond with: []

Conversation:
`
)

// continuePatterns matches Korean phrases indicating the user wants work to continue.
var continuePatterns = []*regexp.Regexp{
	regexp.MustCompile(`계속\s*해`),
	regexp.MustCompile(`이어서\s*해`),
	regexp.MustCompile(`보강해`),
	regexp.MustCompile(`계속\s*진행`),
	regexp.MustCompile(`계속\s*하`),
}

// proactivePatterns matches Korean phrases indicating the user wants proactive responses.
var proactivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`기다리지`),
	regexp.MustCompile(`대기하지`),
	regexp.MustCompile(`먼저\s*말`),
	regexp.MustCompile(`알아서`),
	regexp.MustCompile(`먼저\s*해`),
}

// followUpRe detects short follow-up requests of 5 chars or fewer.
var followUpRe = regexp.MustCompile(`^.{1,5}$`)

// extractedMemory is the JSON structure returned by the LLM.
type extractedMemory struct {
	Category string `json:"category"`
	Content  string `json:"content"`
}

// jsonArrayRe strips optional markdown code-fence wrapping from an LLM response.
var jsonArrayRe = regexp.MustCompile("(?s)```(?:json)?\\s*(\\[.*?\\])\\s*```")

// MemoryExtractor distils conversation history into long-term memory entries.
// It calls the LLM with a haiku-class model for cost efficiency and stores
// extracted facts in PostgreSQL, optionally indexing them in OpenSearch.
type MemoryExtractor struct {
	provider llm.Provider
	store    *MessageStore
	osClient *OpenSearchClient
	dsn      string
}

// NewMemoryExtractor creates a MemoryExtractor with the supplied dependencies.
// osClient may be nil when OpenSearch is not configured.
func NewMemoryExtractor(
	provider llm.Provider,
	store *MessageStore,
	osClient *OpenSearchClient,
	dsn string,
) *MemoryExtractor {
	return &MemoryExtractor{
		provider: provider,
		store:    store,
		osClient: osClient,
		dsn:      dsn,
	}
}

// ExtractAndStore analyses the recent conversation for the given bot and channel
// and persists any extracted facts to the memory store.
// All errors are logged at Warn level; the method never blocks message processing.
func (e *MemoryExtractor) ExtractAndStore(ctx context.Context, botID, channelID string) error {
	hKey := "channel:" + channelID

	history, err := e.store.GetHistory(ctx, hKey, maxMessages)
	if err != nil {
		slog.Warn("extractor: failed to load history",
			"bot", botID,
			"channel", channelID,
			"error", err,
		)
		return nil
	}
	if len(history) == 0 {
		return nil
	}

	convText := buildConversationText(history)

	extracted, extractErr := e.callLLM(ctx, convText)
	if extractErr != nil {
		slog.Warn("extractor: llm call failed",
			"bot", botID,
			"channel", channelID,
			"error", extractErr,
		)
	}

	inferred := inferPreferences(history)
	all := append(extracted, inferred...)

	if len(all) == 0 {
		return nil
	}

	stored := 0
	for _, m := range all {
		if m.Content == "" {
			continue
		}
		exists, checkErr := e.store.MemoryExists(ctx, botID, m.Category, m.Content)
		if checkErr != nil {
			slog.Warn("extractor: duplicate check failed", "error", checkErr)
		}
		if exists {
			continue
		}
		if storeErr := e.storeMemory(ctx, botID, channelID, m); storeErr != nil {
			slog.Warn("extractor: failed to store memory",
				"category", m.Category,
				"error", storeErr,
			)
			continue
		}
		stored++
	}

	slog.Info("extractor: memories stored",
		"bot", botID,
		"channel", channelID,
		"count", stored,
	)
	return nil
}

// callLLM sends the conversation text to the LLM and parses the JSON response.
func (e *MemoryExtractor) callLLM(ctx context.Context, convText string) ([]extractedMemory, error) {
	req := &llm.Request{
		UserMessage: extractionPrompt + convText,
		Model:       "haiku",
		MaxTurns:    1,
	}

	resp, err := e.provider.Complete(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("llm complete: %w", err)
	}

	return parseExtraction(resp.Text), nil
}

// storeMemory persists a single extracted memory to PostgreSQL and optionally OpenSearch.
func (e *MemoryExtractor) storeMemory(ctx context.Context, botID, userID string, m extractedMemory) error {
	memoryID, err := e.store.StoreMemory(ctx, botID, userID, m.Category, m.Content)
	if err != nil {
		return err
	}
	if e.osClient != nil {
		if err := e.osClient.IndexMemory(ctx, memoryID, botID, m.Content, m.Category, userID); err != nil {
			slog.Warn("extractor: opensearch index failed", "error", err)
			// Non-fatal: PostgreSQL is the source of truth.
		}
	}
	return nil
}

// buildConversationText formats the last maxMessages messages into a prompt string.
// Each message content is truncated to maxMessageRunes runes.
// Empty messages are filtered.
func buildConversationText(messages []Message) string {
	start := 0
	if len(messages) > maxMessages {
		start = len(messages) - maxMessages
	}

	var sb strings.Builder
	for _, m := range messages[start:] {
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		if utf8.RuneCountInString(content) > maxMessageRunes {
			runes := []rune(content)
			content = string(runes[:maxMessageRunes])
		}
		role := m.Role
		if role == "user" {
			role = "User"
		} else {
			role = "Assistant"
		}
		sb.WriteString(role)
		sb.WriteString(": ")
		sb.WriteString(content)
		sb.WriteString("\n")
	}
	return sb.String()
}

// parseExtraction decodes the LLM JSON response into a slice of extractedMemory.
// It handles optional markdown code-fence wrapping and filters entries with no content.
// Returns an empty slice on any parse error.
func parseExtraction(raw string) []extractedMemory {
	text := strings.TrimSpace(raw)

	// Strip markdown code fences if present.
	if m := jsonArrayRe.FindStringSubmatch(text); len(m) == 2 {
		text = m[1]
	}

	// Find the JSON array bounds directly for cases without code fences.
	start := strings.Index(text, "[")
	end := strings.LastIndex(text, "]")
	if start == -1 || end == -1 || end <= start {
		return nil
	}
	text = text[start : end+1]

	var memories []extractedMemory
	if err := json.Unmarshal([]byte(text), &memories); err != nil {
		return nil
	}

	// Filter entries that lack content.
	result := memories[:0]
	for _, m := range memories {
		if strings.TrimSpace(m.Content) != "" {
			result = append(result, m)
		}
	}
	return result
}

// inferPreferences analyses the conversation for pattern-based preference signals
// and returns synthetic memory entries for any patterns found.
// Each preference type is emitted at most once per conversation.
func inferPreferences(messages []Message) []extractedMemory {
	if len(messages) == 0 {
		return nil
	}

	// Combine all user messages into a single string for pattern matching.
	var userText strings.Builder
	for _, m := range messages {
		if m.Role == "user" {
			userText.WriteString(m.Content)
			userText.WriteString("\n")
		}
	}
	combined := userText.String()

	var result []extractedMemory
	seen := make(map[string]bool)

	for _, p := range continuePatterns {
		if p.MatchString(combined) {
			key := "continue"
			if !seen[key] {
				seen[key] = true
				result = append(result, extractedMemory{
					Category: "preference",
					Content:  "User prefers that work continues without waiting for confirmation",
				})
			}
			break
		}
	}

	for _, p := range proactivePatterns {
		if p.MatchString(combined) {
			key := "proactive"
			if !seen[key] {
				seen[key] = true
				result = append(result, extractedMemory{
					Category: "preference",
					Content:  "User wants proactive responses without being asked first",
				})
			}
			break
		}
	}

	// Detect short follow-up messages (e.g. "해", "응", "맞아") that appear after
	// a substantial assistant response — a signal the user prefers continuations.
	if len(messages) >= 2 {
		last := messages[len(messages)-1]
		prev := messages[len(messages)-2]
		if last.Role == "user" &&
			prev.Role == "assistant" &&
			followUpRe.MatchString(strings.TrimSpace(last.Content)) {
			key := "followup"
			if !seen[key] {
				seen[key] = true
				result = append(result, extractedMemory{
					Category: "preference",
					Content:  "User often sends short follow-up messages to continue work",
				})
			}
		}
	}

	return result
}

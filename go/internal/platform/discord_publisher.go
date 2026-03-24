package platform

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	discordMsgLimit = 2000
	discordAPIBase  = "https://discord.com/api/v10"
	maxRetries      = 3
)

// emojiMap translates Slack-style emoji names to their Unicode equivalents.
// Keys that are not present are passed through unchanged, which lets callers
// supply raw Unicode or custom Discord emoji strings directly.
var emojiMap = map[string]string{
	"hourglass_flowing_sand": "⏳",
	"white_check_mark":       "✅",
	"x":                      "❌",
}

// resolveEmoji translates a Slack-style emoji name (e.g. "hourglass_flowing_sand")
// to its Unicode representation. Unknown names are returned unchanged.
func resolveEmoji(name string) string {
	if u, ok := emojiMap[name]; ok {
		return u
	}
	return name
}

// DiscordResponsePublisher implements ResponsePublisher for the Discord platform.
// It uses the Discord REST API (v10) directly via net/http, matching the approach
// used by the Python implementation which also used requests rather than a library.
type DiscordResponsePublisher struct {
	token  string
	client *http.Client
}

// NewDiscordResponsePublisher creates a DiscordResponsePublisher using the given
// Discord bot token.
func NewDiscordResponsePublisher(token string) *DiscordResponsePublisher {
	return &DiscordResponsePublisher{
		token:  token,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// SendMessage posts text to channelID, optionally within a thread identified by
// threadID. Long messages are split at paragraph, newline, or space boundaries
// and sent sequentially. Returns the message ID of the last sent chunk.
func (p *DiscordResponsePublisher) SendMessage(channelID, text string, threadID *string) (string, error) {
	chunks := splitMessage(text, discordMsgLimit)
	if len(chunks) == 0 {
		return "", nil
	}

	var lastID string
	for _, chunk := range chunks {
		payload := map[string]any{"content": chunk}
		if threadID != nil && *threadID != "" {
			// Discord's message_reference field creates a reply.
			payload["message_reference"] = map[string]string{
				"message_id": *threadID,
			}
			// Only set the reference on the first chunk; subsequent chunks
			// reply to the thread implicitly through channel context.
			threadID = nil
		}

		id, err := p.postMessage(channelID, payload)
		if err != nil {
			return lastID, fmt.Errorf("discord send message chunk: %w", err)
		}
		lastID = id
	}
	return lastID, nil
}

// AddReaction attaches an emoji reaction to the message identified by messageID
// in channelID.
func (p *DiscordResponsePublisher) AddReaction(channelID, messageID, emoji string) error {
	emoji = resolveEmoji(emoji)
	endpoint := fmt.Sprintf("%s/channels/%s/messages/%s/reactions/%s/@me",
		discordAPIBase, channelID, messageID, url.PathEscape(emoji))

	resp, err := p.doWithRetry(http.MethodPut, endpoint, nil)
	if err != nil {
		return fmt.Errorf("discord add reaction: %w", err)
	}

	defer resp.Body.Close()
	return nil
}

// RemoveReaction detaches an emoji reaction from the message identified by
// messageID in channelID.
func (p *DiscordResponsePublisher) RemoveReaction(channelID, messageID, emoji string) error {
	emoji = resolveEmoji(emoji)
	endpoint := fmt.Sprintf("%s/channels/%s/messages/%s/reactions/%s/@me",
		discordAPIBase, channelID, messageID, url.PathEscape(emoji))

	resp, err := p.doWithRetry(http.MethodDelete, endpoint, nil)
	if err != nil {
		return fmt.Errorf("discord remove reaction: %w", err)
	}
	defer resp.Body.Close()
	return nil
}

// GetThreadReplies retrieves up to limit messages from the Discord channel
// identified by threadID. On 404 (e.g. the thread no longer exists), it falls
// back to reading from the parent channelID.
func (p *DiscordResponsePublisher) GetThreadReplies(channelID, threadID string, limit int) ([]map[string]any, error) {
	messages, err := p.fetchChannelMessages(threadID, limit)
	if err != nil {
		// Fall back to the parent channel on 404.
		slog.Warn("discord get thread replies failed, falling back to parent channel",
			"thread_id", threadID,
			"channel_id", channelID,
			"error", err,
		)
		messages, err = p.fetchChannelMessages(channelID, limit)
		if err != nil {
			return nil, fmt.Errorf("discord get thread replies (fallback): %w", err)
		}
	}
	return messages, nil
}

// postMessage sends a single message payload to the given channel and returns
// the new message ID.
func (p *DiscordResponsePublisher) postMessage(channelID string, payload map[string]any) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal message payload: %w", err)
	}

	endpoint := fmt.Sprintf("%s/channels/%s/messages", discordAPIBase, channelID)
	resp, err := p.doWithRetry(http.MethodPost, endpoint, body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode message response: %w", err)
	}
	return result.ID, nil
}

// fetchChannelMessages retrieves up to limit messages from channelID.
func (p *DiscordResponsePublisher) fetchChannelMessages(channelID string, limit int) ([]map[string]any, error) {
	endpoint := fmt.Sprintf("%s/channels/%s/messages?limit=%d", discordAPIBase, channelID, limit)
	resp, err := p.doWithRetry(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var raw []struct {
		ID     string `json:"id"`
		Author struct {
			ID string `json:"id"`
		} `json:"author"`
		Content   string `json:"content"`
		Timestamp string `json:"timestamp"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode messages response: %w", err)
	}

	replies := make([]map[string]any, 0, len(raw))
	for _, m := range raw {
		replies = append(replies, map[string]any{
			"user_id": m.Author.ID,
			"text":    m.Content,
			"ts":      m.ID,
		})
	}
	return replies, nil
}

// doWithRetry executes an HTTP request with up to maxRetries attempts. It
// handles 429 Too Many Requests by honoring the Retry-After header.
//
// body is accepted as a raw byte slice so that a fresh bytes.Reader can be
// created for every attempt. Passing an io.Reader would silently send an empty
// body on the second and subsequent attempts because the reader would already
// be exhausted. Callers pass nil for requests that carry no body (GET, PUT
// reactions, DELETE reactions).
func (p *DiscordResponsePublisher) doWithRetry(method, url string, body []byte) (*http.Response, error) {
	var lastErr error
	for attempt := range maxRetries {
		var bodyReader io.Reader
		if len(body) > 0 {
			bodyReader = bytes.NewReader(body)
		}
		req, err := http.NewRequest(method, url, bodyReader)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Authorization", "Bot "+p.token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := p.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			retryAfter := retryAfterSeconds(resp)
			slog.Warn("discord rate limited",
				"url", url,
				"retry_after_sec", retryAfter,
				"attempt", attempt+1,
			)
			resp.Body.Close()
			time.Sleep(time.Duration(retryAfter * float64(time.Second)))
			continue
		}

		if resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			return nil, fmt.Errorf("discord api 404: %s", url)
		}

		if resp.StatusCode >= 400 {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("discord api error %d: %s", resp.StatusCode, string(b))
		}

		return resp, nil
	}

	if lastErr != nil {
		return nil, fmt.Errorf("discord request failed after %d attempts: %w", maxRetries, lastErr)
	}
	return nil, fmt.Errorf("discord request exhausted %d attempts: %s %s", maxRetries, method, url)
}

// retryAfterSeconds parses the Retry-After header from a 429 response.
// Falls back to 1 second if the header is absent or malformed.
func retryAfterSeconds(resp *http.Response) float64 {
	val := resp.Header.Get("Retry-After")
	if val == "" {
		return 1
	}
	// Discord sends Retry-After as a decimal number of seconds.
	f, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return 1
	}
	return f
}

// splitMessage splits text into chunks no longer than limit runes. It works
// entirely in []rune to avoid byte/rune index mismatches on multi-byte
// characters such as Korean. It attempts to split at paragraph boundaries
// ("\n\n"), then newlines, then spaces, and falls back to a hard rune cut when
// no boundary is found within the limit.
func splitMessage(text string, limit int) []string {
	if len(text) == 0 {
		return nil
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return []string{text}
	}

	var chunks []string
	rem := runes // remaining runes to process

	for len(rem) > limit {
		window := rem[:limit] // candidate chunk as rune slice

		// Try paragraph boundary first (search for "\n\n" within window runes).
		if idx := lastRuneIndex(window, []rune("\n\n")); idx > 0 {
			chunks = append(chunks, string(rem[:idx]))
			rem = trimLeftRune(rem[idx:], '\n')
			continue
		}

		// Try single newline boundary.
		if idx := lastRuneIndexSingle(window, '\n'); idx > 0 {
			chunks = append(chunks, string(rem[:idx]))
			rem = trimLeftRune(rem[idx:], '\n')
			continue
		}

		// Try space boundary.
		if idx := lastRuneIndexSingle(window, ' '); idx > 0 {
			chunks = append(chunks, string(rem[:idx]))
			rem = trimLeftRune(rem[idx:], ' ')
			continue
		}

		// Hard cut: no whitespace boundary found within limit runes.
		chunks = append(chunks, string(rem[:limit]))
		rem = rem[limit:]
	}

	if len(rem) > 0 {
		chunks = append(chunks, string(rem))
	}
	return chunks
}

// lastRuneIndex returns the last rune index in haystack at which needle starts,
// or -1 if not found. Both arguments are rune slices.
func lastRuneIndex(haystack, needle []rune) int {
	n := len(needle)
	if n == 0 || len(haystack) < n {
		return -1
	}
	for i := len(haystack) - n; i >= 0; i-- {
		match := true
		for j := 0; j < n; j++ {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// lastRuneIndexSingle returns the last rune index in rs where r appears, or -1.
func lastRuneIndexSingle(rs []rune, r rune) int {
	for i := len(rs) - 1; i >= 0; i-- {
		if rs[i] == r {
			return i
		}
	}
	return -1
}

// trimLeftRune removes leading occurrences of r from rs.
func trimLeftRune(rs []rune, r rune) []rune {
	for len(rs) > 0 && rs[0] == r {
		rs = rs[1:]
	}
	return rs
}

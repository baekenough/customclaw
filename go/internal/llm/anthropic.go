package llm

import (
	"context"
	"fmt"
	"log/slog"

	anthropic "github.com/anthropics/anthropic-sdk-go"
)

// claudeModelAliases maps short friendly names to Anthropic Messages API model IDs.
// Unknown aliases are passed through unchanged.
var claudeModelAliases = map[string]string{
	"opus":   "claude-opus-4-6",
	"sonnet": "claude-sonnet-4-6",
	"haiku":  "claude-haiku-4-5-20251001",
}

// resolveClaudeModel returns the Anthropic API model ID for a given alias.
// If the alias is not found in the map and is non-empty, it is returned as-is.
// An empty alias falls back to "sonnet".
func resolveClaudeModel(alias string) string {
	if alias == "" {
		return claudeModelAliases["sonnet"]
	}
	if m, ok := claudeModelAliases[alias]; ok {
		return m
	}
	return alias
}

// ClaudeProvider implements Provider using the Anthropic Messages API SDK.
// Authentication is handled automatically via the ANTHROPIC_API_KEY environment
// variable, which is read by the SDK on client construction.
type ClaudeProvider struct {
	client anthropic.Client
}

// NewClaudeProvider constructs a ClaudeProvider. The SDK reads ANTHROPIC_API_KEY
// from the environment automatically. No configuration is required at construction
// time beyond the environment variable being set at call time.
func NewClaudeProvider() *ClaudeProvider {
	client := anthropic.NewClient()
	return &ClaudeProvider{client: client}
}

// Name returns "claude".
func (p *ClaudeProvider) Name() string { return "claude" }

// Complete sends req to the Anthropic Messages API and returns the result.
//
// The system prompt is passed via the system parameter (array of TextBlockParam).
// The user message is passed as a user turn. MaxTurns is used to derive max_tokens
// (MaxTurns * 4096), defaulting to 8192 when MaxTurns is zero or negative.
//
// FullAgent and WorkDir are not supported by the Messages API and are ignored;
// a warning is logged when FullAgent is true.
func (p *ClaudeProvider) Complete(ctx context.Context, req *Request) (*Response, error) {
	if req.FullAgent {
		slog.Warn("ClaudeProvider: FullAgent mode is not supported by the Messages API; ignoring",
			"work_dir", req.WorkDir,
		)
	}

	model := resolveClaudeModel(req.Model)

	maxTokens := int64(8192)
	if req.MaxTurns > 0 {
		maxTokens = int64(req.MaxTurns) * 4096
	}

	// Build messages array: history turns followed by the current user message.
	messages := make([]anthropic.MessageParam, 0, len(req.History)+1)
	for _, h := range req.History {
		switch h.Role {
		case "user":
			messages = append(messages, anthropic.NewUserMessage(anthropic.NewTextBlock(h.Content)))
		case "assistant":
			messages = append(messages, anthropic.NewAssistantMessage(anthropic.NewTextBlock(h.Content)))
		}
	}
	messages = append(messages, anthropic.NewUserMessage(anthropic.NewTextBlock(req.UserMessage)))

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(model),
		MaxTokens: maxTokens,
		Messages:  messages,
	}

	if req.SystemPrompt != "" {
		params.System = []anthropic.TextBlockParam{
			{Text: req.SystemPrompt},
		}
	}

	slog.Info("calling anthropic messages api",
		"model", model,
		"max_tokens", maxTokens,
	)

	stream := p.client.Messages.NewStreaming(ctx, params)
	defer stream.Close()

	var msg anthropic.Message
	for stream.Next() {
		if err := msg.Accumulate(stream.Current()); err != nil {
			return nil, fmt.Errorf("anthropic stream accumulate error: %w", err)
		}
	}
	if err := stream.Err(); err != nil {
		return nil, fmt.Errorf("anthropic api error: %w", err)
	}

	text := extractClaudeText(&msg)
	if text == "" {
		return nil, fmt.Errorf("anthropic api returned no text content")
	}

	usage := &UsageInfo{
		InputTokens:         int(msg.Usage.InputTokens),
		OutputTokens:        int(msg.Usage.OutputTokens),
		CacheReadTokens:     int(msg.Usage.CacheReadInputTokens),
		CacheCreationTokens: int(msg.Usage.CacheCreationInputTokens),
		Model:               string(msg.Model),
	}

	return &Response{Text: text, Usage: usage}, nil
}

// extractClaudeText concatenates all text content blocks from the response.
func extractClaudeText(msg *anthropic.Message) string {
	var out string
	for _, block := range msg.Content {
		if block.Type == "text" {
			if out != "" {
				out += "\n"
			}
			out += block.Text
		}
	}
	return out
}

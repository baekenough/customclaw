package llm

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// modelAliases maps short friendly names to Anthropic model IDs.
var modelAliases = map[string]string{
	"opus":   "claude-opus-4-20250514",
	"sonnet": "claude-sonnet-4-20250514",
	"haiku":  "claude-haiku-4-5-20251001",
}

// resolveModel returns the full Anthropic model ID for a given alias or passthrough.
func resolveModel(alias string) string {
	if full, ok := modelAliases[alias]; ok {
		return full
	}
	if alias == "" {
		return modelAliases["sonnet"]
	}
	return alias
}

// AnthropicProvider implements Provider using the official Anthropic SDK.
type AnthropicProvider struct {
	// client is a value type (not a pointer) per the SDK design.
	client anthropic.Client
}

// NewAnthropicProvider constructs an AnthropicProvider.
// The ANTHROPIC_API_KEY environment variable is read automatically via
// anthropic.DefaultClientOptions().
func NewAnthropicProvider(opts ...option.RequestOption) *AnthropicProvider {
	return &AnthropicProvider{
		client: anthropic.NewClient(opts...),
	}
}

// Name returns "anthropic".
func (p *AnthropicProvider) Name() string { return "anthropic" }

// Complete sends a request to the Anthropic Messages API and returns the response.
// Tool-use blocks are noted in the log but not executed in Phase 1; Phase 2 will
// wire up the ToolRegistry here.
func (p *AnthropicProvider) Complete(ctx context.Context, req *Request) (*Response, error) {
	modelID := resolveModel(req.Model)

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(modelID),
		MaxTokens: 8192,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(req.UserMessage)),
		},
	}
	if req.SystemPrompt != "" {
		params.System = []anthropic.TextBlockParam{
			{Text: req.SystemPrompt},
		}
	}

	msg, err := p.client.Messages.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("anthropic messages.new: %w", err)
	}

	text := extractText(msg)

	usage := &UsageInfo{
		InputTokens:         int(msg.Usage.InputTokens),
		OutputTokens:        int(msg.Usage.OutputTokens),
		CacheReadTokens:     int(msg.Usage.CacheReadInputTokens),
		CacheCreationTokens: int(msg.Usage.CacheCreationInputTokens),
		Model:               string(msg.Model),
	}

	return &Response{Text: text, Usage: usage}, nil
}

// extractText concatenates all text blocks from the message content.
// Uses ContentBlockUnion.AsAny() for safe type dispatch per the SDK's design.
func extractText(msg *anthropic.Message) string {
	var result string
	for _, block := range msg.Content {
		switch b := block.AsAny().(type) {
		case anthropic.TextBlock:
			result += b.Text
		case anthropic.ToolUseBlock:
			// Phase 1: tool-use blocks are acknowledged but not executed.
			// Phase 2 will wire up ToolRegistry execution here.
			_ = b
		}
	}
	return result
}

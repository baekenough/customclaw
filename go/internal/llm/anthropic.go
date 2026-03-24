package llm

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// modelAliases maps short friendly names to Anthropic model IDs.
var modelAliases = map[string]string{
	"opus":   "claude-opus-4-20250514",
	"sonnet": "claude-sonnet-4-20250514",
	"haiku":  "claude-haiku-4-5-20251001",
}

// modelMaxTokens maps resolved model IDs to their maximum output token limits.
// Values are sourced from the Anthropic documentation. When a model is not
// present, defaultMaxTokens is used as a safe conservative fallback.
var modelMaxTokens = map[string]int64{
	"claude-opus-4-20250514":      8192,
	"claude-sonnet-4-20250514":    8192,
	"claude-haiku-4-5-20251001":   4096, // haiku max is 4 096
	"claude-3-5-haiku-20241022":   4096,
	"claude-3-5-sonnet-20241022":  8192,
	"claude-3-opus-20240229":      4096,
}

// defaultMaxTokens is used when a model is not found in modelMaxTokens.
const defaultMaxTokens int64 = 4096

// maxTokensForModel returns the safe maximum output token count for modelID.
func maxTokensForModel(modelID string) int64 {
	if n, ok := modelMaxTokens[modelID]; ok {
		return n
	}
	return defaultMaxTokens
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
	// tokenSource is non-nil when using OAuth token auth instead of a static API key.
	tokenSource *OAuthTokenSource
}

// NewAnthropicProvider constructs an AnthropicProvider.
// The ANTHROPIC_API_KEY environment variable is read automatically via
// anthropic.DefaultClientOptions().
func NewAnthropicProvider(opts ...option.RequestOption) *AnthropicProvider {
	return &AnthropicProvider{
		client: anthropic.NewClient(opts...),
	}
}

// NewAnthropicProviderWithOAuth creates an AnthropicProvider that uses
// Claude Code OAuth tokens. The token is fetched (and refreshed as needed)
// before every API call.
func NewAnthropicProviderWithOAuth(tokenSource *OAuthTokenSource) *AnthropicProvider {
	token, err := tokenSource.Token()
	if err != nil {
		slog.Warn("oauth: could not obtain initial token", "error", err)
	}
	return &AnthropicProvider{
		client:      anthropic.NewClient(option.WithAuthToken(token)),
		tokenSource: tokenSource,
	}
}

// Name returns "anthropic".
func (p *AnthropicProvider) Name() string { return "anthropic" }

// Complete sends a request to the Anthropic Messages API and returns the response.
// Tool-use blocks are noted in the log but not executed in Phase 1; Phase 2 will
// wire up the ToolRegistry here.
//
// When an OAuthTokenSource is configured, the token is refreshed before each
// call so that long-running workers never hit auth failures mid-session.
func (p *AnthropicProvider) Complete(ctx context.Context, req *Request) (*Response, error) {
	if p.tokenSource != nil {
		token, err := p.tokenSource.Token()
		if err != nil {
			slog.Warn("oauth: token refresh failed, proceeding with current token", "error", err)
		} else {
			// Recreate the client with the fresh token for this call.
			p.client = anthropic.NewClient(option.WithAuthToken(token))
		}
	}

	modelID := resolveModel(req.Model)
	maxTok := maxTokensForModel(modelID)

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(modelID),
		MaxTokens: maxTok,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(req.UserMessage)),
		},
	}
	if req.SystemPrompt != "" {
		params.System = []anthropic.TextBlockParam{
			{Text: req.SystemPrompt},
		}
	}

	slog.Debug("anthropic request",
		"model", modelID,
		"max_tokens", maxTok,
		"system_len", len(req.SystemPrompt),
		"user_len", len(req.UserMessage),
	)

	msg, err := p.client.Messages.New(ctx, params)
	if err != nil {
		slog.Error("anthropic api error",
			"model", modelID,
			"max_tokens", maxTok,
			"system_empty", req.SystemPrompt == "",
			"error", err,
		)
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

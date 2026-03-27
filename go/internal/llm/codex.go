package llm

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// openaiModelAliases maps short friendly names to OpenAI model IDs.
// Unknown aliases are passed through unchanged.
var openaiModelAliases = map[string]string{
	"gpt-5.4": "gpt-5.4",
	"codex":   "gpt-5.4",
	"gpt-4o":  "gpt-4o",
}

// resolveOpenAIModel returns the canonical OpenAI model ID for a given alias.
// If the alias is not found in the map and is non-empty, it is returned as-is.
// An empty alias falls back to "gpt-5.4".
func resolveOpenAIModel(alias string) string {
	if alias == "" {
		return "gpt-5.4"
	}
	if full, ok := openaiModelAliases[alias]; ok {
		return full
	}
	return alias
}

// resolveCodexModel is kept for backwards-compatibility with existing tests.
// It delegates to resolveOpenAIModel.
var resolveCodexModel = resolveOpenAIModel

// OpenAIProvider implements Provider using the OpenAI Chat Completions API SDK.
// Authentication is handled automatically via the OPENAI_API_KEY environment
// variable, which is read by the SDK on client construction.
type OpenAIProvider struct {
	client *openai.Client
}

// NewOpenAIProvider constructs an OpenAIProvider. The SDK reads OPENAI_API_KEY
// from the environment automatically.
func NewOpenAIProvider() *OpenAIProvider {
	apiKey := os.Getenv("OPENAI_API_KEY")
	client := openai.NewClient(option.WithAPIKey(apiKey))
	return &OpenAIProvider{client: &client}
}

// NewOpenAIProviderWithKey constructs an OpenAIProvider with an explicit API key.
// Use this for per-bot key overrides; the key bypasses the OPENAI_API_KEY env var.
func NewOpenAIProviderWithKey(apiKey string) *OpenAIProvider {
	client := openai.NewClient(option.WithAPIKey(apiKey))
	return &OpenAIProvider{client: &client}
}

// CodexProvider is a backwards-compatible alias for OpenAIProvider.
// It exists so that existing references to NewCodexProvider continue to work.
type CodexProvider = OpenAIProvider

// NewCodexProvider constructs an OpenAIProvider under the legacy Codex name.
func NewCodexProvider() *OpenAIProvider {
	return NewOpenAIProvider()
}

// Name returns "openai".
func (p *OpenAIProvider) Name() string { return "openai" }

// Complete sends req to the OpenAI Chat Completions API and returns the result.
//
// The system prompt is passed as a system message, and the user message as a
// user message. MaxTurns is used to derive max_completion_tokens (MaxTurns * 4096),
// defaulting to 8192 when MaxTurns is zero or negative.
//
// FullAgent and WorkDir are not supported by the Chat Completions API; a warning
// is logged when FullAgent is true.
func (p *OpenAIProvider) Complete(ctx context.Context, req *Request) (*Response, error) {
	if req.FullAgent {
		slog.Warn("OpenAIProvider: FullAgent mode is not supported by the Chat Completions API; ignoring",
			"work_dir", req.WorkDir,
		)
	}

	model := resolveOpenAIModel(req.Model)

	maxTokens := int64(8192)
	if req.MaxTurns > 0 {
		maxTokens = int64(req.MaxTurns) * 4096
	}

	// Build messages array: system prompt, history turns, then current user message.
	messages := make([]openai.ChatCompletionMessageParamUnion, 0, len(req.History)+2)
	if req.SystemPrompt != "" {
		messages = append(messages, openai.SystemMessage(req.SystemPrompt))
	}
	for _, h := range req.History {
		switch h.Role {
		case "user":
			messages = append(messages, openai.UserMessage(h.Content))
		case "assistant":
			messages = append(messages, openai.AssistantMessage(h.Content))
		}
	}
	messages = append(messages, openai.UserMessage(req.UserMessage))

	params := openai.ChatCompletionNewParams{
		Model:               openai.ChatModel(model),
		Messages:            messages,
		MaxCompletionTokens: openai.Int(maxTokens),
	}

	slog.Info("calling openai chat completions api",
		"model", model,
		"max_completion_tokens", maxTokens,
	)

	completion, err := p.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("openai api error: %w", err)
	}

	if len(completion.Choices) == 0 {
		return nil, fmt.Errorf("openai api returned no choices")
	}

	text := completion.Choices[0].Message.Content
	if text == "" {
		return nil, fmt.Errorf("openai api returned empty content")
	}

	usage := &UsageInfo{
		InputTokens:  int(completion.Usage.PromptTokens),
		OutputTokens: int(completion.Usage.CompletionTokens),
		Model:        completion.Model,
	}

	return &Response{Text: text, Usage: usage}, nil
}

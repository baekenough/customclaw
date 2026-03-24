package llm

import (
	"context"
	"fmt"
	"os"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

// openaiModelAliases maps short friendly names and Python codex aliases to
// OpenAI model IDs. Unknown aliases are passed through unchanged.
var openaiModelAliases = map[string]string{
	"gpt-5.4": "gpt-5.4",
	"gpt-4o":  "gpt-4o",
	"o3":      "o3",
}

// resolveOpenAIModel returns the canonical OpenAI model ID for a given alias.
// If the alias is not found in the map and is non-empty, it is returned as-is.
// An empty alias falls back to "gpt-4o".
func resolveOpenAIModel(alias string) string {
	if alias == "" {
		return "gpt-4o"
	}
	if full, ok := openaiModelAliases[alias]; ok {
		return full
	}
	return alias
}

// OpenAIProvider implements Provider using the official OpenAI Go SDK.
// It is used as the replacement for the Python codex CLI subprocess calls.
type OpenAIProvider struct {
	client openai.Client
}

// NewOpenAIProvider constructs an OpenAIProvider.
// If OPENAI_API_KEY is set in the environment, the SDK will pick it up
// automatically via option.WithAPIKey. Additional options may be passed to
// override the base URL, timeouts, or other request-level settings.
func NewOpenAIProvider(opts ...option.RequestOption) *OpenAIProvider {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey != "" {
		opts = append([]option.RequestOption{option.WithAPIKey(apiKey)}, opts...)
	}
	return &OpenAIProvider{
		client: openai.NewClient(opts...),
	}
}

// Name returns "openai".
func (p *OpenAIProvider) Name() string { return "openai" }

// Complete sends req to the OpenAI Chat Completions API and returns the
// response. An empty API key causes the underlying HTTP call to fail; the
// error is propagated to the caller so the provider can be constructed without
// a key and the failure surfaces only at call time.
func (p *OpenAIProvider) Complete(ctx context.Context, req *Request) (*Response, error) {
	modelID := resolveOpenAIModel(req.Model)

	messages := make([]openai.ChatCompletionMessageParamUnion, 0, 2)
	if req.SystemPrompt != "" {
		messages = append(messages, openai.SystemMessage(req.SystemPrompt))
	}
	messages = append(messages, openai.UserMessage(req.UserMessage))

	params := openai.ChatCompletionNewParams{
		Model:    openai.ChatModel(modelID),
		Messages: messages,
	}

	completion, err := p.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("openai chat.completions.new: %w", err)
	}

	text := ""
	if len(completion.Choices) > 0 {
		text = completion.Choices[0].Message.Content
	}

	usage := &UsageInfo{
		InputTokens:  int(completion.Usage.PromptTokens),
		OutputTokens: int(completion.Usage.CompletionTokens),
		Model:        completion.Model,
	}

	return &Response{Text: text, Usage: usage}, nil
}

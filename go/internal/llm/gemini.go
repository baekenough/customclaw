package llm

import (
	"context"
	"fmt"
	"os"

	"google.golang.org/genai"
)

// geminiModelAliases maps short friendly names to Gemini model IDs.
// Unknown aliases are passed through unchanged.
var geminiModelAliases = map[string]string{
	"gemini-3-pro":   "gemini-3.0-pro-preview",
	"gemini-2-flash": "gemini-2.0-flash",
}

// resolveGeminiModel returns the canonical Gemini model ID for a given alias.
// If the alias is not found in the map and is non-empty, it is returned as-is.
// An empty alias falls back to "gemini-2.0-flash".
func resolveGeminiModel(alias string) string {
	if alias == "" {
		return "gemini-2.0-flash"
	}
	if full, ok := geminiModelAliases[alias]; ok {
		return full
	}
	return alias
}

// GeminiProvider implements Provider using the official Google GenAI Go SDK.
// It is used as the replacement for the Python gemini CLI subprocess calls.
type GeminiProvider struct {
	// apiKey is stored so the client can be lazily initialised on the first
	// Complete call, keeping NewGeminiProvider non-failing.
	apiKey string
	// client is populated on the first Complete call. guarded implicitly by
	// the fact that Go's scheduler does not reorder stores within a goroutine,
	// and the provider is expected to be used from a single call site. For
	// concurrent use a sync.Once or sync.Mutex should be added.
	client *genai.Client
}

// NewGeminiProvider constructs a GeminiProvider.
// If apiKey is empty, the value of the GEMINI_API_KEY environment variable is
// used. The actual API client is not initialised until the first Complete call,
// so construction never returns an error.
func NewGeminiProvider(apiKey string) *GeminiProvider {
	if apiKey == "" {
		apiKey = os.Getenv("GEMINI_API_KEY")
	}
	return &GeminiProvider{apiKey: apiKey}
}

// Name returns "gemini".
func (p *GeminiProvider) Name() string { return "gemini" }

// initClient lazily creates the underlying genai.Client. An empty API key
// will cause the SDK to return an error at this point rather than at
// construction time, so the provider can be wired up unconditionally and the
// failure surfaces only when the provider is actually used.
func (p *GeminiProvider) initClient(ctx context.Context) error {
	if p.client != nil {
		return nil
	}
	c, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  p.apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return fmt.Errorf("gemini: create client: %w", err)
	}
	p.client = c
	return nil
}

// Complete sends req to the Gemini GenerateContent API and returns the
// response. The system prompt is injected via GenerateContentConfig so it is
// kept separate from the user turn.
func (p *GeminiProvider) Complete(ctx context.Context, req *Request) (*Response, error) {
	if err := p.initClient(ctx); err != nil {
		return nil, err
	}

	modelID := resolveGeminiModel(req.Model)

	contents := []*genai.Content{
		genai.NewContentFromText(req.UserMessage, genai.RoleUser),
	}

	cfg := &genai.GenerateContentConfig{}
	if req.SystemPrompt != "" {
		cfg.SystemInstruction = genai.NewContentFromText(req.SystemPrompt, genai.RoleUser)
	}

	resp, err := p.client.Models.GenerateContent(ctx, modelID, contents, cfg)
	if err != nil {
		return nil, fmt.Errorf("gemini generatecontent: %w", err)
	}

	text := resp.Text()

	usage := &UsageInfo{
		Model: modelID,
	}
	if resp.UsageMetadata != nil {
		usage.InputTokens = int(resp.UsageMetadata.PromptTokenCount)
		usage.OutputTokens = int(resp.UsageMetadata.CandidatesTokenCount)
	}

	return &Response{Text: text, Usage: usage}, nil
}

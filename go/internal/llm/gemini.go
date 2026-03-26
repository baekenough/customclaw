package llm

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"google.golang.org/genai"
)

// geminiModelAliases maps short friendly names to Gemini API model IDs.
// Unknown aliases are passed through unchanged.
var geminiModelAliases = map[string]string{
	"gemini-3-pro":   "gemini-3-pro-preview",
	"gemini-2-flash": "gemini-2.0-flash",
}

// resolveGeminiModel returns the Gemini API model ID for a given alias.
// If the alias is not found in the map and is non-empty, it is returned as-is.
// An empty alias falls back to "gemini-3-pro-preview".
func resolveGeminiModel(alias string) string {
	if alias == "" {
		return "gemini-3-pro-preview"
	}
	if full, ok := geminiModelAliases[alias]; ok {
		return full
	}
	return alias
}

// GeminiProvider implements Provider using the Google GenAI SDK.
// Authentication is handled via the GEMINI_API_KEY environment variable,
// which is read during Complete (when the client is constructed per-call).
// The SDK also accepts GOOGLE_API_KEY as a fallback.
type GeminiProvider struct{}

// NewGeminiProvider constructs a GeminiProvider.
func NewGeminiProvider() *GeminiProvider {
	return &GeminiProvider{}
}

// Name returns "gemini".
func (p *GeminiProvider) Name() string { return "gemini" }

// Complete sends req to the Google GenAI API and returns the result.
//
// The system prompt is passed via GenerateContentConfig.SystemInstruction.
// The user message is passed as user content. MaxTurns is used to derive
// MaxOutputTokens (MaxTurns * 4096), defaulting to 8192 when zero or negative.
//
// FullAgent and WorkDir are not supported; a warning is logged when FullAgent is true.
func (p *GeminiProvider) Complete(ctx context.Context, req *Request) (*Response, error) {
	if req.FullAgent {
		slog.Warn("GeminiProvider: FullAgent mode is not supported by the GenAI API; ignoring",
			"work_dir", req.WorkDir,
		)
	}

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("GOOGLE_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("gemini api key not set: GEMINI_API_KEY or GOOGLE_API_KEY must be provided")
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("gemini client error: %w", err)
	}

	model := resolveGeminiModel(req.Model)

	maxOutputTokens := int32(8192)
	if req.MaxTurns > 0 {
		maxOutputTokens = int32(req.MaxTurns) * 4096
	}

	cfg := &genai.GenerateContentConfig{
		MaxOutputTokens: maxOutputTokens,
	}

	if req.SystemPrompt != "" {
		cfg.SystemInstruction = genai.NewContentFromText(req.SystemPrompt, genai.RoleUser)
	}

	contents := []*genai.Content{
		genai.NewContentFromText(req.UserMessage, genai.RoleUser),
	}

	slog.Info("calling gemini api",
		"model", model,
		"max_output_tokens", maxOutputTokens,
	)

	resp, err := client.Models.GenerateContent(ctx, model, contents, cfg)
	if err != nil {
		return nil, fmt.Errorf("gemini api error: %w", err)
	}

	text := extractGeminiText(resp)
	if text == "" {
		return nil, fmt.Errorf("gemini api returned no text content")
	}

	usage := &UsageInfo{
		Model: model,
	}
	if resp.UsageMetadata != nil {
		usage.InputTokens = int(resp.UsageMetadata.PromptTokenCount)
		usage.OutputTokens = int(resp.UsageMetadata.CandidatesTokenCount)
	}

	return &Response{Text: text, Usage: usage}, nil
}

// extractGeminiText concatenates all text parts from the first candidate.
func extractGeminiText(resp *genai.GenerateContentResponse) string {
	if len(resp.Candidates) == 0 {
		return ""
	}
	candidate := resp.Candidates[0]
	if candidate.Content == nil {
		return ""
	}
	var out string
	for _, part := range candidate.Content.Parts {
		if part.Text != "" {
			if out != "" {
				out += "\n"
			}
			out += part.Text
		}
	}
	return out
}

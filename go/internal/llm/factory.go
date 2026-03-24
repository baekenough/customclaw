package llm

import "fmt"

// ProviderConfig holds authentication credentials for all supported LLM
// providers. Fields are populated from environment variables or explicit
// configuration at startup; zero values are handled gracefully by each
// provider constructor.
type ProviderConfig struct {
	// AnthropicOAuthPath is the path to a Claude Code .credentials.json file.
	// When non-empty, it takes precedence over ANTHROPIC_API_KEY.
	AnthropicOAuthPath string
	// OpenAIAPIKey overrides the OPENAI_API_KEY environment variable.
	OpenAIAPIKey string
	// GeminiAPIKey overrides the GEMINI_API_KEY environment variable.
	GeminiAPIKey string
}

// NewProvider creates the appropriate Provider for the given provider name.
// Supported names: "claude", "anthropic", "" (default to Anthropic),
// "codex", "openai", and "gemini".
//
// For Anthropic, OAuth credentials are preferred when AnthropicOAuthPath is
// set and the file can be read; the SDK falls back to the ANTHROPIC_API_KEY
// environment variable otherwise.
func NewProvider(name string, cfg ProviderConfig) (Provider, error) {
	switch name {
	case "claude", "anthropic", "":
		if cfg.AnthropicOAuthPath != "" {
			ts, err := NewOAuthTokenSource(cfg.AnthropicOAuthPath)
			if err == nil {
				return NewAnthropicProviderWithOAuth(ts), nil
			}
			// OAuth path supplied but unreadable — fall through to API key auth.
		}
		return NewAnthropicProvider(), nil

	case "codex", "openai":
		return NewOpenAIProvider(), nil

	case "gemini":
		return NewGeminiProvider(cfg.GeminiAPIKey), nil

	default:
		return nil, fmt.Errorf("unknown provider: %q", name)
	}
}

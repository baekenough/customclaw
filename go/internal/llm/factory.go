package llm

import "fmt"

// NewProvider creates the appropriate Provider for the given provider name.
// All providers use direct API SDK calls with keys from environment variables.
//
// Supported names:
//   - "claude", "anthropic", "" → ClaudeProvider (Anthropic Messages API)
//   - "openai"                  → OpenAIProvider (OpenAI Chat Completions API)
//   - "gemini"                  → GeminiProvider (Google GenAI API)
func NewProvider(name string) (Provider, error) {
	switch name {
	case "claude", "anthropic", "":
		return NewClaudeProvider(), nil
	case "openai":
		return NewOpenAIProvider(), nil
	case "gemini":
		return NewGeminiProvider(), nil
	default:
		return nil, fmt.Errorf("unknown provider: %q", name)
	}
}

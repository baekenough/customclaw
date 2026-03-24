package llm

import "fmt"

// NewProvider creates the appropriate Provider for the given provider name.
// All providers use CLI subprocess execution — authentication is handled by
// each CLI binary's own credential management.
//
// Supported names:
//   - "claude", "anthropic", "" → ClaudeProvider (Claude CLI)
//   - "codex", "openai"         → CodexProvider (Codex CLI)
//   - "gemini"                  → GeminiProvider (Gemini CLI)
func NewProvider(name string) (Provider, error) {
	switch name {
	case "claude", "anthropic", "":
		return NewClaudeProvider(), nil
	case "codex", "openai":
		return NewCodexProvider(), nil
	case "gemini":
		return NewGeminiProvider(), nil
	default:
		return nil, fmt.Errorf("unknown provider: %q", name)
	}
}

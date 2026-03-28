package llm

import (
	"fmt"
	"os"
	"os/exec"
)

// claudeCLIAvailable reports whether the claude binary can be found. It checks
// CLAUDE_CLI_PATH first, then falls back to PATH lookup.
func claudeCLIAvailable() bool {
	if p := os.Getenv("CLAUDE_CLI_PATH"); p != "" {
		_, err := exec.LookPath(p)
		return err == nil
	}
	_, err := exec.LookPath("claude")
	return err == nil
}

// codexCLIAvailable reports whether the codex binary can be found. It checks
// CODEX_CLI_PATH first, then falls back to PATH lookup.
func codexCLIAvailable() bool {
	if p := os.Getenv("CODEX_CLI_PATH"); p != "" {
		_, err := exec.LookPath(p)
		return err == nil
	}
	_, err := exec.LookPath("codex")
	return err == nil
}

// NewProvider creates the appropriate Provider for the given provider name.
// All providers use direct API SDK calls with keys from environment variables.
//
// Supported names:
//   - "claude-cli"              → ClaudeCLIProvider (claude binary via subprocess)
//   - "claude", "anthropic", "" → ClaudeCLIProvider when CLAUDE_CLI_PATH is set or
//     the claude binary is on PATH; otherwise ClaudeProvider (Anthropic Messages API)
//   - "openai"                  → OpenAIProvider (OpenAI Chat Completions API)
//   - "codex-cli"               → CodexCLIProvider (codex binary via subprocess)
//   - "gemini"                  → GeminiProvider (Google GenAI API)
func NewProvider(name string) (Provider, error) {
	switch name {
	case "claude-cli":
		return NewClaudeCLIProvider(), nil
	case "claude", "anthropic", "":
		if claudeCLIAvailable() {
			return NewClaudeCLIProvider(), nil
		}
		return NewClaudeProvider(), nil
	case "openai":
		return NewOpenAIProvider(), nil
	case "codex-cli":
		return NewCodexCLIProvider(), nil
	case "gemini":
		return NewGeminiProvider(), nil
	default:
		return nil, fmt.Errorf("unknown provider: %q", name)
	}
}

// NewProviderWithKey creates a Provider with an explicit API key.
// If apiKey is empty, it delegates to NewProvider (environment variable based).
//
// Supported names follow the same rules as NewProvider.
// Note: "codex-cli" ignores the explicit key and uses the environment (OPENAI_API_KEY
// or Codex CLI OAuth); there is no per-call key injection for subprocess-based providers.
func NewProviderWithKey(name, apiKey string) (Provider, error) {
	if apiKey == "" {
		return NewProvider(name)
	}
	switch name {
	case "claude", "anthropic", "":
		return NewClaudeProviderWithKey(apiKey), nil
	case "openai":
		return NewOpenAIProviderWithKey(apiKey), nil
	case "codex-cli":
		return NewCodexCLIProvider(), nil
	case "gemini":
		return NewGeminiProviderWithKey(apiKey), nil
	default:
		return nil, fmt.Errorf("unknown provider: %q", name)
	}
}

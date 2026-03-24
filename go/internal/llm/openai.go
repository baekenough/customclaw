// Package llm — openai.go is intentionally thin.
//
// The OpenAI / Codex provider is implemented in codex.go via the Codex CLI
// subprocess pattern. This file exists only to preserve the "openai" name in
// the factory for backwards-compatibility with any external config that
// specifies provider: "openai".
//
// NewOpenAIProvider is an alias for NewCodexProvider and returns a
// *CodexProvider whose Name() returns "codex".
package llm

// NewOpenAIProvider is a backwards-compatible alias for NewCodexProvider.
// It reads CODEX_CLI_PATH from the environment (default: "codex") and returns
// a *CodexProvider that shells out to the Codex CLI.
//
// Deprecated: prefer NewCodexProvider directly. This wrapper exists so that
// the factory case "openai" continues to work without changes to call sites.
func NewOpenAIProvider() *CodexProvider {
	return NewCodexProvider()
}

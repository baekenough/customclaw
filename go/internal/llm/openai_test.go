package llm

import "testing"

// TestNewOpenAIProvider_IsCodexProvider verifies that NewOpenAIProvider
// returns a *CodexProvider (the Codex CLI subprocess implementation) so that
// callers relying on the old "openai" name continue to work after the SDK
// was removed in favour of the CLI subprocess approach.
func TestNewOpenAIProvider_IsCodexProvider(t *testing.T) {
	t.Parallel()

	p := NewOpenAIProvider()
	if p == nil {
		t.Fatal("NewOpenAIProvider() returned nil")
	}
	// NewOpenAIProvider is an alias for NewCodexProvider; the provider name
	// must be "codex" (not "openai") because the backing implementation is
	// the Codex CLI, not the OpenAI REST API.
	if p.Name() != "codex" {
		t.Errorf("Name() = %q, want %q", p.Name(), "codex")
	}
}

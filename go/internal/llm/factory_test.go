package llm

import (
	"testing"
)

func TestNewProvider_Claude(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"claude", "anthropic", ""} {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p, err := NewProvider(name)
			if err != nil {
				t.Fatalf("NewProvider(%q) error: %v", name, err)
			}
			if p.Name() != "claude" {
				t.Errorf("Name() = %q, want %q", p.Name(), "claude")
			}
		})
	}
}

func TestNewProvider_OpenAI(t *testing.T) {
	t.Parallel()
	// "openai" maps to the OpenAI Chat Completions API provider.
	// Name() returns "openai".
	p, err := NewProvider("openai")
	if err != nil {
		t.Fatalf("NewProvider(\"openai\") error: %v", err)
	}
	if p.Name() != "openai" {
		t.Errorf("Name() = %q, want %q", p.Name(), "openai")
	}
}

func TestNewProvider_Gemini(t *testing.T) {
	t.Parallel()
	p, err := NewProvider("gemini")
	if err != nil {
		t.Fatalf("NewProvider(\"gemini\") error: %v", err)
	}
	if p.Name() != "gemini" {
		t.Errorf("Name() = %q, want %q", p.Name(), "gemini")
	}
}

func TestNewProvider_Unknown(t *testing.T) {
	t.Parallel()
	_, err := NewProvider("unknown-llm")
	if err == nil {
		t.Fatal("NewProvider(\"unknown-llm\") expected error, got nil")
	}
}

// TestNewProvider_Codex verifies that the old "codex" name now returns
// an unknown provider error (callers should use "openai" instead).
func TestNewProvider_Codex_Removed(t *testing.T) {
	t.Parallel()
	_, err := NewProvider("codex")
	if err == nil {
		t.Fatal("NewProvider(\"codex\") expected error after codex alias removal, got nil")
	}
}

func TestNewProvider_CodexCLI(t *testing.T) {
	t.Parallel()
	p, err := NewProvider("codex-cli")
	if err != nil {
		t.Fatalf("NewProvider(\"codex-cli\") error: %v", err)
	}
	if p.Name() != "codex-cli" {
		t.Errorf("Name() = %q, want %q", p.Name(), "codex-cli")
	}
}

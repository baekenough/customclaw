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

func TestNewProvider_Codex(t *testing.T) {
	t.Parallel()
	// Both "codex" and "openai" map to the Codex CLI subprocess provider.
	// The provider Name() returns "codex" (not "openai") because the backing
	// implementation uses the Codex CLI, not the OpenAI REST API SDK.
	for _, name := range []string{"codex", "openai"} {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p, err := NewProvider(name)
			if err != nil {
				t.Fatalf("NewProvider(%q) error: %v", name, err)
			}
			if p.Name() != "codex" {
				t.Errorf("Name() = %q, want %q", p.Name(), "codex")
			}
		})
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

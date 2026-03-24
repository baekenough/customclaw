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
			p, err := NewProvider(name, ProviderConfig{})
			if err != nil {
				t.Fatalf("NewProvider(%q) error: %v", name, err)
			}
			if p.Name() != "anthropic" {
				t.Errorf("Name() = %q, want %q", p.Name(), "anthropic")
			}
		})
	}
}

func TestNewProvider_OpenAI(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"codex", "openai"} {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p, err := NewProvider(name, ProviderConfig{})
			if err != nil {
				t.Fatalf("NewProvider(%q) error: %v", name, err)
			}
			if p.Name() != "openai" {
				t.Errorf("Name() = %q, want %q", p.Name(), "openai")
			}
		})
	}
}

func TestNewProvider_Gemini(t *testing.T) {
	t.Parallel()
	p, err := NewProvider("gemini", ProviderConfig{GeminiAPIKey: "test-key"})
	if err != nil {
		t.Fatalf("NewProvider(\"gemini\") error: %v", err)
	}
	if p.Name() != "gemini" {
		t.Errorf("Name() = %q, want %q", p.Name(), "gemini")
	}
}

func TestNewProvider_Unknown(t *testing.T) {
	t.Parallel()
	_, err := NewProvider("unknown-llm", ProviderConfig{})
	if err == nil {
		t.Fatal("NewProvider(\"unknown-llm\") expected error, got nil")
	}
}

func TestNewProvider_AnthropicOAuthFallback(t *testing.T) {
	t.Parallel()
	// A non-existent path should fall back to the API-key-based provider
	// without returning an error.
	p, err := NewProvider("claude", ProviderConfig{
		AnthropicOAuthPath: "/non/existent/path/.credentials.json",
	})
	if err != nil {
		t.Fatalf("NewProvider with bad oauth path error: %v", err)
	}
	if p.Name() != "anthropic" {
		t.Errorf("Name() = %q, want %q", p.Name(), "anthropic")
	}
}

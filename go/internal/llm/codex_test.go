package llm

import (
	"testing"
)

// ---------------------------------------------------------------------------
// resolveOpenAIModel (formerly resolveCodexModel)
// ---------------------------------------------------------------------------

func TestResolveCodexModel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		alias string
		want  string
	}{
		// Explicit aliases.
		{"gpt-5.4", "gpt-5.4"},
		{"codex", "gpt-5.4"},
		{"gpt-4o", "gpt-4o"},
		// Unknown aliases pass through unchanged.
		{"gpt-4-turbo", "gpt-4-turbo"},
		{"o3", "o3"},
		// Empty alias falls back to the default model.
		{"", "gpt-5.4"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.alias, func(t *testing.T) {
			t.Parallel()
			got := resolveCodexModel(tc.alias)
			if got != tc.want {
				t.Errorf("resolveCodexModel(%q) = %q, want %q", tc.alias, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// NewCodexProvider / NewOpenAIProvider — construction
// ---------------------------------------------------------------------------

func TestNewCodexProvider_ReturnsNonNil(t *testing.T) {
	t.Parallel()
	p := NewCodexProvider()
	if p == nil {
		t.Fatal("NewCodexProvider() returned nil")
	}
}

func TestNewCodexProvider_Name(t *testing.T) {
	t.Parallel()
	p := NewCodexProvider()
	// CodexProvider is now an alias for OpenAIProvider; Name() returns "openai".
	if p.Name() != "openai" {
		t.Errorf("Name() = %q, want %q", p.Name(), "openai")
	}
}

func TestNewOpenAIProvider_ReturnsNonNil(t *testing.T) {
	t.Parallel()
	p := NewOpenAIProvider()
	if p == nil {
		t.Fatal("NewOpenAIProvider() returned nil")
	}
}

// ---------------------------------------------------------------------------
// Provider interface compliance (compile-time checks)
// ---------------------------------------------------------------------------

var _ Provider = (*OpenAIProvider)(nil)
var _ Provider = (*CodexProvider)(nil)

// TestOpenAIProvider_ImplementsProvider verifies that OpenAIProvider satisfies
// the Provider interface at runtime.
func TestOpenAIProvider_ImplementsProvider(t *testing.T) {
	t.Parallel()
	var p Provider = NewOpenAIProvider()
	if p.Name() != "openai" {
		t.Errorf("Name() = %q, want %q", p.Name(), "openai")
	}
}

// TestCodexProvider_ImplementsProvider verifies that CodexProvider (= OpenAIProvider)
// satisfies the Provider interface at runtime.
func TestCodexProvider_ImplementsProvider(t *testing.T) {
	t.Parallel()
	var p Provider = NewCodexProvider()
	if p.Name() != "openai" {
		t.Errorf("Name() = %q, want %q", p.Name(), "openai")
	}
}

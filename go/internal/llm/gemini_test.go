package llm

import (
	"testing"
)

// ---------------------------------------------------------------------------
// resolveGeminiModel
// ---------------------------------------------------------------------------

func TestResolveGeminiModel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		alias string
		want  string
	}{
		{"gemini-3-pro", "gemini-3-pro-preview"},
		{"gemini-2-flash", "gemini-2.0-flash"},
		// Unknown aliases pass through unchanged.
		{"gemini-1.5-pro", "gemini-1.5-pro"},
		// Empty alias falls back to the default model.
		{"", "gemini-3-pro-preview"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.alias, func(t *testing.T) {
			t.Parallel()
			got := resolveGeminiModel(tc.alias)
			if got != tc.want {
				t.Errorf("resolveGeminiModel(%q) = %q, want %q", tc.alias, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// NewGeminiProvider — construction
// ---------------------------------------------------------------------------

func TestNewGeminiProvider_ReturnsNonNil(t *testing.T) {
	t.Parallel()
	p := NewGeminiProvider()
	if p == nil {
		t.Fatal("NewGeminiProvider() returned nil")
	}
}

func TestNewGeminiProvider_Name(t *testing.T) {
	t.Parallel()
	p := NewGeminiProvider()
	if p.Name() != "gemini" {
		t.Errorf("Name() = %q, want %q", p.Name(), "gemini")
	}
}

// ---------------------------------------------------------------------------
// Provider interface compliance (compile-time check)
// ---------------------------------------------------------------------------

var _ Provider = (*GeminiProvider)(nil)

// TestGeminiProvider_ImplementsProvider verifies that GeminiProvider satisfies
// the Provider interface at runtime.
func TestGeminiProvider_ImplementsProvider(t *testing.T) {
	t.Parallel()
	var p Provider = NewGeminiProvider()
	if p.Name() != "gemini" {
		t.Errorf("Name() = %q, want %q", p.Name(), "gemini")
	}
}

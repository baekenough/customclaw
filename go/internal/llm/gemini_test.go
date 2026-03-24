package llm

import "testing"

func TestResolveGeminiModel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		alias string
		want  string
	}{
		{"gemini-3-pro", "gemini-3.0-pro-preview"},
		{"gemini-2-flash", "gemini-2.0-flash"},
		// Unknown aliases pass through unchanged.
		{"gemini-1.5-pro", "gemini-1.5-pro"},
		// Empty alias falls back to the default model.
		{"", "gemini-2.0-flash"},
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

func TestNewGeminiProvider_DoesNotPanic(t *testing.T) {
	t.Parallel()
	// Construction must succeed even with no API key provided.
	p := NewGeminiProvider("")
	if p == nil {
		t.Fatal("NewGeminiProvider(\"\") returned nil")
	}
	if p.Name() != "gemini" {
		t.Errorf("Name() = %q, want %q", p.Name(), "gemini")
	}
}

func TestNewGeminiProvider_ExplicitKey(t *testing.T) {
	t.Parallel()
	p := NewGeminiProvider("test-key-123")
	if p == nil {
		t.Fatal("NewGeminiProvider(key) returned nil")
	}
	if p.apiKey != "test-key-123" {
		t.Errorf("apiKey = %q, want %q", p.apiKey, "test-key-123")
	}
}

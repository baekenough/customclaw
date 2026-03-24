package llm

import "testing"

func TestResolveOpenAIModel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		alias string
		want  string
	}{
		{"gpt-5.4", "gpt-5.4"},
		{"gpt-4o", "gpt-4o"},
		{"o3", "o3"},
		// Unknown aliases pass through unchanged.
		{"gpt-4-turbo", "gpt-4-turbo"},
		// Empty alias falls back to the default model.
		{"", "gpt-4o"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.alias, func(t *testing.T) {
			t.Parallel()
			got := resolveOpenAIModel(tc.alias)
			if got != tc.want {
				t.Errorf("resolveOpenAIModel(%q) = %q, want %q", tc.alias, got, tc.want)
			}
		})
	}
}

func TestNewOpenAIProvider_DoesNotPanic(t *testing.T) {
	t.Parallel()
	// Construction must succeed even with no API key in the environment.
	p := NewOpenAIProvider()
	if p == nil {
		t.Fatal("NewOpenAIProvider() returned nil")
	}
	if p.Name() != "openai" {
		t.Errorf("Name() = %q, want %q", p.Name(), "openai")
	}
}

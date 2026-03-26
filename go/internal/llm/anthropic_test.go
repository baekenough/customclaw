package llm

import (
	"testing"
)

// ---------------------------------------------------------------------------
// resolveClaudeModel
// ---------------------------------------------------------------------------

func TestResolveClaudeModel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		alias string
		want  string
	}{
		{"opus", "claude-opus-4-6"},
		{"sonnet", "claude-sonnet-4-6"},
		{"haiku", "claude-haiku-4-5-20251001"},
		// Unknown aliases pass through unchanged.
		{"claude-3-5-sonnet-20241022", "claude-3-5-sonnet-20241022"},
		// Empty alias falls back to the default model (sonnet).
		{"", "claude-sonnet-4-6"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.alias, func(t *testing.T) {
			t.Parallel()
			got := resolveClaudeModel(tc.alias)
			if got != tc.want {
				t.Errorf("resolveClaudeModel(%q) = %q, want %q", tc.alias, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// NewClaudeProvider — construction
// ---------------------------------------------------------------------------

func TestNewClaudeProvider_ReturnsNonNil(t *testing.T) {
	t.Parallel()
	p := NewClaudeProvider()
	if p == nil {
		t.Fatal("NewClaudeProvider() returned nil")
	}
}

func TestNewClaudeProvider_Name(t *testing.T) {
	t.Parallel()
	p := NewClaudeProvider()
	if p.Name() != "claude" {
		t.Errorf("Name() = %q, want %q", p.Name(), "claude")
	}
}

// ---------------------------------------------------------------------------
// Provider interface compliance (compile-time check)
// ---------------------------------------------------------------------------

var _ Provider = (*ClaudeProvider)(nil)

// TestClaudeProvider_ImplementsProvider verifies that ClaudeProvider satisfies
// the Provider interface at runtime.
func TestClaudeProvider_ImplementsProvider(t *testing.T) {
	t.Parallel()
	var p Provider = NewClaudeProvider()
	if p.Name() != "claude" {
		t.Errorf("Name() = %q, want %q", p.Name(), "claude")
	}
}

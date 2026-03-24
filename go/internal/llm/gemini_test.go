package llm

import (
	"os"
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

func TestNewGeminiProvider_DefaultPath(t *testing.T) {
	// t.Setenv modifies a process-wide variable: cannot run in parallel.
	t.Setenv("GEMINI_CLI_PATH", "")

	p := NewGeminiProvider()
	if p == nil {
		t.Fatal("NewGeminiProvider() returned nil")
	}
	if p.cliPath != "gemini" {
		t.Errorf("cliPath = %q, want %q", p.cliPath, "gemini")
	}
	if p.Name() != "gemini" {
		t.Errorf("Name() = %q, want %q", p.Name(), "gemini")
	}
}

func TestNewGeminiProvider_CustomPath(t *testing.T) {
	// t.Setenv modifies a process-wide variable: cannot run in parallel.
	t.Setenv("GEMINI_CLI_PATH", "/usr/local/bin/gemini-custom")

	p := NewGeminiProvider()
	if p.cliPath != "/usr/local/bin/gemini-custom" {
		t.Errorf("cliPath = %q, want %q", p.cliPath, "/usr/local/bin/gemini-custom")
	}
}

func TestNewGeminiProvider_PicksUpEnvVar(t *testing.T) {
	// t.Setenv modifies a process-wide variable: cannot run in parallel.
	const customPath = "/opt/gemini/bin/gemini"
	t.Setenv("GEMINI_CLI_PATH", customPath)

	p := NewGeminiProvider()
	if p.cliPath != customPath {
		t.Errorf("expected cliPath=%q from env var, got %q", customPath, p.cliPath)
	}
}

func TestNewGeminiProvider_ContainerHomeNotRequired(t *testing.T) {
	t.Parallel()
	// Construction should succeed regardless of CONTAINER_HOME.
	_ = os.Unsetenv("CONTAINER_HOME")
	p := NewGeminiProvider()
	if p == nil {
		t.Fatal("NewGeminiProvider() returned nil when CONTAINER_HOME unset")
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

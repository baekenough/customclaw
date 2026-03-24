package llm

import (
	"os"
	"testing"
)

// ---------------------------------------------------------------------------
// resolveCodexModel
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
// NewCodexProvider — construction
// ---------------------------------------------------------------------------

func TestNewCodexProvider_DefaultPath(t *testing.T) {
	// t.Setenv modifies a process-wide variable: cannot run in parallel.
	t.Setenv("CODEX_CLI_PATH", "")

	p := NewCodexProvider()
	if p == nil {
		t.Fatal("NewCodexProvider() returned nil")
	}
	if p.cliPath != "codex" {
		t.Errorf("cliPath = %q, want %q", p.cliPath, "codex")
	}
	if p.Name() != "codex" {
		t.Errorf("Name() = %q, want %q", p.Name(), "codex")
	}
}

func TestNewCodexProvider_CustomPath(t *testing.T) {
	// t.Setenv modifies a process-wide variable: cannot run in parallel.
	t.Setenv("CODEX_CLI_PATH", "/usr/local/bin/codex-custom")

	p := NewCodexProvider()
	if p.cliPath != "/usr/local/bin/codex-custom" {
		t.Errorf("cliPath = %q, want %q", p.cliPath, "/usr/local/bin/codex-custom")
	}
}

func TestNewCodexProvider_PicksUpEnvVar(t *testing.T) {
	// t.Setenv modifies a process-wide variable: cannot run in parallel.
	// Verify that CODEX_CLI_PATH is read at construction time, not lazily.
	const customPath = "/opt/codex/bin/codex"
	t.Setenv("CODEX_CLI_PATH", customPath)

	p := NewCodexProvider()
	if p.cliPath != customPath {
		t.Errorf("expected cliPath=%q from env var, got %q", customPath, p.cliPath)
	}
}

// ---------------------------------------------------------------------------
// stripCodexMetadata
// ---------------------------------------------------------------------------

func TestStripCodexMetadata_RemovesHeader(t *testing.T) {
	t.Parallel()

	input := `OpenAI Codex
------------------------------------------------------------------------
workdir:   /repo
model:     gpt-5.4
provider:  openai
approval:  auto-approve-all
sandbox:   off
reasoning  high
session id: abc123

This is the actual response.
It spans multiple lines.`

	got := stripCodexMetadata(input)
	want := "This is the actual response.\nIt spans multiple lines."
	if got != want {
		t.Errorf("stripCodexMetadata output mismatch\ngot:  %q\nwant: %q", got, want)
	}
}

func TestStripCodexMetadata_RemovesFooter(t *testing.T) {
	t.Parallel()

	input := `Real response content here.

codex
tokens used
1234`

	got := stripCodexMetadata(input)
	want := "Real response content here."
	if got != want {
		t.Errorf("stripCodexMetadata footer removal mismatch\ngot:  %q\nwant: %q", got, want)
	}
}

func TestStripCodexMetadata_PreservesContent(t *testing.T) {
	t.Parallel()

	// Simulate full Codex output: header + content + footer.
	input := `OpenAI Codex
------------------------------------------------------------------------
workdir:  /tmp
model:    gpt-5.4
provider: openai
approval: auto
sandbox:  off
session id: xyz

Here is my answer:

1. First point
2. Second point

codex
tokens used
999`

	got := stripCodexMetadata(input)
	want := "Here is my answer:\n\n1. First point\n2. Second point"
	if got != want {
		t.Errorf("stripCodexMetadata full output mismatch\ngot:  %q\nwant: %q", got, want)
	}
}

func TestStripCodexMetadata_NoMetadata(t *testing.T) {
	t.Parallel()

	// If there is no metadata at all, the content should be returned as-is.
	input := "Just a plain response without any header."
	got := stripCodexMetadata(input)
	if got != input {
		t.Errorf("stripCodexMetadata modified plain input\ngot:  %q\nwant: %q", got, input)
	}
}

func TestStripCodexMetadata_EmptyOutput(t *testing.T) {
	t.Parallel()

	got := stripCodexMetadata("")
	if got != "" {
		t.Errorf("stripCodexMetadata(\"\") = %q, want empty string", got)
	}
}

func TestStripCodexMetadata_OnlyHeader(t *testing.T) {
	t.Parallel()

	// When the CLI output contains only metadata with no real content, the
	// result should be an empty string (trimmed).
	input := `OpenAI Codex
------------------------------------------------------------------------
workdir:  /tmp
model:    gpt-5.4
provider: openai`

	got := stripCodexMetadata(input)
	if got != "" {
		t.Errorf("stripCodexMetadata(header-only) = %q, want empty string", got)
	}
}

// ---------------------------------------------------------------------------
// isAllDigits (internal helper)
// ---------------------------------------------------------------------------

func TestIsAllDigits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		s    string
		want bool
	}{
		{"", false},
		{"0", true},
		{"1234567890", true},
		{"12a3", false},
		{" 123", false},
		{"123 ", false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.s, func(t *testing.T) {
			t.Parallel()
			got := isAllDigits(tc.s)
			if got != tc.want {
				t.Errorf("isAllDigits(%q) = %v, want %v", tc.s, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// appendOrReplace (internal helper)
// ---------------------------------------------------------------------------

func TestAppendOrReplace_Append(t *testing.T) {
	t.Parallel()

	env := []string{"FOO=1", "BAR=2"}
	result := appendOrReplace(env, "NEW=3")
	if len(result) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(result))
	}
	if result[2] != "NEW=3" {
		t.Errorf("result[2] = %q, want %q", result[2], "NEW=3")
	}
}

func TestAppendOrReplace_Replace(t *testing.T) {
	t.Parallel()

	env := []string{"FOO=1", "HOME=/old", "BAR=2"}
	result := appendOrReplace(env, "HOME=/new")
	if len(result) != 3 {
		t.Fatalf("expected 3 entries after replace, got %d", len(result))
	}
	if result[1] != "HOME=/new" {
		t.Errorf("result[1] = %q, want %q", result[1], "HOME=/new")
	}
}

// ---------------------------------------------------------------------------
// Provider interface compliance (compile-time check)
// ---------------------------------------------------------------------------

var _ Provider = (*CodexProvider)(nil)

// TestCodexProvider_ImplementsProvider verifies that CodexProvider satisfies
// the Provider interface at runtime too.
func TestCodexProvider_ImplementsProvider(t *testing.T) {
	t.Parallel()
	var p Provider = NewCodexProvider()
	if p.Name() != "codex" {
		t.Errorf("Name() = %q, want %q", p.Name(), "codex")
	}
}

// ---------------------------------------------------------------------------
// CONTAINER_HOME env var handling
// ---------------------------------------------------------------------------

func TestNewCodexProvider_ContainerHomeNotRequired(t *testing.T) {
	t.Parallel()
	// Construction should succeed regardless of CONTAINER_HOME.
	_ = os.Unsetenv("CONTAINER_HOME")
	p := NewCodexProvider()
	if p == nil {
		t.Fatal("NewCodexProvider() returned nil when CONTAINER_HOME unset")
	}
}

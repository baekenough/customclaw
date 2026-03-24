package llm

import (
	"encoding/json"
	"os"
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
		{"opus", "opus"},
		{"sonnet", "sonnet"},
		{"haiku", "haiku"},
		// Unknown aliases pass through unchanged.
		{"claude-3-5-sonnet-20241022", "claude-3-5-sonnet-20241022"},
		// Empty alias falls back to the default model.
		{"", "sonnet"},
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

func TestNewClaudeProvider_DefaultPath(t *testing.T) {
	// t.Setenv modifies a process-wide variable: cannot run in parallel.
	t.Setenv("CLAUDE_CLI_PATH", "")

	p := NewClaudeProvider()
	if p == nil {
		t.Fatal("NewClaudeProvider() returned nil")
	}
	if p.cliPath != "claude" {
		t.Errorf("cliPath = %q, want %q", p.cliPath, "claude")
	}
	if p.Name() != "claude" {
		t.Errorf("Name() = %q, want %q", p.Name(), "claude")
	}
}

func TestNewClaudeProvider_CustomPath(t *testing.T) {
	// t.Setenv modifies a process-wide variable: cannot run in parallel.
	t.Setenv("CLAUDE_CLI_PATH", "/usr/local/bin/claude-custom")

	p := NewClaudeProvider()
	if p.cliPath != "/usr/local/bin/claude-custom" {
		t.Errorf("cliPath = %q, want %q", p.cliPath, "/usr/local/bin/claude-custom")
	}
}

func TestNewClaudeProvider_PicksUpEnvVar(t *testing.T) {
	// t.Setenv modifies a process-wide variable: cannot run in parallel.
	const customPath = "/opt/claude/bin/claude"
	t.Setenv("CLAUDE_CLI_PATH", customPath)

	p := NewClaudeProvider()
	if p.cliPath != customPath {
		t.Errorf("expected cliPath=%q from env var, got %q", customPath, p.cliPath)
	}
}

func TestNewClaudeProvider_ContainerHomeNotRequired(t *testing.T) {
	t.Parallel()
	// Construction should succeed regardless of CONTAINER_HOME.
	_ = os.Unsetenv("CONTAINER_HOME")
	p := NewClaudeProvider()
	if p == nil {
		t.Fatal("NewClaudeProvider() returned nil when CONTAINER_HOME unset")
	}
}

// ---------------------------------------------------------------------------
// parseClaudeJSONOutput
// ---------------------------------------------------------------------------

func TestParseClaudeJSONOutput_ValidResult(t *testing.T) {
	t.Parallel()

	payload := map[string]any{
		"type":    "result",
		"subtype": "success",
		"result":  "Here is the answer.",
		"usage": map[string]any{
			"input_tokens":               float64(100),
			"output_tokens":              float64(50),
			"cache_read_input_tokens":    float64(10),
			"cache_creation_input_tokens": float64(5),
		},
		"total_cost_usd": float64(0.0025),
		"modelUsage": map[string]any{
			"claude-sonnet-4-20250514": map[string]any{},
		},
	}
	raw, _ := json.Marshal(payload)

	text, usage := parseClaudeJSONOutput(string(raw))

	if text != "Here is the answer." {
		t.Errorf("text = %q, want %q", text, "Here is the answer.")
	}
	if usage == nil {
		t.Fatal("usage is nil, want non-nil")
	}
	if usage.InputTokens != 100 {
		t.Errorf("InputTokens = %d, want 100", usage.InputTokens)
	}
	if usage.OutputTokens != 50 {
		t.Errorf("OutputTokens = %d, want 50", usage.OutputTokens)
	}
	if usage.CacheReadTokens != 10 {
		t.Errorf("CacheReadTokens = %d, want 10", usage.CacheReadTokens)
	}
	if usage.CacheCreationTokens != 5 {
		t.Errorf("CacheCreationTokens = %d, want 5", usage.CacheCreationTokens)
	}
	if usage.CostUSD == nil || *usage.CostUSD != 0.0025 {
		t.Errorf("CostUSD = %v, want 0.0025", usage.CostUSD)
	}
	if usage.Model != "claude-sonnet-4-20250514" {
		t.Errorf("Model = %q, want %q", usage.Model, "claude-sonnet-4-20250514")
	}
}

func TestParseClaudeJSONOutput_ErrorMaxTurns(t *testing.T) {
	t.Parallel()

	payload := map[string]any{
		"type":    "result",
		"subtype": "error_max_turns",
	}
	raw, _ := json.Marshal(payload)

	text, usage := parseClaudeJSONOutput(string(raw))

	if text != "" {
		t.Errorf("text = %q, want empty string for error response", text)
	}
	if usage == nil {
		t.Fatal("usage is nil, want non-nil UsageInfo")
	}
}

func TestParseClaudeJSONOutput_PlainText(t *testing.T) {
	t.Parallel()

	// Non-JSON output should be returned as-is.
	plain := "This is a plain text response."
	text, usage := parseClaudeJSONOutput(plain)

	if text != plain {
		t.Errorf("text = %q, want %q", text, plain)
	}
	if usage != nil {
		t.Errorf("usage = %v, want nil for plain text", usage)
	}
}

func TestParseClaudeJSONOutput_JSONWithoutResultField(t *testing.T) {
	t.Parallel()

	// Valid JSON but no "result" key and no "type"="result" — return raw as text.
	payload := map[string]any{
		"some_other_field": "value",
	}
	raw, _ := json.Marshal(payload)
	rawStr := string(raw)

	text, _ := parseClaudeJSONOutput(rawStr)
	if text != rawStr {
		t.Errorf("text = %q, want raw JSON %q", text, rawStr)
	}
}

// ---------------------------------------------------------------------------
// extractUsageFromCLIJSON
// ---------------------------------------------------------------------------

func TestExtractUsageFromCLIJSON_AllFields(t *testing.T) {
	t.Parallel()

	cost := 0.001
	data := map[string]any{
		"usage": map[string]any{
			"input_tokens":               float64(200),
			"output_tokens":              float64(80),
			"cache_read_input_tokens":    float64(15),
			"cache_creation_input_tokens": float64(7),
		},
		"total_cost_usd": cost,
		"modelUsage": map[string]any{
			"claude-opus-4-20250514": map[string]any{},
		},
	}

	usage := extractUsageFromCLIJSON(data)

	if usage.InputTokens != 200 {
		t.Errorf("InputTokens = %d, want 200", usage.InputTokens)
	}
	if usage.OutputTokens != 80 {
		t.Errorf("OutputTokens = %d, want 80", usage.OutputTokens)
	}
	if usage.CostUSD == nil || *usage.CostUSD != cost {
		t.Errorf("CostUSD = %v, want %v", usage.CostUSD, cost)
	}
	if usage.Model != "claude-opus-4-20250514" {
		t.Errorf("Model = %q, want claude-opus-4-20250514", usage.Model)
	}
}

func TestExtractUsageFromCLIJSON_EmptyData(t *testing.T) {
	t.Parallel()

	usage := extractUsageFromCLIJSON(map[string]any{})

	if usage == nil {
		t.Fatal("extractUsageFromCLIJSON({}) returned nil")
	}
	if usage.InputTokens != 0 || usage.OutputTokens != 0 {
		t.Errorf("expected zero tokens for empty data, got input=%d output=%d",
			usage.InputTokens, usage.OutputTokens)
	}
	if usage.CostUSD != nil {
		t.Errorf("CostUSD should be nil for empty data, got %v", usage.CostUSD)
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

package credprobe

import (
	"context"
	"testing"
)

// TestCheckOpenAI_Unconfigured verifies that an absent OPENAI_API_KEY yields
// "unconfigured" without making any network calls.
func TestCheckOpenAI_Unconfigured(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")

	result := checkOpenAI(context.Background())

	if result.status != "unconfigured" {
		t.Errorf("expected status %q, got %q", "unconfigured", result.status)
	}
	if result.errMsg != "" {
		t.Errorf("expected empty errMsg, got %q", result.errMsg)
	}
}

// TestCheckGemini_Unconfigured verifies that an absent GEMINI_API_KEY yields
// "unconfigured" without making any network calls.
func TestCheckGemini_Unconfigured(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")

	result := checkGemini(context.Background())

	if result.status != "unconfigured" {
		t.Errorf("expected status %q, got %q", "unconfigured", result.status)
	}
	if result.errMsg != "" {
		t.Errorf("expected empty errMsg, got %q", result.errMsg)
	}
}

// TestCheckClaude_NotFound verifies that a nonexistent CLI path returns "error"
// with an informative message, rather than panicking or hanging.
func TestCheckClaude_NotFound(t *testing.T) {
	t.Setenv("CLAUDE_CLI_PATH", "/nonexistent/path/to/claude-cli-that-does-not-exist")

	result := checkClaude(context.Background())

	if result.status != "error" {
		t.Errorf("expected status %q, got %q", "error", result.status)
	}
	if result.errMsg == "" {
		t.Error("expected non-empty errMsg when CLI path does not exist")
	}
}

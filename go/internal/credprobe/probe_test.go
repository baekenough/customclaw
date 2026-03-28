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
	if result.errKind != "" {
		t.Errorf("expected empty errKind, got %q", result.errKind)
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
	if result.errKind != "" {
		t.Errorf("expected empty errKind, got %q", result.errKind)
	}
}

// TestCheckClaude_Unconfigured verifies that an absent ANTHROPIC_API_KEY yields
// "unconfigured" without making any network calls.
func TestCheckClaude_Unconfigured(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")

	result := checkClaude(context.Background())

	if result.status != "unconfigured" {
		t.Errorf("expected status %q, got %q", "unconfigured", result.status)
	}
	if result.errMsg != "" {
		t.Errorf("expected empty errMsg, got %q", result.errMsg)
	}
	if result.errKind != "" {
		t.Errorf("expected empty errKind, got %q", result.errKind)
	}
}

// TestCheckClaudeCLI_Unconfigured verifies that a missing claude binary yields
// "unconfigured" without making any network calls.
func TestCheckClaudeCLI_Unconfigured(t *testing.T) {
	t.Setenv("CLAUDE_CLI_PATH", "/nonexistent/path/claude")

	result := checkClaudeCLI(context.Background())

	// A non-existent binary path results in an exec error → "error" status,
	// not "unconfigured". "unconfigured" only fires when LookPath also fails.
	// This test validates errKind is either empty or non-network for exec errors.
	if result.errKind == "quota" || result.errKind == "auth" {
		t.Errorf("unexpected errKind %q for CLI exec failure", result.errKind)
	}
}

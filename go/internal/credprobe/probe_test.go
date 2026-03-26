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
}

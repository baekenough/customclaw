package llm

import (
	"testing"
)

func TestResolveCodexCLIModel_Aliases(t *testing.T) {
	tests := []struct {
		alias string
		want  string
	}{
		{"gpt-5.4", "gpt-5.4"},
		{"codex", "gpt-5.4"},
		{"codex-mini", "gpt-5.4-mini"},
		{"gpt-5.3-codex", "gpt-5.3-codex"},
		{"", "gpt-5.4"},
		{"custom-model", "custom-model"},
	}
	for _, tt := range tests {
		got := resolveCodexCLIModel(tt.alias)
		if got != tt.want {
			t.Errorf("resolveCodexCLIModel(%q) = %q, want %q", tt.alias, got, tt.want)
		}
	}
}

func TestNewCodexCLIProvider_DefaultPath(t *testing.T) {
	t.Setenv("CODEX_CLI_PATH", "")
	p := NewCodexCLIProvider()
	if p.cliPath != "codex" {
		t.Errorf("cliPath = %q, want %q", p.cliPath, "codex")
	}
}

func TestNewCodexCLIProvider_CustomPath(t *testing.T) {
	const customPath = "/usr/local/bin/codex"
	t.Setenv("CODEX_CLI_PATH", customPath)
	p := NewCodexCLIProvider()
	if p.cliPath != customPath {
		t.Errorf("cliPath = %q, want %q", p.cliPath, customPath)
	}
}

func TestCodexCLIProvider_Name(t *testing.T) {
	p := NewCodexCLIProvider()
	if p.Name() != "codex-cli" {
		t.Errorf("Name() = %q, want %q", p.Name(), "codex-cli")
	}
}

func TestFilterCodexEnv_RemovesSensitiveKeys(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "secret-anthropic")
	t.Setenv("GEMINI_API_KEY", "secret-gemini")
	t.Setenv("OPENAI_API_KEY", "keep-this")

	env := filterCodexEnv()

	for _, e := range env {
		if e == "ANTHROPIC_API_KEY=secret-anthropic" {
			t.Error("ANTHROPIC_API_KEY should be filtered out")
		}
		if e == "GEMINI_API_KEY=secret-gemini" {
			t.Error("GEMINI_API_KEY should be filtered out")
		}
	}

	foundOpenAI := false
	foundNoColor := false
	for _, e := range env {
		if e == "OPENAI_API_KEY=keep-this" {
			foundOpenAI = true
		}
		if e == "NO_COLOR=1" {
			foundNoColor = true
		}
	}
	if !foundOpenAI {
		t.Error("OPENAI_API_KEY should be preserved")
	}
	if !foundNoColor {
		t.Error("NO_COLOR=1 should be added")
	}
}

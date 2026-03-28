package llm

import (
	"strconv"
	"strings"
	"testing"
)

func TestResolveClaudeModel_Aliases(t *testing.T) {
	tests := []struct {
		alias string
		want  string
	}{
		{"opus", "claude-opus-4-6"},
		{"sonnet", "claude-sonnet-4-6"},
		{"haiku", "claude-haiku-4-5-20251001"},
		{"", "claude-sonnet-4-6"}, // empty falls back to sonnet
		{"claude-opus-4-6", "claude-opus-4-6"}, // pass-through
		{"unknown-model", "unknown-model"},       // unknown alias pass-through
	}
	for _, tt := range tests {
		got := resolveClaudeModel(tt.alias)
		if got != tt.want {
			t.Errorf("resolveClaudeModel(%q) = %q, want %q", tt.alias, got, tt.want)
		}
	}
}

func TestNewClaudeCLIProvider_DefaultPath(t *testing.T) {
	t.Setenv("CLAUDE_CLI_PATH", "")
	p := NewClaudeCLIProvider()
	if p.cliPath != "claude" {
		t.Errorf("cliPath = %q, want %q", p.cliPath, "claude")
	}
}

func TestNewClaudeCLIProvider_CustomPath(t *testing.T) {
	const customPath = "/usr/local/bin/claude"
	t.Setenv("CLAUDE_CLI_PATH", customPath)
	p := NewClaudeCLIProvider()
	if p.cliPath != customPath {
		t.Errorf("cliPath = %q, want %q", p.cliPath, customPath)
	}
}

func TestClaudeCLIProvider_Name(t *testing.T) {
	p := NewClaudeCLIProvider()
	if p.Name() != "claude" {
		t.Errorf("Name() = %q, want %q", p.Name(), "claude")
	}
}

// buildCLIArgs mirrors the argument construction in ClaudeCLIProvider.Complete
// without actually executing anything, allowing us to verify arg structure.
func buildCLIArgs(req *Request) (args []string, hasStdin bool) {
	model := resolveClaudeModel(req.Model)

	if req.FullAgent {
		maxTurns := req.MaxTurns
		if maxTurns <= 0 {
			maxTurns = 10
		}
		args = []string{
			"--model", model,
			"--output-format", "text",
			"--dangerously-skip-permissions",
			"--max-turns", strconv.Itoa(maxTurns),
		}
		if req.SystemPrompt != "" {
			args = append(args, "--system-prompt", req.SystemPrompt)
		}
		args = append(args, req.UserMessage)
		return args, false
	}

	args = []string{
		"-p",
		"--model", model,
		"--output-format", "text",
	}
	if req.SystemPrompt != "" {
		args = append(args, "--system-prompt", req.SystemPrompt)
	}
	return args, req.UserMessage != ""
}

func TestBuildCLIArgs_PromptMode(t *testing.T) {
	req := &Request{
		UserMessage: "hello world",
		Model:       "opus",
		FullAgent:   false,
	}
	args, hasStdin := buildCLIArgs(req)

	if args[0] != "-p" {
		t.Errorf("prompt mode: args[0] = %q, want %q", args[0], "-p")
	}
	if !hasStdin {
		t.Error("prompt mode: expected hasStdin=true for non-empty UserMessage")
	}

	// Verify user message is NOT in args for -p mode.
	for _, a := range args {
		if a == req.UserMessage {
			t.Errorf("prompt mode: UserMessage %q should not appear in args, got args=%v", req.UserMessage, args)
		}
	}

	// Model alias resolution.
	if !containsSequence(args, "--model", "claude-opus-4-6") {
		t.Errorf("prompt mode: args missing --model claude-opus-4-6, got %v", args)
	}
}

func TestBuildCLIArgs_FullAgentMode(t *testing.T) {
	req := &Request{
		UserMessage: "analyze this repo",
		Model:       "opus",
		MaxTurns:    10,
		FullAgent:   true,
		WorkDir:     "/tmp/repo",
	}
	args, hasStdin := buildCLIArgs(req)

	if hasStdin {
		t.Error("full agent mode: hasStdin should be false")
	}

	// Must NOT start with -p.
	if args[0] == "-p" {
		t.Errorf("full agent mode: args[0] should not be -p")
	}

	// Must include --dangerously-skip-permissions.
	found := false
	for _, a := range args {
		if a == "--dangerously-skip-permissions" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("full agent mode: missing --dangerously-skip-permissions, got %v", args)
	}

	// UserMessage must be the last positional argument.
	if args[len(args)-1] != req.UserMessage {
		t.Errorf("full agent mode: last arg = %q, want UserMessage %q", args[len(args)-1], req.UserMessage)
	}

	// --max-turns must be present.
	if !containsSequence(args, "--max-turns", "10") {
		t.Errorf("full agent mode: args missing --max-turns 10, got %v", args)
	}
}

func TestBuildCLIArgs_SystemPromptIncluded(t *testing.T) {
	req := &Request{
		UserMessage:  "hello",
		SystemPrompt: "You are a helpful assistant.",
		Model:        "sonnet",
		FullAgent:    false,
	}
	args, _ := buildCLIArgs(req)

	if !containsSequence(args, "--system-prompt", req.SystemPrompt) {
		t.Errorf("expected --system-prompt in args, got %v", args)
	}
}

func TestBuildCLIArgs_NoSystemPromptWhenEmpty(t *testing.T) {
	req := &Request{
		UserMessage: "hello",
		Model:       "haiku",
		FullAgent:   false,
	}
	args, _ := buildCLIArgs(req)

	for _, a := range args {
		if strings.Contains(a, "system-prompt") {
			t.Errorf("unexpected --system-prompt in args when SystemPrompt is empty, got %v", args)
		}
	}
}

func TestBuildCLIArgs_FullAgentDefaultMaxTurns(t *testing.T) {
	req := &Request{
		UserMessage: "go",
		Model:       "sonnet",
		FullAgent:   true,
		MaxTurns:    0, // should default to 10
	}
	args, _ := buildCLIArgs(req)

	if !containsSequence(args, "--max-turns", "10") {
		t.Errorf("full agent default max-turns: expected --max-turns 10, got %v", args)
	}
}

// containsSequence reports whether key immediately followed by val appears in s.
func containsSequence(s []string, key, val string) bool {
	for i := 0; i < len(s)-1; i++ {
		if s[i] == key && s[i+1] == val {
			return true
		}
	}
	return false
}

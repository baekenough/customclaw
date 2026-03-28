package llm

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// ClaudeCLIProvider implements Provider by invoking the claude CLI binary as a
// subprocess. It uses the OAuth token managed by the Claude CLI (separate from
// the Anthropic API key pool), making it suitable as a fallback when the API
// key exhausts its monthly quota.
type ClaudeCLIProvider struct {
	cliPath string
}

// NewClaudeCLIProvider constructs a ClaudeCLIProvider. The CLI binary path is
// read from the CLAUDE_CLI_PATH environment variable; if unset, "claude" is
// used and resolved via PATH at exec time.
func NewClaudeCLIProvider() *ClaudeCLIProvider {
	cliPath := os.Getenv("CLAUDE_CLI_PATH")
	if cliPath == "" {
		cliPath = "claude"
	}
	return &ClaudeCLIProvider{cliPath: cliPath}
}

// Name returns "claude".
func (p *ClaudeCLIProvider) Name() string { return "claude" }

// Complete invokes the claude CLI binary and returns its output as the
// response text. Usage information is not available from the CLI and will
// always be nil in the returned Response.
//
// Two execution modes are supported:
//
//   - Standard (-p) mode: the user message is piped via stdin. Suitable for
//     single-turn prompts.
//   - FullAgent mode: uses --dangerously-skip-permissions for non-interactive
//     execution; the user message is passed as a trailing positional argument.
//
// When req.WorkDir is non-empty, the subprocess working directory is set
// accordingly so that relative tool paths resolve correctly.
func (p *ClaudeCLIProvider) Complete(ctx context.Context, req *Request) (*Response, error) {
	model := resolveClaudeModel(req.Model)

	maxTurns := req.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 10
	}

	var args []string
	var stdinPayload string

	if req.FullAgent {
		// Agent mode: non-interactive execution with tool use enabled.
		// User message is a trailing positional argument, not stdin.
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
	} else {
		// Prompt (-p) mode: single-turn, user message via stdin.
		args = []string{
			"-p",
			"--model", model,
			"--output-format", "text",
		}
		if req.SystemPrompt != "" {
			args = append(args, "--system-prompt", req.SystemPrompt)
		}
		stdinPayload = req.UserMessage
	}

	slog.Info("calling claude cli",
		"model", model,
		"full_agent", req.FullAgent,
		"work_dir", req.WorkDir,
	)

	cmd := exec.CommandContext(ctx, p.cliPath, args...) //nolint:gosec // path is from env or default

	if req.WorkDir != "" {
		cmd.Dir = req.WorkDir
	}

	if !req.FullAgent && stdinPayload != "" {
		cmd.Stdin = strings.NewReader(stdinPayload)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		stderrStr := strings.TrimSpace(stderr.String())
		if stderrStr != "" {
			return nil, fmt.Errorf("claude cli error: %w: %s", err, stderrStr)
		}
		return nil, fmt.Errorf("claude cli error: %w", err)
	}

	text := strings.TrimSpace(stdout.String())
	if text == "" {
		stderrStr := strings.TrimSpace(stderr.String())
		if stderrStr != "" {
			return nil, fmt.Errorf("claude cli returned no output (stderr: %s)", stderrStr)
		}
		return nil, fmt.Errorf("claude cli returned no output")
	}

	// Usage is not reported by the CLI binary; callers must handle nil Usage.
	return &Response{Text: text, Usage: nil}, nil
}

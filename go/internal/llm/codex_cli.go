package llm

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
)

// codexModelAliases maps short friendly names to Codex CLI model IDs.
var codexModelAliases = map[string]string{
	"gpt-5.4":       "gpt-5.4",
	"codex":         "gpt-5.4",
	"codex-mini":    "gpt-5.4-mini",
	"gpt-5.3-codex": "gpt-5.3-codex",
}

// resolveCodexCLIModel returns the Codex CLI model ID for a given alias.
func resolveCodexCLIModel(alias string) string {
	if alias == "" {
		return "gpt-5.4"
	}
	if m, ok := codexModelAliases[alias]; ok {
		return m
	}
	return alias
}

// CodexCLIProvider implements Provider by invoking the codex CLI binary as a
// subprocess. It uses the OAuth token managed by the Codex CLI or OPENAI_API_KEY.
type CodexCLIProvider struct {
	cliPath string
}

// NewCodexCLIProvider constructs a CodexCLIProvider. The CLI binary path is
// read from the CODEX_CLI_PATH environment variable; if unset, "codex" is
// used and resolved via PATH at exec time.
func NewCodexCLIProvider() *CodexCLIProvider {
	cliPath := os.Getenv("CODEX_CLI_PATH")
	if cliPath == "" {
		cliPath = "codex"
	}
	return &CodexCLIProvider{cliPath: cliPath}
}

// Name returns "codex-cli".
func (p *CodexCLIProvider) Name() string { return "codex-cli" }

// Complete invokes the codex CLI binary and returns its output.
// Usage information is not available from the CLI and will always be nil.
//
// Two execution modes:
//   - Standard: `codex exec <prompt>` with sandbox=read-only
//   - FullAgent: `codex exec <prompt> --sandbox=danger-full-access`
func (p *CodexCLIProvider) Complete(ctx context.Context, req *Request) (*Response, error) {
	model := resolveCodexCLIModel(req.Model)

	// Build args: codex exec <prompt> [flags]
	args := []string{
		"exec",
		req.UserMessage,
		"-m", model,
		"--notify", "false",
	}

	if req.FullAgent {
		args = append(args, "--sandbox", "danger-full-access")
	} else {
		args = append(args, "--sandbox", "read-only")
	}

	slog.Info("calling codex cli",
		"model", model,
		"full_agent", req.FullAgent,
		"work_dir", req.WorkDir,
	)

	cmd := exec.CommandContext(ctx, p.cliPath, args...) //nolint:gosec // path is from env or default

	if req.WorkDir != "" {
		cmd.Dir = req.WorkDir
	}

	// Set environment: filter sensitive keys, set NO_COLOR for clean output.
	cmd.Env = filterCodexEnv()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		stderrStr := strings.TrimSpace(stderr.String())
		if stderrStr != "" {
			return nil, fmt.Errorf("codex cli error: %w: %s", err, stderrStr)
		}
		return nil, fmt.Errorf("codex cli error: %w", err)
	}

	text := strings.TrimSpace(stdout.String())
	if text == "" {
		stderrStr := strings.TrimSpace(stderr.String())
		if stderrStr != "" {
			return nil, fmt.Errorf("codex cli returned no output (stderr: %s)", stderrStr)
		}
		return nil, fmt.Errorf("codex cli returned no output")
	}

	return &Response{Text: text, Usage: nil}, nil
}

// filterCodexEnv returns os.Environ() with NO_COLOR=1 added and sensitive
// keys (ANTHROPIC_API_KEY, GEMINI_API_KEY) removed. OPENAI_API_KEY is kept
// as codex needs it for authentication.
func filterCodexEnv() []string {
	remove := map[string]bool{
		"ANTHROPIC_API_KEY": true,
		"GEMINI_API_KEY":    true,
	}
	var env []string
	hasNoColor := false
	for _, e := range os.Environ() {
		key, _, _ := strings.Cut(e, "=")
		if remove[key] {
			continue
		}
		if key == "NO_COLOR" {
			hasNoColor = true
		}
		env = append(env, e)
	}
	if !hasNoColor {
		env = append(env, "NO_COLOR=1")
	}
	return env
}

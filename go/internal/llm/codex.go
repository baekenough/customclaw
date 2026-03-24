package llm

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// codexModelAliases maps short friendly names to Codex model IDs.
// Unknown aliases are passed through unchanged.
var codexModelAliases = map[string]string{
	"gpt-5.4": "gpt-5.4",
	"codex":   "gpt-5.4",
}

// resolveCodexModel returns the canonical Codex model ID for a given alias.
// If the alias is not found in the map and is non-empty, it is returned as-is.
// An empty alias falls back to "gpt-5.4".
func resolveCodexModel(alias string) string {
	if alias == "" {
		return "gpt-5.4"
	}
	if full, ok := codexModelAliases[alias]; ok {
		return full
	}
	return alias
}

// CodexProvider implements Provider by invoking the Codex CLI as a subprocess.
// Codex uses ChatGPT OAuth which is incompatible with the OpenAI API SDK, so
// we shell out to the CLI instead. This mirrors the Python _run_codex_cli
// pattern from the bot_engine.
type CodexProvider struct {
	cliPath string
}

// NewCodexProvider constructs a CodexProvider. The CLI binary path is read
// from the CODEX_CLI_PATH environment variable; it defaults to "codex" (i.e.
// resolved via PATH at execution time).
func NewCodexProvider() *CodexProvider {
	path := os.Getenv("CODEX_CLI_PATH")
	if path == "" {
		path = "codex"
	}
	return &CodexProvider{cliPath: path}
}

// Name returns "codex".
func (p *CodexProvider) Name() string { return "codex" }

// Complete invokes the Codex CLI as a subprocess and returns its output.
//
// The command executed is:
//
//	codex exec <prompt> -m <model> --dangerously-bypass-approvals-and-sandbox
//
// HOME is set from the CONTAINER_HOME environment variable so the Codex CLI
// can locate its OAuth credentials inside the container. NO_COLOR=1 suppresses
// ANSI escape sequences in the output.
//
// If the system prompt is non-empty it is prepended to the user message,
// separated by a double newline. The Codex CLI does not have a separate system
// prompt flag, so both are merged into the single prompt argument.
//
// The raw CLI output is stripped of the metadata header/footer that Codex
// emits before the actual response text.
func (p *CodexProvider) Complete(ctx context.Context, req *Request) (*Response, error) {
	model := resolveCodexModel(req.Model)

	prompt := req.UserMessage
	if req.SystemPrompt != "" {
		prompt = req.SystemPrompt + "\n\n" + prompt
	}

	args := []string{
		"exec", prompt,
		"-m", model,
		"--dangerously-bypass-approvals-and-sandbox",
	}

	cmd := exec.CommandContext(ctx, p.cliPath, args...)

	// Build environment: inherit host env, then override HOME for container
	// credential resolution and suppress colour output.
	env := os.Environ()
	if containerHome := os.Getenv("CONTAINER_HOME"); containerHome != "" {
		env = appendOrReplace(env, "HOME="+containerHome)
	}
	env = appendOrReplace(env, "NO_COLOR=1")
	cmd.Env = env

	if req.WorkDir != "" {
		cmd.Dir = req.WorkDir
	}
	cmd.Stdin = nil // equivalent to subprocess.DEVNULL

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errSnippet := stderr.String()
		if len(errSnippet) > 500 {
			errSnippet = errSnippet[:500]
		}
		return nil, fmt.Errorf("codex cli error (%w): %s", err, errSnippet)
	}

	output := strings.TrimSpace(stdout.String())
	if output == "" {
		return nil, fmt.Errorf("codex cli returned empty output")
	}

	output = stripCodexMetadata(output)

	return &Response{
		Text: output,
		Usage: &UsageInfo{
			Model: model,
		},
	}, nil
}

// stripCodexMetadata removes the metadata header and footer that the Codex
// CLI emits around the actual response. The header includes lines like:
//
//	OpenAI Codex
//	-----------
//	workdir:  /path
//	model:    gpt-5.4
//	provider: openai
//	approval: ...
//	sandbox:  ...
//	reasoning ...
//	session id: ...
//
// The footer lines include bare "codex", "tokens used", and digit-only lines.
// Content lines between the header and footer are preserved verbatim.
func stripCodexMetadata(output string) string {
	lines := strings.Split(output, "\n")
	content := make([]string, 0, len(lines))
	skipHeader := true

	for _, line := range lines {
		if skipHeader {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "OpenAI Codex") ||
				strings.HasPrefix(trimmed, "--------") ||
				strings.HasPrefix(trimmed, "workdir:") ||
				strings.HasPrefix(trimmed, "model:") ||
				strings.HasPrefix(trimmed, "provider:") ||
				strings.HasPrefix(trimmed, "approval:") ||
				strings.HasPrefix(trimmed, "sandbox:") ||
				strings.HasPrefix(trimmed, "reasoning") ||
				strings.HasPrefix(trimmed, "session id:") {
				continue
			}
			skipHeader = false
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "codex" || trimmed == "tokens used" || isAllDigits(trimmed) {
			continue
		}
		content = append(content, line)
	}

	return strings.TrimSpace(strings.Join(content, "\n"))
}

// isAllDigits reports whether s consists entirely of ASCII digit characters.
// An empty string returns false.
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// appendOrReplace appends key=value to env, replacing any existing entry with
// the same key. This avoids duplicate environment variable entries.
func appendOrReplace(env []string, entry string) []string {
	key := strings.SplitN(entry, "=", 2)[0] + "="
	for i, e := range env {
		if strings.HasPrefix(e, key) {
			env[i] = entry
			return env
		}
	}
	return append(env, entry)
}

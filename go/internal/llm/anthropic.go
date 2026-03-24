package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"
)

// claudeModelAliases maps short friendly names to Claude CLI model IDs.
// Unknown aliases are passed through unchanged.
var claudeModelAliases = map[string]string{
	"opus":   "opus",
	"sonnet": "sonnet",
	"haiku":  "haiku",
}

// resolveClaudeModel returns the Claude CLI model name for a given alias.
// If the alias is not found in the map and is non-empty, it is returned as-is.
// An empty alias falls back to "sonnet".
func resolveClaudeModel(alias string) string {
	if alias == "" {
		return "sonnet"
	}
	if m, ok := claudeModelAliases[alias]; ok {
		return m
	}
	return alias
}

// ClaudeProvider implements Provider by calling the Claude CLI binary.
// Authentication is handled by the CLI itself via its own OAuth credentials,
// making the Anthropic REST API SDK unnecessary.
type ClaudeProvider struct {
	cliPath string
}

// NewClaudeProvider constructs a ClaudeProvider. The CLI binary path is read
// from the CLAUDE_CLI_PATH environment variable; it defaults to "claude"
// (resolved via PATH at execution time).
func NewClaudeProvider() *ClaudeProvider {
	path := os.Getenv("CLAUDE_CLI_PATH")
	if path == "" {
		path = "claude"
	}
	return &ClaudeProvider{cliPath: path}
}

// Name returns "claude".
func (p *ClaudeProvider) Name() string { return "claude" }

// Complete invokes the Claude CLI as a subprocess and returns its output.
//
// The command executed is:
//
//	claude -p <prompt> --model <model> --max-turns <n> --output-format json
//
// When FullAgent is true, --dangerously-skip-permissions is added and the
// timeout is extended proportionally to max_turns.
//
// HOME is set from CONTAINER_HOME so the CLI can locate its OAuth credentials
// inside the container. NO_COLOR=1 suppresses ANSI escape sequences.
func (p *ClaudeProvider) Complete(ctx context.Context, req *Request) (*Response, error) {
	model := resolveClaudeModel(req.Model)
	maxTurns := req.MaxTurns
	if maxTurns == 0 {
		maxTurns = 3
	}

	// Build combined prompt: the Claude CLI takes a single -p argument.
	prompt := req.UserMessage
	if req.SystemPrompt != "" {
		prompt = req.SystemPrompt + "\n\n" + req.UserMessage
	}

	args := []string{
		"-p", prompt,
		"--model", model,
		"--max-turns", fmt.Sprintf("%d", maxTurns),
		"--output-format", "json",
	}

	timeout := 180 // default seconds
	if req.FullAgent {
		args = append(args, "--dangerously-skip-permissions")
		timeout = maxTurns * 60
	}

	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(execCtx, p.cliPath, args...)

	// Build environment: inherit host env, override HOME for container
	// credential resolution, suppress colour output.
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

	slog.Info("running claude cli",
		"model", model,
		"max_turns", maxTurns,
		"full_agent", req.FullAgent,
	)

	if err := cmd.Run(); err != nil {
		errMsg := stderr.String()
		if len(errMsg) > 500 {
			errMsg = errMsg[:500]
		}
		return nil, fmt.Errorf("claude cli error (exit %v): %s", err, errMsg)
	}

	output := strings.TrimSpace(stdout.String())
	if output == "" {
		return nil, fmt.Errorf("claude cli returned empty output")
	}

	text, usage := parseClaudeJSONOutput(output)
	if text == "" {
		return nil, fmt.Errorf("claude cli returned no result text")
	}

	return &Response{Text: text, Usage: usage}, nil
}

// parseClaudeJSONOutput parses Claude CLI --output-format json response.
// Returns (text, usage). On parse failure the raw output is returned as plain text.
func parseClaudeJSONOutput(raw string) (string, *UsageInfo) {
	var data map[string]any
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		// Not JSON — return raw output as plain text (e.g. plain-text mode).
		return raw, nil
	}

	result, ok := data["result"].(string)
	if !ok {
		// Detect CLI status/error JSON (type=result but no result text).
		if dataType, _ := data["type"].(string); dataType == "result" {
			subtype, _ := data["subtype"].(string)
			if subtype == "error_max_turns" {
				slog.Warn("claude cli hit max_turns limit")
			}
			return "", extractUsageFromCLIJSON(data)
		}
		return raw, nil
	}

	usage := extractUsageFromCLIJSON(data)
	return result, usage
}

// extractUsageFromCLIJSON extracts token usage and cost from a parsed Claude
// CLI JSON response. Returns a non-nil UsageInfo even when no usage fields are present.
func extractUsageFromCLIJSON(data map[string]any) *UsageInfo {
	info := &UsageInfo{}
	if u, ok := data["usage"].(map[string]any); ok {
		if v, ok := u["input_tokens"].(float64); ok {
			info.InputTokens = int(v)
		}
		if v, ok := u["output_tokens"].(float64); ok {
			info.OutputTokens = int(v)
		}
		if v, ok := u["cache_read_input_tokens"].(float64); ok {
			info.CacheReadTokens = int(v)
		}
		if v, ok := u["cache_creation_input_tokens"].(float64); ok {
			info.CacheCreationTokens = int(v)
		}
	}
	if v, ok := data["total_cost_usd"].(float64); ok {
		info.CostUSD = &v
	}
	// modelUsage maps model ID → usage breakdown; take the first key as Model.
	if mu, ok := data["modelUsage"].(map[string]any); ok {
		for modelID := range mu {
			info.Model = modelID
			break
		}
	}
	return info
}

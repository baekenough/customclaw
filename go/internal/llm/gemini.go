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

// geminiModelAliases maps short friendly names to Gemini CLI model IDs.
// Unknown aliases are passed through unchanged.
var geminiModelAliases = map[string]string{
	"gemini-3-pro":   "gemini-3-pro-preview",
	"gemini-2-flash": "gemini-2.0-flash",
}

// resolveGeminiModel returns the Gemini CLI model name for a given alias.
// If the alias is not found in the map and is non-empty, it is returned as-is.
// An empty alias falls back to "gemini-3-pro-preview".
func resolveGeminiModel(alias string) string {
	if alias == "" {
		return "gemini-3-pro-preview"
	}
	if full, ok := geminiModelAliases[alias]; ok {
		return full
	}
	return alias
}

// GeminiProvider implements Provider by calling the Gemini CLI binary.
// Authentication is handled by the CLI itself, making the Google GenAI SDK
// unnecessary and keeping the provider consistent with the Claude/Codex pattern.
type GeminiProvider struct {
	cliPath string
}

// NewGeminiProvider constructs a GeminiProvider. The CLI binary path is read
// from the GEMINI_CLI_PATH environment variable; it defaults to "gemini"
// (resolved via PATH at execution time).
func NewGeminiProvider() *GeminiProvider {
	path := os.Getenv("GEMINI_CLI_PATH")
	if path == "" {
		path = "gemini"
	}
	return &GeminiProvider{cliPath: path}
}

// Name returns "gemini".
func (p *GeminiProvider) Name() string { return "gemini" }

// Complete invokes the Gemini CLI as a subprocess and returns its output.
//
// The Gemini CLI reads the prompt from stdin. The command executed is:
//
//	gemini --model <model> --yolo
//
// The system prompt (if present) is prepended to the user message,
// separated by a double newline. HOME is set from CONTAINER_HOME so the CLI
// can locate its credentials inside the container. NO_COLOR=1 suppresses
// ANSI escape sequences.
func (p *GeminiProvider) Complete(ctx context.Context, req *Request) (*Response, error) {
	model := resolveGeminiModel(req.Model)

	args := []string{"--model", model, "--yolo"}

	cmd := exec.CommandContext(ctx, p.cliPath, args...)

	// Build combined prompt: system prompt prepended to user message.
	prompt := req.UserMessage
	if req.SystemPrompt != "" {
		prompt = req.SystemPrompt + "\n\n" + req.UserMessage
	}

	// Gemini CLI reads the prompt from stdin.
	cmd.Stdin = strings.NewReader(prompt)

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

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	slog.Info("running gemini cli", "model", model)

	if err := cmd.Run(); err != nil {
		errMsg := stderr.String()
		if len(errMsg) > 500 {
			errMsg = errMsg[:500]
		}
		return nil, fmt.Errorf("gemini cli error (exit %v): %s", err, errMsg)
	}

	output := strings.TrimSpace(stdout.String())
	if output == "" {
		return nil, fmt.Errorf("gemini cli returned empty output")
	}

	return &Response{
		Text: output,
		Usage: &UsageInfo{
			Model: model,
		},
	}, nil
}

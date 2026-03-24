// Package credprobe periodically checks LLM provider credentials and stores
// results in the credential_status PostgreSQL table. It sends Slack alerts on
// ok→error transitions.
package credprobe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultAlertChannel  = "C0AMBNY135Z"
	defaultClaudeCLI     = "claude"
	defaultContainerHome = "/home/appuser"
	claudeTimeout        = 30 * time.Second
	httpTimeout          = 10 * time.Second
	probeInterval        = 30 * time.Minute
)

// stateTracker guards the previous-status map used for alert deduplication.
var stateTracker = struct {
	mu     sync.Mutex
	states map[string]string
}{
	states: make(map[string]string),
}

// checkResult holds the outcome of a single provider check.
type checkResult struct {
	status string // "ok", "error", or "unconfigured"
	errMsg string // non-empty only when status == "error"
}

// ---------------------------------------------------------------------------
// Provider checks
// ---------------------------------------------------------------------------

// checkClaude runs the Claude CLI as a subprocess and inspects its JSON output.
// Returns ("ok", "") on success, ("error", msg) on any failure.
func checkClaude(ctx context.Context) checkResult {
	cliPath := os.Getenv("CLAUDE_CLI_PATH")
	if cliPath == "" {
		cliPath = defaultClaudeCLI
	}
	homeDir := os.Getenv("CONTAINER_HOME")
	if homeDir == "" {
		homeDir = defaultContainerHome
	}

	execCtx, cancel := context.WithTimeout(ctx, claudeTimeout)
	defer cancel()

	args := []string{
		"-p", ".",
		"--model", "haiku",
		"--max-turns", "1",
		"--output-format", "json",
	}
	cmd := exec.CommandContext(execCtx, cliPath, args...)

	// Inherit host environment and override HOME for container credential resolution.
	env := os.Environ()
	env = appendOrReplace(env, "HOME="+homeDir)
	cmd.Env = env

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	output := strings.TrimSpace(stdout.String())
	if output == "" {
		output = strings.TrimSpace(stderr.String())
	}

	if runErr != nil {
		// FileNotFoundError equivalent: exec.ErrNotFound or "executable file not found"
		if strings.Contains(runErr.Error(), "executable file not found") ||
			strings.Contains(runErr.Error(), "no such file") {
			return checkResult{"error", fmt.Sprintf("Claude CLI not found at path: %s", cliPath)}
		}
		// Context deadline exceeded maps to timeout.
		if execCtx.Err() == context.DeadlineExceeded {
			return checkResult{"error", "Claude CLI timed out after 30 seconds"}
		}
		if output != "" {
			return checkResult{"error", output}
		}
		return checkResult{"error", runErr.Error()}
	}

	if output == "" {
		return checkResult{"ok", ""}
	}

	var data map[string]any
	if err := json.Unmarshal([]byte(output), &data); err != nil {
		// Non-JSON output with zero exit — treat as success.
		return checkResult{"ok", ""}
	}

	isError, _ := data["is_error"].(bool)
	if !isError {
		return checkResult{"ok", ""}
	}

	// is_error: true — inspect for authentication failures.
	raw, _ := json.Marshal(data)
	rawStr := string(raw)
	if strings.Contains(rawStr, "authentication_error") || strings.Contains(rawStr, "expired") {
		errVal := data["error"]
		switch v := errVal.(type) {
		case map[string]any:
			if msg, ok := v["message"].(string); ok {
				return checkResult{"error", msg}
			}
		case string:
			return checkResult{"error", v}
		}
		return checkResult{"error", rawStr}
	}

	if errVal, ok := data["error"].(string); ok && errVal != "" {
		return checkResult{"error", errVal}
	}
	return checkResult{"error", rawStr}
}

// checkOpenAI validates the OpenAI API key via a lightweight GET to /v1/models.
// Returns ("unconfigured", "") when OPENAI_API_KEY is absent.
func checkOpenAI(ctx context.Context) checkResult {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return checkResult{"unconfigured", ""}
	}

	reqCtx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, "https://api.openai.com/v1/models", nil)
	if err != nil {
		return checkResult{"error", err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if reqCtx.Err() == context.DeadlineExceeded {
			return checkResult{"error", "OpenAI API timed out after 10 seconds"}
		}
		return checkResult{"error", err.Error()}
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return checkResult{"ok", ""}
	case http.StatusUnauthorized:
		msg := extractErrorMessage(resp.Body)
		if msg == "" {
			msg = "invalid or expired API key"
		}
		return checkResult{"error", msg}
	default:
		body := readBodyTruncated(resp.Body, 200)
		return checkResult{"error", fmt.Sprintf("unexpected status %d: %s", resp.StatusCode, body)}
	}
}

// checkGemini validates the Gemini API key via a GET to /v1beta/models.
// Returns ("unconfigured", "") when GEMINI_API_KEY is absent.
func checkGemini(ctx context.Context) checkResult {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return checkResult{"unconfigured", ""}
	}

	url := "https://generativelanguage.googleapis.com/v1beta/models?key=" + apiKey

	reqCtx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return checkResult{"error", err.Error()}
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if reqCtx.Err() == context.DeadlineExceeded {
			return checkResult{"error", "Gemini API timed out after 10 seconds"}
		}
		return checkResult{"error", err.Error()}
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return checkResult{"ok", ""}
	case http.StatusBadRequest, http.StatusForbidden:
		msg := extractErrorMessage(resp.Body)
		if msg == "" {
			msg = fmt.Sprintf("API returned %d", resp.StatusCode)
		}
		return checkResult{"error", msg}
	default:
		body := readBodyTruncated(resp.Body, 200)
		return checkResult{"error", fmt.Sprintf("unexpected status %d: %s", resp.StatusCode, body)}
	}
}

// ---------------------------------------------------------------------------
// Database storage
// ---------------------------------------------------------------------------

// saveStatus upserts a provider's credential status into the credential_status table.
func saveStatus(ctx context.Context, pool *pgxpool.Pool, provider, status, errMsg string) {
	const q = `
		INSERT INTO credential_status (provider, status, error, checked_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (provider) DO UPDATE
			SET status     = EXCLUDED.status,
			    error      = EXCLUDED.error,
			    checked_at = EXCLUDED.checked_at`

	var errArg *string
	if errMsg != "" {
		errArg = &errMsg
	}

	if _, err := pool.Exec(ctx, q, provider, status, errArg); err != nil {
		slog.Error("credential probe: failed to save status",
			"provider", provider,
			"status", status,
			"error", err,
		)
	}
}

// ---------------------------------------------------------------------------
// Slack alerting
// ---------------------------------------------------------------------------

// maybeAlert sends a Slack alert when a provider transitions into an error state.
// It fires on the first "error" result (no prior state) or on a non-error→error transition.
func maybeAlert(provider, status, errMsg string) {
	stateTracker.mu.Lock()
	previous := stateTracker.states[provider]
	stateTracker.states[provider] = status
	stateTracker.mu.Unlock()

	if status == "error" && previous != "error" {
		sendSlackAlert(provider, errMsg)
	}
}

// sendSlackAlert posts an alert message to the configured Slack channel.
func sendSlackAlert(provider, errMsg string) {
	token := os.Getenv("CUSTOMCLAW_SLACK_BOT_TOKEN")
	if token == "" {
		slog.Warn("credential probe: CUSTOMCLAW_SLACK_BOT_TOKEN not set; skipping Slack alert")
		return
	}
	channel := os.Getenv("CREDENTIAL_ALERT_CHANNEL")
	if channel == "" {
		channel = defaultAlertChannel
	}

	text := fmt.Sprintf(
		"\u26a0\ufe0f *LLM Credential Alert*\n%s 인증 실패: %s\n서버에서 credential 갱신이 필요합니다.",
		provider, errMsg,
	)

	payload, err := json.Marshal(map[string]string{
		"channel": channel,
		"text":    text,
	})
	if err != nil {
		slog.Error("credential probe: failed to marshal Slack payload", "error", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), httpTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://slack.com/api/chat.postMessage",
		bytes.NewReader(payload))
	if err != nil {
		slog.Error("credential probe: failed to create Slack request", "error", err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Error("credential probe: Slack API request failed", "error", err)
		return
	}
	defer resp.Body.Close()

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		slog.Error("credential probe: failed to parse Slack response", "error", err)
		return
	}
	if ok, _ := result["ok"].(bool); !ok {
		slackErr, _ := result["error"].(string)
		slog.Error("credential probe: Slack alert failed", "slack_error", slackErr)
	}
}

// ---------------------------------------------------------------------------
// Main probe loop
// ---------------------------------------------------------------------------

type provider struct {
	name  string
	check func(ctx context.Context) checkResult
}

var providers = []provider{
	{"claude", checkClaude},
	{"openai", checkOpenAI},
	{"gemini", checkGemini},
}

// runProbe executes one full round of credential checks across all providers.
func runProbe(ctx context.Context, pool *pgxpool.Pool) {
	for _, p := range providers {
		result := p.check(ctx)
		if result.errMsg != "" {
			slog.Info("credential probe",
				"provider", p.name,
				"status", result.status,
				"error", result.errMsg,
			)
		} else {
			slog.Info("credential probe",
				"provider", p.name,
				"status", result.status,
			)
		}
		saveStatus(ctx, pool, p.name, result.status, result.errMsg)
		maybeAlert(p.name, result.status, result.errMsg)
	}
}

// Start launches the credential probe as a background goroutine. It runs one
// immediate check then repeats every 30 minutes until ctx is cancelled.
//
// The provided pool must remain valid for the lifetime of the background loop.
func Start(ctx context.Context, pool *pgxpool.Pool) {
	go func() {
		slog.Info("credential probe starting", "interval", "30m")
		ticker := time.NewTicker(probeInterval)
		defer ticker.Stop()

		runProbe(ctx, pool) // immediate first run
		for {
			select {
			case <-ctx.Done():
				slog.Info("credential probe stopping")
				return
			case <-ticker.C:
				runProbe(ctx, pool)
			}
		}
	}()
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// appendOrReplace adds key=value to env, replacing an existing entry for key.
func appendOrReplace(env []string, kv string) []string {
	key := kv[:strings.IndexByte(kv, '=')]
	for i, e := range env {
		if strings.HasPrefix(e, key+"=") {
			env[i] = kv
			return env
		}
	}
	return append(env, kv)
}

// extractErrorMessage reads a JSON response body and returns the error message
// at .error.message, falling back to the raw body text on failure.
func extractErrorMessage(r io.Reader) string {
	body, err := io.ReadAll(io.LimitReader(r, 4096))
	if err != nil || len(body) == 0 {
		return ""
	}
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return strings.TrimSpace(string(body))
	}
	if errObj, ok := data["error"].(map[string]any); ok {
		if msg, ok := errObj["message"].(string); ok {
			return msg
		}
	}
	return strings.TrimSpace(string(body))
}

// readBodyTruncated reads up to maxBytes from r and returns it as a string.
func readBodyTruncated(r io.Reader, maxBytes int64) string {
	b, _ := io.ReadAll(io.LimitReader(r, maxBytes))
	return strings.TrimSpace(string(b))
}

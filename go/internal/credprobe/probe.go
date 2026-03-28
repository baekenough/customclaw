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
	defaultAlertChannel = "C0AMBNY135Z"
	httpTimeout         = 10 * time.Second
	probeInterval       = 30 * time.Minute
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
	status  string // "ok", "error", "degraded", or "unconfigured"
	errMsg  string // non-empty only when status == "error" or "degraded"
	errKind string // "auth", "quota", "transient", "network", or ""
}

// ---------------------------------------------------------------------------
// Provider checks
// ---------------------------------------------------------------------------

// checkClaude validates the Anthropic API key via a lightweight Messages API call.
// Returns ("unconfigured", "") when ANTHROPIC_API_KEY is absent.
func checkClaude(ctx context.Context) checkResult {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return checkResult{"unconfigured", "", ""}
	}

	reqCtx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()

	// Use a minimal Messages API call to verify the key.
	payload := []byte(`{"model":"claude-haiku-4-5-20251001","max_tokens":1,"messages":[{"role":"user","content":"."}]}`)

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return checkResult{"error", err.Error(), "network"}
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if reqCtx.Err() == context.DeadlineExceeded {
			return checkResult{"error", "Anthropic API timed out", "network"}
		}
		return checkResult{"error", err.Error(), "network"}
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		return checkResult{"ok", "", ""}
	case http.StatusUnauthorized, http.StatusForbidden:
		msg := extractErrorMessage(resp.Body)
		if msg == "" {
			msg = "invalid or expired API key"
		}
		return checkResult{"error", msg, "auth"}
	case http.StatusTooManyRequests:
		return checkResult{"degraded", "rate limited", "quota"}
	case 529: // Anthropic overloaded
		return checkResult{"degraded", "service overloaded", "transient"}
	case http.StatusBadRequest:
		body := readBodyTruncated(resp.Body, 200)
		if strings.Contains(body, "usage") || strings.Contains(body, "limit") || strings.Contains(body, "credit") {
			return checkResult{"degraded", fmt.Sprintf("usage limit: %s", body), "quota"}
		}
		return checkResult{"error", fmt.Sprintf("bad request: %s", body), ""}
	default:
		body := readBodyTruncated(resp.Body, 200)
		return checkResult{"error", fmt.Sprintf("unexpected status %d: %s", resp.StatusCode, body), ""}
	}
}

// checkOpenAI validates the OpenAI API key via a lightweight GET to /v1/models.
// Returns ("unconfigured", "") when OPENAI_API_KEY is absent.
func checkOpenAI(ctx context.Context) checkResult {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return checkResult{"unconfigured", "", ""}
	}

	reqCtx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, "https://api.openai.com/v1/models", nil)
	if err != nil {
		return checkResult{"error", err.Error(), "network"}
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if reqCtx.Err() == context.DeadlineExceeded {
			return checkResult{"error", "OpenAI API timed out after 10 seconds", "network"}
		}
		return checkResult{"error", err.Error(), "network"}
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		return checkResult{"ok", "", ""}
	case http.StatusUnauthorized, http.StatusForbidden:
		msg := extractErrorMessage(resp.Body)
		if msg == "" {
			msg = "invalid or expired API key"
		}
		return checkResult{"error", msg, "auth"}
	case http.StatusTooManyRequests:
		return checkResult{"degraded", "rate limited", "quota"}
	case 529:
		return checkResult{"degraded", "service overloaded", "transient"}
	default:
		body := readBodyTruncated(resp.Body, 200)
		return checkResult{"error", fmt.Sprintf("unexpected status %d: %s", resp.StatusCode, body), ""}
	}
}

// checkClaudeCLI validates that the claude CLI binary is present and runnable
// by executing `claude --version` with a 10-second timeout.
// Returns ("unconfigured", "") when neither CLAUDE_CLI_PATH nor a PATH-visible
// "claude" binary is found.
func checkClaudeCLI(ctx context.Context) checkResult {
	cliPath := os.Getenv("CLAUDE_CLI_PATH")
	if cliPath == "" {
		var err error
		cliPath, err = exec.LookPath("claude")
		if err != nil {
			return checkResult{"unconfigured", "", ""}
		}
	}

	reqCtx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()

	cmd := exec.CommandContext(reqCtx, cliPath, "--version") //nolint:gosec
	if err := cmd.Run(); err != nil {
		if reqCtx.Err() == context.DeadlineExceeded {
			return checkResult{"error", "claude CLI timed out", "network"}
		}
		return checkResult{"error", err.Error(), ""}
	}
	return checkResult{"ok", "", ""}
}

// checkGemini validates the Gemini API key via a GET to /v1beta/models.
// Returns ("unconfigured", "") when GEMINI_API_KEY is absent.
func checkGemini(ctx context.Context) checkResult {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return checkResult{"unconfigured", "", ""}
	}

	url := "https://generativelanguage.googleapis.com/v1beta/models?key=" + apiKey

	reqCtx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return checkResult{"error", err.Error(), "network"}
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if reqCtx.Err() == context.DeadlineExceeded {
			return checkResult{"error", "Gemini API timed out after 10 seconds", "network"}
		}
		return checkResult{"error", err.Error(), "network"}
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		return checkResult{"ok", "", ""}
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden:
		msg := extractErrorMessage(resp.Body)
		if msg == "" {
			msg = fmt.Sprintf("API returned %d", resp.StatusCode)
		}
		return checkResult{"error", msg, "auth"}
	case http.StatusTooManyRequests:
		return checkResult{"degraded", "rate limited", "quota"}
	default:
		body := readBodyTruncated(resp.Body, 200)
		return checkResult{"error", fmt.Sprintf("unexpected status %d: %s", resp.StatusCode, body), ""}
	}
}

// ---------------------------------------------------------------------------
// Database storage
// ---------------------------------------------------------------------------

// saveStatus upserts a provider's credential status into the credential_status table.
func saveStatus(ctx context.Context, pool *pgxpool.Pool, provider, status, errMsg, errKind string) {
	const q = `
		INSERT INTO credential_status (provider, status, error, error_kind, checked_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (provider) DO UPDATE
			SET status     = EXCLUDED.status,
			    error      = EXCLUDED.error,
			    error_kind = EXCLUDED.error_kind,
			    checked_at = EXCLUDED.checked_at`

	var errArg, kindArg *string
	if errMsg != "" {
		errArg = &errMsg
	}
	if errKind != "" {
		kindArg = &errKind
	}

	if _, err := pool.Exec(ctx, q, provider, status, errArg, kindArg); err != nil {
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
	defer func() { _ = resp.Body.Close() }()

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
	{"claude-cli", checkClaudeCLI},
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
		saveStatus(ctx, pool, p.name, result.status, result.errMsg, result.errKind)
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

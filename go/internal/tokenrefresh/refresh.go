package tokenrefresh

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const (
	defaultAlertChannel = "C0AMBNY135Z"

	// checkInterval is how often the refresh loop wakes up to check both tokens.
	checkInterval = 10 * time.Minute

	// maxAttempts is the number of refresh attempts before giving up and
	// sending a Slack alert.
	maxAttempts = 3
)

// backoffDurations defines the wait time between consecutive retry attempts.
// Index 0 is the pause before attempt 2, index 1 before attempt 3.
var backoffDurations = [maxAttempts - 1]time.Duration{30 * time.Second, 60 * time.Second}

// refreshFn is the signature shared by refreshClaude and refreshCodex.
type refreshFn func(ctx context.Context) (bool, error)

// providerRefresher pairs a human-readable name with its refresh function.
type providerRefresher struct {
	name    string
	refresh refreshFn
}

// Start launches the token auto-refresh loop as a background goroutine. It
// performs an immediate check on startup and then rechecks every 10 minutes.
// The loop stops cleanly when ctx is cancelled. Any error encountered while
// refreshing a token is logged and triggers a Slack alert after maxAttempts
// consecutive failures, but it never crashes the caller.
func Start(ctx context.Context) {
	go func() {
		slog.Info("token refresh loop starting", "interval", checkInterval)

		refreshers := []providerRefresher{
			{"claude", refreshClaude},
			{"codex", refreshCodex},
		}

		ticker := time.NewTicker(checkInterval)
		defer ticker.Stop()

		runAll(ctx, refreshers) // immediate first run
		for {
			select {
			case <-ctx.Done():
				slog.Info("token refresh loop stopping")
				return
			case <-ticker.C:
				runAll(ctx, refreshers)
			}
		}
	}()
}

// runAll iterates over all providers and attempts a token refresh for each.
func runAll(ctx context.Context, refreshers []providerRefresher) {
	for _, r := range refreshers {
		refreshWithRetry(ctx, r)
	}
}

// refreshWithRetry calls r.refresh up to maxAttempts times with exponential
// back-off on transient errors. It sends a Slack alert if all attempts fail.
func refreshWithRetry(ctx context.Context, r providerRefresher) {
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		refreshed, err := r.refresh(ctx)
		if err == nil {
			if refreshed {
				slog.Info("token refresh succeeded", "provider", r.name, "attempt", attempt)
			}
			return
		}

		lastErr = err

		// Handle rate-limit responses: honour the Retry-After value.
		var rle *rateLimitError
		if isRateLimitError(err, &rle) {
			slog.Warn("token refresh rate-limited; waiting before retry",
				"provider", r.name,
				"retry_after", rle.retryAfter,
			)
			select {
			case <-ctx.Done():
				return
			case <-time.After(rle.retryAfter):
			}
			continue
		}

		slog.Warn("token refresh attempt failed",
			"provider", r.name,
			"attempt", attempt,
			"error", err,
		)

		if attempt < maxAttempts {
			backoff := backoffDurations[attempt-1]
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
		}
	}

	// All attempts exhausted.
	slog.Error("token refresh failed after all attempts; sending alert",
		"provider", r.name,
		"error", lastErr,
	)
	sendRefreshAlert(r.name, lastErr.Error())
}

// sendRefreshAlert posts a Slack alert when token refresh has persistently
// failed for a provider.
func sendRefreshAlert(provider, errMsg string) {
	token := os.Getenv("CUSTOMCLAW_SLACK_BOT_TOKEN")
	if token == "" {
		slog.Warn("token refresh: CUSTOMCLAW_SLACK_BOT_TOKEN not set; skipping Slack alert")
		return
	}
	channel := os.Getenv("CREDENTIAL_ALERT_CHANNEL")
	if channel == "" {
		channel = defaultAlertChannel
	}

	text := fmt.Sprintf(
		"\u26a0\ufe0f *Token Refresh Failed*\n%s OAuth 토큰 자동 갱신 실패: %s\n서버에서 수동 re-authentication이 필요합니다.",
		provider, errMsg,
	)

	payload, err := json.Marshal(map[string]string{
		"channel": channel,
		"text":    text,
	})
	if err != nil {
		slog.Error("token refresh: failed to marshal Slack payload", "error", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://slack.com/api/chat.postMessage", bytes.NewReader(payload))
	if err != nil {
		slog.Error("token refresh: failed to create Slack request", "error", err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Error("token refresh: Slack API request failed", "error", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		slog.Error("token refresh: failed to parse Slack response", "error", err)
		return
	}
	if ok, _ := result["ok"].(bool); !ok {
		slackErr, _ := result["error"].(string)
		slog.Error("token refresh: Slack alert failed", "slack_error", slackErr)
	}
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

// rateLimitError carries a retry-after duration extracted from a 429 response.
type rateLimitError struct {
	retryAfter time.Duration
}

func (e *rateLimitError) Error() string {
	return fmt.Sprintf("rate limited; retry after %s", e.retryAfter)
}

// isRateLimitError reports whether err is a *rateLimitError and writes the
// concrete value into target if so.
func isRateLimitError(err error, target **rateLimitError) bool {
	var rle *rateLimitError
	switch v := err.(type) {
	case *rateLimitError:
		rle = v
	default:
		return false
	}
	if target != nil {
		*target = rle
	}
	return true
}

// retryAfterDuration parses the Retry-After header value (either an integer
// number of seconds or an HTTP-date) and returns the corresponding duration.
// Falls back to defaultDuration on parse failure.
func retryAfterDuration(header string, defaultDuration time.Duration) time.Duration {
	if header == "" {
		return defaultDuration
	}
	// Try integer seconds first.
	if secs, err := strconv.ParseInt(header, 10, 64); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	// Try HTTP-date format.
	if t, err := http.ParseTime(header); err == nil {
		d := time.Until(t)
		if d > 0 {
			return d
		}
	}
	return defaultDuration
}

// atomicWriteFile writes data to a temporary file in the same directory as
// path, then renames it into place. This ensures readers never observe a
// partial write. The supplied perm is applied to the temp file before rename.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".tokenrefresh-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := f.Name()

	// Clean up the temp file on any error path.
	var writeErr error
	defer func() {
		if writeErr != nil {
			_ = os.Remove(tmpName)
		}
	}()

	if _, writeErr = f.Write(data); writeErr != nil {
		_ = f.Close()
		return fmt.Errorf("write temp file: %w", writeErr)
	}
	if writeErr = f.Close(); writeErr != nil {
		return fmt.Errorf("close temp file: %w", writeErr)
	}
	if writeErr = os.Chmod(tmpName, perm); writeErr != nil {
		return fmt.Errorf("chmod temp file: %w", writeErr)
	}
	if writeErr = os.Rename(tmpName, path); writeErr != nil {
		return fmt.Errorf("rename temp file: %w", writeErr)
	}
	return nil
}

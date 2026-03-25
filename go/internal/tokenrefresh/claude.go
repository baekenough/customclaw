package tokenrefresh

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	claudeTokenEndpoint = "https://platform.claude.com/v1/oauth/token"
	claudeClientID      = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"

	// claudeRefreshWindow is how far ahead of expiry we attempt a refresh.
	claudeRefreshWindow = time.Hour
)

// claudeCredPath returns the path to the Claude credentials file, consulting
// env vars in priority order:
//  1. CLAUDE_CREDENTIALS_PATH  — full path to the file
//  2. CLAUDE_CONFIG_DIR/.credentials.json
//  3. CONTAINER_HOME/.claude/.credentials.json
func claudeCredPath() (string, error) {
	if p := os.Getenv("CLAUDE_CREDENTIALS_PATH"); p != "" {
		return p, nil
	}
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, ".credentials.json"), nil
	}
	if home := os.Getenv("CONTAINER_HOME"); home != "" {
		return filepath.Join(home, ".claude", ".credentials.json"), nil
	}
	return "", fmt.Errorf("none of CLAUDE_CREDENTIALS_PATH, CLAUDE_CONFIG_DIR, or CONTAINER_HOME is set")
}

// claudeNeedsRefresh reports whether the token in creds should be refreshed
// now: either it is already expired or it expires within claudeRefreshWindow.
func claudeNeedsRefresh(creds *claudeCredentials) bool {
	expiresAt := time.UnixMilli(creds.ClaudeAiOauth.ExpiresAt)
	return time.Until(expiresAt) < claudeRefreshWindow
}

// refreshClaude loads the Claude credentials file, checks whether a refresh
// is needed, and if so POSTs the refresh_token grant to the token endpoint.
// The updated credentials are written back atomically. Returns true when a
// refresh was performed, false when the token is still valid.
func refreshClaude(ctx context.Context) (bool, error) {
	credPath, err := claudeCredPath()
	if err != nil {
		return false, fmt.Errorf("claude cred path: %w", err)
	}

	creds, perm, err := readClaudeCreds(credPath)
	if err != nil {
		return false, fmt.Errorf("read claude creds: %w", err)
	}

	if !claudeNeedsRefresh(creds) {
		slog.Debug("claude token still valid; no refresh needed",
			"expires_at", time.UnixMilli(creds.ClaudeAiOauth.ExpiresAt).Format(time.RFC3339),
		)
		return false, nil
	}

	slog.Info("claude token expiring soon or expired; refreshing",
		"expires_at", time.UnixMilli(creds.ClaudeAiOauth.ExpiresAt).Format(time.RFC3339),
	)

	resp, err := doClaudeRefresh(ctx, creds.ClaudeAiOauth.RefreshToken)
	if err != nil {
		return false, fmt.Errorf("claude token refresh request: %w", err)
	}

	// Update token fields. Only overwrite refresh_token if the response
	// included one; otherwise keep the existing token.
	creds.ClaudeAiOauth.AccessToken = resp.AccessToken
	if resp.RefreshToken != "" {
		creds.ClaudeAiOauth.RefreshToken = resp.RefreshToken
	}
	if resp.ExpiresIn > 0 {
		creds.ClaudeAiOauth.ExpiresAt = time.Now().UnixMilli() + resp.ExpiresIn*1000
	}

	if err := writeClaudeCreds(credPath, creds, perm); err != nil {
		return false, fmt.Errorf("write claude creds: %w", err)
	}

	slog.Info("claude token refreshed successfully",
		"new_expires_at", time.UnixMilli(creds.ClaudeAiOauth.ExpiresAt).Format(time.RFC3339),
	)
	return true, nil
}

// doClaudeRefresh posts a refresh_token grant to the Claude OAuth token
// endpoint and returns the parsed response.
func doClaudeRefresh(ctx context.Context, refreshToken string) (*claudeTokenResponse, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {claudeClientID},
	}

	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, claudeTokenEndpoint,
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		wait := retryAfterDuration(resp.Header.Get("Retry-After"), 60*time.Second)
		return nil, &rateLimitError{retryAfter: wait}
	}

	if resp.StatusCode != http.StatusOK {
		snippet := string(body)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return nil, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, snippet)
	}

	var tokenResp claudeTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse token response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("token response missing access_token")
	}
	return &tokenResp, nil
}

// readClaudeCreds reads and parses the Claude credentials file. Returns the
// parsed credentials and the original file permissions so they can be
// preserved on write-back.
func readClaudeCreds(path string) (*claudeCredentials, os.FileMode, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, err
	}
	perm := info.Mode().Perm()

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}

	var creds claudeCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, 0, fmt.Errorf("unmarshal credentials: %w", err)
	}
	return &creds, perm, nil
}

// writeClaudeCreds serialises creds to JSON and atomically replaces the file
// at path, preserving the supplied file permissions.
func writeClaudeCreds(path string, creds *claudeCredentials, perm os.FileMode) error {
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal credentials: %w", err)
	}
	return atomicWriteFile(path, data, perm)
}

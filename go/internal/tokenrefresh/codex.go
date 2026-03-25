package tokenrefresh

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	codexTokenEndpoint = "https://auth0.openai.com/oauth/token"
	codexClientID      = "app_EMoamEEZ73f0CkXaXp7hrann"

	// codexRefreshWindow is how far ahead of expiry we attempt a refresh.
	codexRefreshWindow = 24 * time.Hour
)

// codexCredPath returns the path to the Codex credentials file, consulting
// env vars in priority order:
//  1. CODEX_CONFIG_DIR/auth.json
//  2. CONTAINER_HOME/.codex/auth.json
func codexCredPath() (string, error) {
	if dir := os.Getenv("CODEX_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "auth.json"), nil
	}
	if home := os.Getenv("CONTAINER_HOME"); home != "" {
		return filepath.Join(home, ".codex", "auth.json"), nil
	}
	return "", fmt.Errorf("none of CODEX_CONFIG_DIR or CONTAINER_HOME is set")
}

// jwtExpiry decodes the payload of a JWT (without signature verification) and
// returns the value of the "exp" claim in seconds since epoch. Returns 0 and
// a non-nil error when the token is malformed or the claim is absent.
func jwtExpiry(token string) (int64, error) {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 {
		return 0, fmt.Errorf("malformed JWT: expected 3 parts, got %d", len(parts))
	}

	// JWT payload uses base64url encoding without padding.
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0, fmt.Errorf("decode JWT payload: %w", err)
	}

	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return 0, fmt.Errorf("parse JWT claims: %w", err)
	}
	if claims.Exp == 0 {
		return 0, fmt.Errorf("JWT missing exp claim")
	}
	return claims.Exp, nil
}

// codexNeedsRefresh reports whether the access token in creds should be
// refreshed: either parsing fails (treat as expired) or the token expires
// within codexRefreshWindow.
func codexNeedsRefresh(creds *codexCredentials) bool {
	exp, err := jwtExpiry(creds.Tokens.AccessToken)
	if err != nil {
		slog.Warn("codex: could not parse access token expiry; assuming refresh needed", "error", err)
		return true
	}
	expiresAt := time.Unix(exp, 0)
	return time.Until(expiresAt) < codexRefreshWindow
}

// refreshCodex loads the Codex auth file, checks whether a refresh is needed,
// and if so POSTs the refresh_token grant. The updated credentials are written
// back atomically. Returns true when a refresh was performed.
func refreshCodex(ctx context.Context) (bool, error) {
	credPath, err := codexCredPath()
	if err != nil {
		return false, fmt.Errorf("codex cred path: %w", err)
	}

	creds, perm, err := readCodexCreds(credPath)
	if err != nil {
		return false, fmt.Errorf("read codex creds: %w", err)
	}

	if !codexNeedsRefresh(creds) {
		exp, _ := jwtExpiry(creds.Tokens.AccessToken)
		slog.Debug("codex token still valid; no refresh needed",
			"expires_at", time.Unix(exp, 0).Format(time.RFC3339),
		)
		return false, nil
	}

	exp, _ := jwtExpiry(creds.Tokens.AccessToken)
	slog.Info("codex token expiring soon or expired; refreshing",
		"expires_at", time.Unix(exp, 0).Format(time.RFC3339),
	)

	resp, err := doCodexRefresh(ctx, creds.Tokens.RefreshToken)
	if err != nil {
		return false, fmt.Errorf("codex token refresh request: %w", err)
	}

	// Update only the fields present in the response.
	if resp.AccessToken != "" {
		creds.Tokens.AccessToken = resp.AccessToken
	}
	if resp.RefreshToken != "" {
		creds.Tokens.RefreshToken = resp.RefreshToken
	}
	if resp.IDToken != "" {
		creds.Tokens.IDToken = resp.IDToken
	}
	creds.LastRefresh = time.Now().UTC().Format(time.RFC3339Nano)

	if err := writeCodexCreds(credPath, creds, perm); err != nil {
		return false, fmt.Errorf("write codex creds: %w", err)
	}

	slog.Info("codex token refreshed successfully")
	return true, nil
}

// doCodexRefresh posts a refresh_token grant to the Auth0 token endpoint and
// returns the parsed response.
func doCodexRefresh(ctx context.Context, refreshToken string) (*codexTokenResponse, error) {
	payload, err := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     codexClientID,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, codexTokenEndpoint,
		bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

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

	var tokenResp codexTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse token response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("token response missing access_token")
	}
	return &tokenResp, nil
}

// readCodexCreds reads and parses the Codex auth file. Returns the parsed
// credentials and the original file permissions.
func readCodexCreds(path string) (*codexCredentials, os.FileMode, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, err
	}
	perm := info.Mode().Perm()

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}

	var creds codexCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, 0, fmt.Errorf("unmarshal credentials: %w", err)
	}
	return &creds, perm, nil
}

// writeCodexCreds serialises creds to JSON and atomically replaces the file
// at path, preserving the supplied file permissions.
func writeCodexCreds(path string, creds *codexCredentials, perm os.FileMode) error {
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal credentials: %w", err)
	}
	return atomicWriteFile(path, data, perm)
}

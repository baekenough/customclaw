package llm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeCredsFile writes a credentialsFile to dir/.credentials.json and
// returns the full path.
func writeCredsFile(t *testing.T, dir string, c credentialsFile) string {
	t.Helper()
	path := filepath.Join(dir, ".credentials.json")
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		t.Fatalf("marshal creds: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write creds file: %v", err)
	}
	return path
}

func validCreds(expiresAt time.Time) credentialsFile {
	return credentialsFile{
		ClaudeAiOauth: oauthCredentials{
			AccessToken:      "sk-ant-oat01-test-access",
			RefreshToken:     "sk-ant-ort01-test-refresh",
			ExpiresAt:        expiresAt.UnixMilli(),
			Scopes:           []string{"user:inference"},
			SubscriptionType: "max",
			RateLimitTier:    "default_claude_max_20x",
		},
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	future := time.Now().Add(time.Hour)
	path := writeCredsFile(t, dir, validCreds(future))

	ts := &OAuthTokenSource{credPath: path}
	if err := ts.loadFromFile(); err != nil {
		t.Fatalf("loadFromFile: %v", err)
	}

	ts.mu.RLock()
	defer ts.mu.RUnlock()

	if ts.accessToken != "sk-ant-oat01-test-access" {
		t.Errorf("accessToken = %q, want sk-ant-oat01-test-access", ts.accessToken)
	}
	if ts.refreshToken != "sk-ant-ort01-test-refresh" {
		t.Errorf("refreshToken = %q, want sk-ant-ort01-test-refresh", ts.refreshToken)
	}
	if ts.subType != "max" {
		t.Errorf("subscriptionType = %q, want max", ts.subType)
	}
	if !ts.expiresAt.Equal(time.UnixMilli(future.UnixMilli())) {
		t.Errorf("expiresAt = %v, want %v", ts.expiresAt, time.UnixMilli(future.UnixMilli()))
	}
}

func TestNewOAuthTokenSource_MissingFile(t *testing.T) {
	_, err := NewOAuthTokenSource("/nonexistent/path/.credentials.json")
	if err == nil {
		t.Fatal("expected error for missing credentials file, got nil")
	}
}

func TestNewOAuthTokenSource_EmptyAccessToken(t *testing.T) {
	dir := t.TempDir()
	creds := credentialsFile{
		ClaudeAiOauth: oauthCredentials{
			AccessToken: "", // deliberately empty
		},
	}
	path := writeCredsFile(t, dir, creds)

	_, err := NewOAuthTokenSource(path)
	if err == nil {
		t.Fatal("expected error for empty accessToken, got nil")
	}
}

func TestToken_NotExpired(t *testing.T) {
	dir := t.TempDir()
	// expires well in the future, beyond the 5-min buffer
	path := writeCredsFile(t, dir, validCreds(time.Now().Add(time.Hour)))

	ts, err := NewOAuthTokenSource(path)
	if err != nil {
		t.Fatalf("NewOAuthTokenSource: %v", err)
	}

	tok, err := ts.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok != "sk-ant-oat01-test-access" {
		t.Errorf("Token = %q, want sk-ant-oat01-test-access", tok)
	}
}

func TestToken_ExpiredToken(t *testing.T) {
	dir := t.TempDir()
	// set expiry to the past so refresh is required
	path := writeCredsFile(t, dir, validCreds(time.Now().Add(-time.Hour)))

	ts, err := NewOAuthTokenSource(path)
	if err != nil {
		t.Fatalf("NewOAuthTokenSource: %v", err)
	}

	// refresh() will fail (no network), but Token() must still return the
	// current token (graceful degradation) and surface an error.
	tok, err := ts.Token()
	if err == nil {
		t.Error("expected refresh error for expired token, got nil")
	}
	// The stale token should still be returned for the caller to attempt.
	if tok == "" {
		t.Error("expected non-empty token even after failed refresh")
	}
}

func TestToken_ExpiryBuffer(t *testing.T) {
	dir := t.TempDir()
	// Token expires in 3 minutes — within the 5-minute buffer.
	path := writeCredsFile(t, dir, validCreds(time.Now().Add(3*time.Minute)))

	ts, err := NewOAuthTokenSource(path)
	if err != nil {
		t.Fatalf("NewOAuthTokenSource: %v", err)
	}

	// Should attempt refresh (and fail with network error — that's expected).
	_, err = ts.Token()
	if err == nil {
		t.Error("expected refresh attempt error for token within 5-min buffer")
	}
}

func TestCredentialFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	original := validCreds(time.Now().Add(time.Hour))
	path := writeCredsFile(t, dir, original)

	ts, err := NewOAuthTokenSource(path)
	if err != nil {
		t.Fatalf("NewOAuthTokenSource: %v", err)
	}

	// Simulate a token rotation in memory.
	newAccess := "sk-ant-oat01-rotated-access"
	newRefresh := "sk-ant-ort01-rotated-refresh"
	newExpiry := time.Now().Add(2 * time.Hour)

	ts.mu.Lock()
	ts.accessToken = newAccess
	ts.refreshToken = newRefresh
	ts.expiresAt = newExpiry
	ts.mu.Unlock()

	if err := ts.saveToFile(); err != nil {
		t.Fatalf("saveToFile: %v", err)
	}

	// Reload from disk and verify the persisted values.
	ts2 := &OAuthTokenSource{credPath: path}
	if err := ts2.loadFromFile(); err != nil {
		t.Fatalf("reload loadFromFile: %v", err)
	}

	ts2.mu.RLock()
	defer ts2.mu.RUnlock()

	if ts2.accessToken != newAccess {
		t.Errorf("reloaded accessToken = %q, want %q", ts2.accessToken, newAccess)
	}
	if ts2.refreshToken != newRefresh {
		t.Errorf("reloaded refreshToken = %q, want %q", ts2.refreshToken, newRefresh)
	}
	// Compare at millisecond granularity (json round-trip loses sub-ms).
	if ts2.expiresAt.UnixMilli() != newExpiry.UnixMilli() {
		t.Errorf("reloaded expiresAt = %v, want %v", ts2.expiresAt, newExpiry)
	}
	// Non-token fields should be preserved from the original file.
	if ts2.subType != "max" {
		t.Errorf("reloaded subscriptionType = %q, want max", ts2.subType)
	}
}

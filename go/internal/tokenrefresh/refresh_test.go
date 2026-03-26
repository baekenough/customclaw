package tokenrefresh

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// jwtExpiry
// ---------------------------------------------------------------------------

func makeJWT(exp int64) string {
	// Minimal JWT: header.payload.signature (signature is ignored)
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]int64{"exp": exp})
	payload := base64.RawURLEncoding.EncodeToString(claims)
	return header + "." + payload + ".sig"
}

func makeJWTPadded(exp int64) string {
	header := base64.URLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]int64{"exp": exp})
	payload := base64.URLEncoding.EncodeToString(claims)
	return header + "." + payload + ".sig"
}

func TestJWTExpiry_Valid(t *testing.T) {
	want := int64(9999999999)
	token := makeJWT(want)
	got, err := jwtExpiry(token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("got exp %d, want %d", got, want)
	}
}

func TestJWTExpiry_Malformed(t *testing.T) {
	_, err := jwtExpiry("notavalidjwt")
	if err == nil {
		t.Error("expected error for malformed JWT")
	}
}

func TestJWTExpiry_MissingExp(t *testing.T) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"user123"}`))
	token := header + "." + payload + ".sig"
	_, err := jwtExpiry(token)
	if err == nil {
		t.Error("expected error when exp claim is absent")
	}
}

func TestJWTExpiry_PaddedBase64(t *testing.T) {
	want := int64(2233445566)
	token := makeJWTPadded(want)
	got, err := jwtExpiry(token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("got exp %d, want %d", got, want)
	}
}

// ---------------------------------------------------------------------------
// retryAfterDuration
// ---------------------------------------------------------------------------

func TestRetryAfterDuration_IntegerSeconds(t *testing.T) {
	got := retryAfterDuration("42", time.Minute)
	want := 42 * time.Second
	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestRetryAfterDuration_Empty(t *testing.T) {
	got := retryAfterDuration("", 30*time.Second)
	if got != 30*time.Second {
		t.Errorf("got %v, want %v", got, 30*time.Second)
	}
}

func TestRetryAfterDuration_InvalidFallback(t *testing.T) {
	got := retryAfterDuration("not-a-date-or-number", 45*time.Second)
	if got != 45*time.Second {
		t.Errorf("got %v, want %v", got, 45*time.Second)
	}
}

// ---------------------------------------------------------------------------
// claudeNeedsRefresh
// ---------------------------------------------------------------------------

func TestClaudeNeedsRefresh_Expired(t *testing.T) {
	creds := &claudeCredentials{
		ClaudeAiOauth: claudeOAuthToken{
			ExpiresAt: time.Now().Add(-time.Hour).UnixMilli(),
		},
	}
	if !claudeNeedsRefresh(creds) {
		t.Error("expected needs refresh for expired token")
	}
}

func TestClaudeNeedsRefresh_ExpiringWithinWindow(t *testing.T) {
	creds := &claudeCredentials{
		ClaudeAiOauth: claudeOAuthToken{
			ExpiresAt: time.Now().Add(30 * time.Minute).UnixMilli(), // inside 1-hour window
		},
	}
	if !claudeNeedsRefresh(creds) {
		t.Error("expected needs refresh for token expiring within window")
	}
}

func TestClaudeNeedsRefresh_Valid(t *testing.T) {
	creds := &claudeCredentials{
		ClaudeAiOauth: claudeOAuthToken{
			ExpiresAt: time.Now().Add(8 * time.Hour).UnixMilli(),
		},
	}
	if claudeNeedsRefresh(creds) {
		t.Error("expected no refresh needed for long-lived token")
	}
}

// ---------------------------------------------------------------------------
// codexNeedsRefresh
// ---------------------------------------------------------------------------

func TestCodexNeedsRefresh_Expired(t *testing.T) {
	exp := time.Now().Add(-time.Hour).Unix()
	creds := &codexCredentials{
		Tokens: codexTokens{AccessToken: makeJWT(exp)},
	}
	if !codexNeedsRefresh(creds) {
		t.Error("expected needs refresh for expired token")
	}
}

func TestCodexNeedsRefresh_Valid(t *testing.T) {
	exp := time.Now().Add(48 * time.Hour).Unix()
	creds := &codexCredentials{
		Tokens: codexTokens{AccessToken: makeJWT(exp)},
	}
	if codexNeedsRefresh(creds) {
		t.Error("expected no refresh needed for long-lived token")
	}
}

func TestCodexNeedsRefresh_MalformedToken(t *testing.T) {
	creds := &codexCredentials{
		Tokens: codexTokens{AccessToken: "garbage"},
	}
	// Malformed token should be treated as needing refresh.
	if !codexNeedsRefresh(creds) {
		t.Error("expected needs refresh for malformed token")
	}
}

func TestCodexNeedsRefresh_IDTokenExpired(t *testing.T) {
	accessExp := time.Now().Add(48 * time.Hour).Unix()
	idExp := time.Now().Add(-5 * time.Minute).Unix()
	creds := &codexCredentials{
		Tokens: codexTokens{
			AccessToken: makeJWT(accessExp),
			IDToken:     makeJWT(idExp),
		},
	}
	if !codexNeedsRefresh(creds) {
		t.Error("expected needs refresh when id token is already expired")
	}
}

func TestCodexNeedsRefresh_IDTokenValid(t *testing.T) {
	accessExp := time.Now().Add(48 * time.Hour).Unix()
	idExp := time.Now().Add(48 * time.Hour).Unix()
	creds := &codexCredentials{
		Tokens: codexTokens{
			AccessToken: makeJWT(accessExp),
			IDToken:     makeJWT(idExp),
		},
	}
	if codexNeedsRefresh(creds) {
		t.Error("expected no refresh needed when both access and id token are valid")
	}
}

// ---------------------------------------------------------------------------
// atomicWriteFile
// ---------------------------------------------------------------------------

func TestAtomicWriteFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.json")
	data := []byte(`{"key":"value"}`)

	if err := atomicWriteFile(path, data, 0o600); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("got %q, want %q", got, data)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("got perm %o, want %o", got, 0o600)
	}
}

// ---------------------------------------------------------------------------
// claudeCredentials round-trip (preserves unknown fields)
// ---------------------------------------------------------------------------

func TestClaudeCredentialsRoundTrip(t *testing.T) {
	raw := `{
		"claudeAiOauth": {
			"accessToken": "sk-ant-oat01-abc",
			"refreshToken": "sk-ant-ort01-xyz",
			"expiresAt": 9999999999000,
			"scopes": ["user:inference"],
			"subscriptionType": "max",
			"rateLimitTier": "default"
		},
		"mcpOAuth": {"someField": "someValue"}
	}`

	var creds claudeCredentials
	if err := json.Unmarshal([]byte(raw), &creds); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if creds.ClaudeAiOauth.AccessToken != "sk-ant-oat01-abc" {
		t.Errorf("accessToken mismatch")
	}

	// Re-marshal and verify mcpOAuth is preserved.
	out, err := json.Marshal(&creds)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var check map[string]json.RawMessage
	if err := json.Unmarshal(out, &check); err != nil {
		t.Fatalf("check unmarshal: %v", err)
	}
	if _, ok := check["mcpOAuth"]; !ok {
		t.Error("mcpOAuth field was dropped during round-trip")
	}
	if _, ok := check["claudeAiOauth"]; !ok {
		t.Error("claudeAiOauth field is missing after round-trip")
	}
}

// ---------------------------------------------------------------------------
// codexCredentials round-trip (preserves unknown fields)
// ---------------------------------------------------------------------------

func TestCodexCredentialsRoundTrip(t *testing.T) {
	raw := `{
		"auth_mode": "chatgpt",
		"OPENAI_API_KEY": null,
		"tokens": {
			"id_token": "eyJid",
			"access_token": "eyJac",
			"refresh_token": "rt_abc",
			"account_id": "acct_123"
		},
		"last_refresh": "2026-03-23T09:30:44.197895919Z",
		"some_future_field": "preserved"
	}`

	var creds codexCredentials
	if err := json.Unmarshal([]byte(raw), &creds); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if creds.Tokens.RefreshToken != "rt_abc" {
		t.Errorf("refresh_token mismatch")
	}

	out, err := json.Marshal(&creds)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var check map[string]json.RawMessage
	if err := json.Unmarshal(out, &check); err != nil {
		t.Fatalf("check unmarshal: %v", err)
	}
	if _, ok := check["some_future_field"]; !ok {
		t.Error("some_future_field was dropped during round-trip")
	}
	if _, ok := check["tokens"]; !ok {
		t.Error("tokens field is missing after round-trip")
	}
}

// ---------------------------------------------------------------------------
// readClaudeCreds / writeClaudeCreds integration
// ---------------------------------------------------------------------------

func TestReadWriteClaudeCreds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".credentials.json")

	original := &claudeCredentials{
		ClaudeAiOauth: claudeOAuthToken{
			AccessToken:  "sk-ant-oat01-original",
			RefreshToken: "sk-ant-ort01-original",
			ExpiresAt:    time.Now().Add(8 * time.Hour).UnixMilli(),
			Scopes:       []string{"user:inference"},
		},
	}
	original.Extra = map[string]json.RawMessage{
		"mcpOAuth": json.RawMessage(`{"preserved":true}`),
	}

	data, _ := json.MarshalIndent(original, "", "  ")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	creds, perm, err := readClaudeCreds(path)
	if err != nil {
		t.Fatalf("readClaudeCreds: %v", err)
	}
	if perm != 0o600 {
		t.Errorf("perm got %o, want 0600", perm)
	}

	// Simulate a token update.
	creds.ClaudeAiOauth.AccessToken = "sk-ant-oat01-refreshed"
	if err := writeClaudeCreds(path, creds, perm); err != nil {
		t.Fatalf("writeClaudeCreds: %v", err)
	}

	creds2, _, err := readClaudeCreds(path)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if creds2.ClaudeAiOauth.AccessToken != "sk-ant-oat01-refreshed" {
		t.Errorf("updated token not persisted")
	}
	if _, ok := creds2.Extra["mcpOAuth"]; !ok {
		t.Error("mcpOAuth lost after write-back")
	}
}

// ---------------------------------------------------------------------------
// claudeCredPath env var resolution
// ---------------------------------------------------------------------------

func TestClaudeCredPath_ExplicitPath(t *testing.T) {
	t.Setenv("CLAUDE_CREDENTIALS_PATH", "/explicit/path.json")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CONTAINER_HOME", "")

	p, err := claudeCredPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p != "/explicit/path.json" {
		t.Errorf("got %q, want /explicit/path.json", p)
	}
}

func TestClaudeCredPath_ConfigDir(t *testing.T) {
	t.Setenv("CLAUDE_CREDENTIALS_PATH", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "/config/dir")
	t.Setenv("CONTAINER_HOME", "")

	p, err := claudeCredPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p != "/config/dir/.credentials.json" {
		t.Errorf("got %q, want /config/dir/.credentials.json", p)
	}
}

func TestClaudeCredPath_ContainerHome(t *testing.T) {
	t.Setenv("CLAUDE_CREDENTIALS_PATH", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CONTAINER_HOME", "/home/appuser")

	p, err := claudeCredPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p != "/home/appuser/.claude/.credentials.json" {
		t.Errorf("got %q, want /home/appuser/.claude/.credentials.json", p)
	}
}

func TestClaudeCredPath_NoneSet(t *testing.T) {
	t.Setenv("CLAUDE_CREDENTIALS_PATH", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CONTAINER_HOME", "")

	_, err := claudeCredPath()
	if err == nil {
		t.Error("expected error when no env vars are set")
	}
}

func TestCodexCredPath_PrefersExistingFallbackFile(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "host-codex")
	homeDir := filepath.Join(dir, "home")
	homeCodexDir := filepath.Join(homeDir, ".codex")

	if err := os.MkdirAll(homeCodexDir, 0o755); err != nil {
		t.Fatalf("mkdir home codex dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(homeCodexDir, "auth.json"), []byte(`{"tokens":{}}`), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	t.Setenv("CODEX_CONFIG_DIR", configDir)
	t.Setenv("CONTAINER_HOME", homeDir)

	p, err := codexCredPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := filepath.Join(homeCodexDir, "auth.json")
	if p != want {
		t.Errorf("got %q, want %q", p, want)
	}
}

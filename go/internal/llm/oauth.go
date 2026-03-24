package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	// oauthRefreshURL is the Anthropic OAuth token refresh endpoint.
	oauthRefreshURL = "https://console.anthropic.com/v1/oauth/token"

	// oauthClientID is the client_id used by Claude Code.
	oauthClientID = "claude-code"

	// refreshBuffer is how far before expiry we proactively refresh.
	refreshBuffer = 5 * time.Minute

	// periodicRefreshInterval is the background refresh cadence.
	periodicRefreshInterval = 30 * time.Minute
)

// credentialsFile mirrors the JSON structure of ~/.claude/.credentials.json.
type credentialsFile struct {
	ClaudeAiOauth oauthCredentials `json:"claudeAiOauth"`
}

// oauthCredentials holds the OAuth token fields from the credentials file.
type oauthCredentials struct {
	AccessToken      string   `json:"accessToken"`
	RefreshToken     string   `json:"refreshToken"`
	ExpiresAt        int64    `json:"expiresAt"` // unix milliseconds
	Scopes           []string `json:"scopes"`
	SubscriptionType string   `json:"subscriptionType"`
	RateLimitTier    string   `json:"rateLimitTier"`
}

// oauthRefreshResponse is the JSON body returned by the refresh endpoint.
type oauthRefreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"` // seconds
	TokenType    string `json:"token_type"`
}

// OAuthTokenSource reads and auto-refreshes Claude Code OAuth tokens.
// It is safe for concurrent use.
type OAuthTokenSource struct {
	credPath string

	mu           sync.RWMutex
	accessToken  string
	refreshToken string
	expiresAt    time.Time
	scopes       []string
	subType      string
	rateTier     string
}

// NewOAuthTokenSource constructs an OAuthTokenSource by loading the initial
// token from credPath. Returns an error if the file is missing or malformed.
func NewOAuthTokenSource(credPath string) (*OAuthTokenSource, error) {
	ts := &OAuthTokenSource{credPath: credPath}
	if err := ts.loadFromFile(); err != nil {
		return nil, fmt.Errorf("oauth: load credentials from %s: %w", credPath, err)
	}
	return ts, nil
}

// Token returns the current access token, refreshing if it is expired or
// within the 5-minute buffer window. On refresh failure the existing token
// is returned so that the caller can attempt the API call and see the
// resulting auth error directly.
func (ts *OAuthTokenSource) Token() (string, error) {
	ts.mu.RLock()
	needsRefresh := time.Now().Add(refreshBuffer).After(ts.expiresAt)
	token := ts.accessToken
	ts.mu.RUnlock()

	if !needsRefresh {
		return token, nil
	}

	slog.Info("oauth: token near expiry, refreshing", "expires_at", ts.expiresAt.UTC())
	if err := ts.refresh(); err != nil {
		slog.Warn("oauth: token refresh failed, returning current token", "error", err)
		// Return current token and surface the error; the caller decides.
		ts.mu.RLock()
		defer ts.mu.RUnlock()
		return ts.accessToken, err
	}

	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.accessToken, nil
}

// StartPeriodicRefresh launches a background goroutine that refreshes the
// token every 30 minutes. It stops when ctx is cancelled.
func (ts *OAuthTokenSource) StartPeriodicRefresh(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(periodicRefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := ts.refresh(); err != nil {
					slog.Warn("oauth: periodic refresh failed", "error", err)
				} else {
					slog.Info("oauth: token refreshed proactively")
				}
			}
		}
	}()
}

// refresh calls the Anthropic OAuth endpoint to obtain a new access token.
// It updates the in-memory state and persists the new credentials to disk.
func (ts *OAuthTokenSource) refresh() error {
	ts.mu.RLock()
	currentRefresh := ts.refreshToken
	ts.mu.RUnlock()

	if currentRefresh == "" {
		return fmt.Errorf("oauth: no refresh token available")
	}

	slog.Info("oauth: calling refresh endpoint", "url", oauthRefreshURL)

	formData := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {currentRefresh},
		"client_id":     {oauthClientID},
	}

	resp, err := http.Post( //nolint:gosec // URL is a package-level constant
		oauthRefreshURL,
		"application/x-www-form-urlencoded",
		strings.NewReader(formData.Encode()),
	)
	if err != nil {
		return fmt.Errorf("oauth: refresh request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("oauth: read refresh response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("oauth: refresh endpoint returned %d: %s", resp.StatusCode, body)
	}

	var result oauthRefreshResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("oauth: parse refresh response: %w", err)
	}

	if result.AccessToken == "" {
		return fmt.Errorf("oauth: refresh response missing access_token")
	}

	newExpiry := time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)

	ts.mu.Lock()
	ts.accessToken = result.AccessToken
	if result.RefreshToken != "" {
		ts.refreshToken = result.RefreshToken
	}
	ts.expiresAt = newExpiry
	ts.mu.Unlock()

	slog.Info("oauth: token refreshed successfully", "expires_at", newExpiry.UTC())

	if err := ts.saveToFile(); err != nil {
		// Non-fatal: the in-memory token is valid; log and continue.
		slog.Warn("oauth: failed to persist updated credentials", "error", err)
	}
	return nil
}

// loadFromFile reads credPath and populates the in-memory token fields.
func (ts *OAuthTokenSource) loadFromFile() error {
	data, err := os.ReadFile(ts.credPath)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	var creds credentialsFile
	if err := json.Unmarshal(data, &creds); err != nil {
		return fmt.Errorf("parse json: %w", err)
	}

	c := creds.ClaudeAiOauth
	if c.AccessToken == "" {
		return fmt.Errorf("credentials file has no accessToken")
	}

	ts.mu.Lock()
	ts.accessToken = c.AccessToken
	ts.refreshToken = c.RefreshToken
	ts.expiresAt = time.UnixMilli(c.ExpiresAt)
	ts.scopes = c.Scopes
	ts.subType = c.SubscriptionType
	ts.rateTier = c.RateLimitTier
	ts.mu.Unlock()

	return nil
}

// saveToFile writes the current in-memory token state back to credPath so
// that Claude Code and other processes pick up the rotated tokens.
func (ts *OAuthTokenSource) saveToFile() error {
	// Read the existing file first to preserve any fields we don't manage.
	data, err := os.ReadFile(ts.credPath)
	if err != nil {
		return fmt.Errorf("read existing credentials for merge: %w", err)
	}

	var creds credentialsFile
	if err := json.Unmarshal(data, &creds); err != nil {
		return fmt.Errorf("parse existing credentials for merge: %w", err)
	}

	ts.mu.RLock()
	creds.ClaudeAiOauth.AccessToken = ts.accessToken
	creds.ClaudeAiOauth.RefreshToken = ts.refreshToken
	creds.ClaudeAiOauth.ExpiresAt = ts.expiresAt.UnixMilli()
	ts.mu.RUnlock()

	updated, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal updated credentials: %w", err)
	}

	// Write with the same permissions as the original file (0600 typical).
	if err := os.WriteFile(ts.credPath, updated, 0o600); err != nil {
		return fmt.Errorf("write credentials file: %w", err)
	}
	return nil
}

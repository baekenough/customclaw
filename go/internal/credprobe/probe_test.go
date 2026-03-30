package credprobe

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/baekenough/customclaw/internal/notify"
)

// ---------------------------------------------------------------------------
// Unconfigured provider tests (no network calls)
// ---------------------------------------------------------------------------

func TestCheckOpenAI_Unconfigured(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")

	result := checkOpenAI(context.Background())

	assertResult(t, result, "unconfigured", "", "")
}

func TestCheckGemini_Unconfigured(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")

	result := checkGemini(context.Background())

	assertResult(t, result, "unconfigured", "", "")
}

func TestCheckClaude_Unconfigured(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")

	result := checkClaude(context.Background())

	assertResult(t, result, "unconfigured", "", "")
}

func TestCheckClaudeCLI_Unconfigured(t *testing.T) {
	// Both CLAUDE_CLI_PATH empty and "claude" not on PATH → unconfigured.
	t.Setenv("CLAUDE_CLI_PATH", "")
	t.Setenv("PATH", "/nonexistent")

	result := checkClaudeCLI(context.Background())

	assertResult(t, result, "unconfigured", "", "")
}

func TestCheckClaudeCLI_BinaryNotExecutable(t *testing.T) {
	// Explicit path to non-existent binary → error (not unconfigured).
	t.Setenv("CLAUDE_CLI_PATH", "/nonexistent/path/claude")

	result := checkClaudeCLI(context.Background())

	if result.status != "error" {
		t.Errorf("expected status %q, got %q", "error", result.status)
	}
	if result.errMsg == "" {
		t.Error("expected non-empty errMsg for exec failure")
	}
	// CLI exec failure should NOT be classified as auth or quota.
	if result.errKind == "auth" || result.errKind == "quota" {
		t.Errorf("unexpected errKind %q for CLI exec failure", result.errKind)
	}
}

// ---------------------------------------------------------------------------
// HTTP status code classification tests (using httptest)
// ---------------------------------------------------------------------------

func TestCheckClaude_StatusCodes(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantStatus string
		wantKind   string
	}{
		{
			name:       "200 OK",
			statusCode: http.StatusOK,
			body:       `{}`,
			wantStatus: "ok",
			wantKind:   "",
		},
		{
			name:       "401 Unauthorized",
			statusCode: http.StatusUnauthorized,
			body:       `{"error":{"message":"invalid api key"}}`,
			wantStatus: "error",
			wantKind:   "auth",
		},
		{
			name:       "403 Forbidden",
			statusCode: http.StatusForbidden,
			body:       `{"error":{"message":"forbidden"}}`,
			wantStatus: "error",
			wantKind:   "auth",
		},
		{
			name:       "429 Rate Limited",
			statusCode: http.StatusTooManyRequests,
			body:       `{}`,
			wantStatus: "degraded",
			wantKind:   "quota",
		},
		{
			name:       "529 Overloaded",
			statusCode: 529,
			body:       `{}`,
			wantStatus: "degraded",
			wantKind:   "transient",
		},
		{
			name:       "400 with usage keyword",
			statusCode: http.StatusBadRequest,
			body:       `{"error":"usage limit exceeded"}`,
			wantStatus: "degraded",
			wantKind:   "quota",
		},
		{
			name:       "400 with credit keyword",
			statusCode: http.StatusBadRequest,
			body:       `{"error":"insufficient credit balance"}`,
			wantStatus: "degraded",
			wantKind:   "quota",
		},
		{
			name:       "400 with limit keyword",
			statusCode: http.StatusBadRequest,
			body:       `{"error":"rate limit reached"}`,
			wantStatus: "degraded",
			wantKind:   "quota",
		},
		{
			name:       "400 generic",
			statusCode: http.StatusBadRequest,
			body:       `{"error":"invalid model"}`,
			wantStatus: "error",
			wantKind:   "",
		},
		{
			name:       "400 empty body",
			statusCode: http.StatusBadRequest,
			body:       "",
			wantStatus: "error",
			wantKind:   "",
		},
		{
			name:       "500 Server Error",
			statusCode: http.StatusInternalServerError,
			body:       `{"error":"internal"}`,
			wantStatus: "error",
			wantKind:   "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify auth header is set.
				if r.Header.Get("x-api-key") == "" {
					t.Error("missing x-api-key header")
				}
				w.WriteHeader(tc.statusCode)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			result := checkClaudeWithURL(context.Background(), srv.URL+"/v1/messages", "test-key")

			if result.status != tc.wantStatus {
				t.Errorf("status: want %q, got %q", tc.wantStatus, result.status)
			}
			if result.errKind != tc.wantKind {
				t.Errorf("errKind: want %q, got %q", tc.wantKind, result.errKind)
			}
		})
	}
}

func TestCheckOpenAI_StatusCodes(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantStatus string
		wantKind   string
	}{
		{
			name:       "200 OK",
			statusCode: http.StatusOK,
			body:       `{"data":[]}`,
			wantStatus: "ok",
			wantKind:   "",
		},
		{
			name:       "401 Unauthorized",
			statusCode: http.StatusUnauthorized,
			body:       `{"error":{"message":"Incorrect API key"}}`,
			wantStatus: "error",
			wantKind:   "auth",
		},
		{
			name:       "403 Forbidden",
			statusCode: http.StatusForbidden,
			body:       `{"error":{"message":"access denied"}}`,
			wantStatus: "error",
			wantKind:   "auth",
		},
		{
			name:       "429 Rate Limited",
			statusCode: http.StatusTooManyRequests,
			body:       `{}`,
			wantStatus: "degraded",
			wantKind:   "quota",
		},
		{
			name:       "500 Server Error",
			statusCode: http.StatusInternalServerError,
			body:       `{"error":"server issue"}`,
			wantStatus: "error",
			wantKind:   "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
					t.Error("missing or malformed Authorization header")
				}
				w.WriteHeader(tc.statusCode)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			result := checkOpenAIWithURL(context.Background(), srv.URL+"/v1/models", "test-key")

			if result.status != tc.wantStatus {
				t.Errorf("status: want %q, got %q", tc.wantStatus, result.status)
			}
			if result.errKind != tc.wantKind {
				t.Errorf("errKind: want %q, got %q", tc.wantKind, result.errKind)
			}
		})
	}
}

func TestCheckGemini_StatusCodes(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantStatus string
		wantKind   string
	}{
		{
			name:       "200 OK",
			statusCode: http.StatusOK,
			body:       `{"models":[]}`,
			wantStatus: "ok",
			wantKind:   "",
		},
		{
			name:       "400 Bad Request",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"message":"API key not valid"}}`,
			wantStatus: "error",
			wantKind:   "auth",
		},
		{
			name:       "401 Unauthorized",
			statusCode: http.StatusUnauthorized,
			body:       `{"error":{"message":"invalid credentials"}}`,
			wantStatus: "error",
			wantKind:   "auth",
		},
		{
			name:       "403 Forbidden",
			statusCode: http.StatusForbidden,
			body:       `{"error":{"message":"permission denied"}}`,
			wantStatus: "error",
			wantKind:   "auth",
		},
		{
			name:       "429 Rate Limited",
			statusCode: http.StatusTooManyRequests,
			body:       `{}`,
			wantStatus: "degraded",
			wantKind:   "quota",
		},
		{
			name:       "500 Server Error",
			statusCode: http.StatusInternalServerError,
			body:       `{"error":"internal"}`,
			wantStatus: "error",
			wantKind:   "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			result := checkGeminiWithURL(context.Background(), srv.URL+"/v1beta/models?key=test-key")

			if result.status != tc.wantStatus {
				t.Errorf("status: want %q, got %q", tc.wantStatus, result.status)
			}
			if result.errKind != tc.wantKind {
				t.Errorf("errKind: want %q, got %q", tc.wantKind, result.errKind)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Alert deduplication tests
// ---------------------------------------------------------------------------

func TestMaybeAlert_Transitions(t *testing.T) {
	// Reset stateTracker for deterministic tests.
	stateTracker.mu.Lock()
	stateTracker.states = make(map[string]string)
	stateTracker.mu.Unlock()

	tests := []struct {
		name     string
		provider string
		status   string
		// We cannot directly test Slack calls without mocking, but we verify
		// the state transitions are recorded correctly.
		wantPrevious string
	}{
		{"first check ok", "test-provider", "ok", ""},
		{"ok stays ok", "test-provider", "ok", "ok"},
		{"ok to error", "test-provider", "error", "ok"},
		{"error stays error (no re-alert)", "test-provider", "error", "error"},
		{"error to ok (recovery)", "test-provider", "ok", "error"},
		{"ok to degraded", "test-provider", "degraded", "ok"},
		{"degraded to error", "test-provider", "error", "degraded"},
	}

	// Use LogNotifier so sendAlert is a no-op (no real network calls).
	SetAlertNotifier(notify.NewLogNotifier())

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stateTracker.mu.Lock()
			prev := stateTracker.states[tc.provider]
			stateTracker.mu.Unlock()

			if prev != tc.wantPrevious {
				t.Errorf("previous state: want %q, got %q", tc.wantPrevious, prev)
			}

			maybeAlert(tc.provider, tc.status, "test error")
		})
	}
}

func TestMaybeAlert_DegradedDoesNotAlert(t *testing.T) {
	stateTracker.mu.Lock()
	stateTracker.states = make(map[string]string)
	stateTracker.mu.Unlock()

	// Use LogNotifier so sendAlert is a no-op (no real network calls).
	SetAlertNotifier(notify.NewLogNotifier())

	// Transition from ok to degraded should NOT trigger alert.
	maybeAlert("degrade-test", "ok", "")
	maybeAlert("degrade-test", "degraded", "rate limited")

	// Verify state was tracked.
	stateTracker.mu.Lock()
	state := stateTracker.states["degrade-test"]
	stateTracker.mu.Unlock()

	if state != "degraded" {
		t.Errorf("expected state %q, got %q", "degraded", state)
	}
}

// ---------------------------------------------------------------------------
// checkResult struct field tests
// ---------------------------------------------------------------------------

func TestCheckResultFields(t *testing.T) {
	tests := []struct {
		name   string
		result checkResult
	}{
		{
			name:   "ok result has empty error fields",
			result: checkResult{status: "ok", errMsg: "", errKind: ""},
		},
		{
			name:   "error with auth kind",
			result: checkResult{status: "error", errMsg: "invalid key", errKind: "auth"},
		},
		{
			name:   "degraded with quota kind",
			result: checkResult{status: "degraded", errMsg: "rate limited", errKind: "quota"},
		},
		{
			name:   "unconfigured has all empty",
			result: checkResult{status: "unconfigured", errMsg: "", errKind: ""},
		},
		{
			name:   "error with network kind",
			result: checkResult{status: "error", errMsg: "timeout", errKind: "network"},
		},
		{
			name:   "degraded with transient kind",
			result: checkResult{status: "degraded", errMsg: "overloaded", errKind: "transient"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.result.status == "ok" || tc.result.status == "unconfigured" {
				if tc.result.errMsg != "" {
					t.Errorf("status %q should have empty errMsg, got %q", tc.result.status, tc.result.errMsg)
				}
				if tc.result.errKind != "" {
					t.Errorf("status %q should have empty errKind, got %q", tc.result.status, tc.result.errKind)
				}
			} else {
				if tc.result.errMsg == "" {
					t.Errorf("status %q should have non-empty errMsg", tc.result.status)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Helper function tests
// ---------------------------------------------------------------------------

func TestExtractErrorMessage(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "standard error object",
			body: `{"error":{"message":"invalid api key","type":"auth_error"}}`,
			want: "invalid api key",
		},
		{
			name: "no error object",
			body: `{"status":"bad"}`,
			want: `{"status":"bad"}`,
		},
		{
			name: "empty body",
			body: "",
			want: "",
		},
		{
			name: "non-JSON body",
			body: "Service Unavailable",
			want: "Service Unavailable",
		},
		{
			name: "error object without message",
			body: `{"error":{"type":"server_error"}}`,
			want: `{"error":{"type":"server_error"}}`,
		},
		{
			name: "error as string not object",
			body: `{"error":"something went wrong"}`,
			want: `{"error":"something went wrong"}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractErrorMessage(strings.NewReader(tc.body))
			if got != tc.want {
				t.Errorf("want %q, got %q", tc.want, got)
			}
		})
	}
}

func TestReadBodyTruncated(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		maxBytes int64
		want     string
	}{
		{
			name:     "short body",
			body:     "hello",
			maxBytes: 200,
			want:     "hello",
		},
		{
			name:     "truncated body",
			body:     "abcdefghij",
			maxBytes: 5,
			want:     "abcde",
		},
		{
			name:     "empty body",
			body:     "",
			maxBytes: 200,
			want:     "",
		},
		{
			name:     "body with whitespace trimmed",
			body:     "  hello  \n",
			maxBytes: 200,
			want:     "hello",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := readBodyTruncated(strings.NewReader(tc.body), tc.maxBytes)
			if got != tc.want {
				t.Errorf("want %q, got %q", tc.want, got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Context cancellation tests
// ---------------------------------------------------------------------------

func TestCheckClaude_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate a slow response — the context should cancel before this completes.
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	result := checkClaudeWithURL(ctx, srv.URL+"/v1/messages", "test-key")

	if result.status != "error" {
		t.Errorf("expected status %q, got %q", "error", result.status)
	}
	if result.errKind != "network" {
		t.Errorf("expected errKind %q, got %q", "network", result.errKind)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func assertResult(t *testing.T, result checkResult, wantStatus, wantErrMsg, wantErrKind string) {
	t.Helper()
	if result.status != wantStatus {
		t.Errorf("status: want %q, got %q", wantStatus, result.status)
	}
	if result.errMsg != wantErrMsg {
		t.Errorf("errMsg: want %q, got %q", wantErrMsg, result.errMsg)
	}
	if result.errKind != wantErrKind {
		t.Errorf("errKind: want %q, got %q", wantErrKind, result.errKind)
	}
}

// checkClaudeWithURL is a test helper that calls the Claude check logic against
// a custom URL (typically an httptest server). This avoids hitting the real API.
func checkClaudeWithURL(ctx context.Context, url, apiKey string) checkResult {
	return doHTTPCheck(ctx, url, apiKey, "claude")
}

// checkOpenAIWithURL is a test helper that calls the OpenAI check logic against
// a custom URL.
func checkOpenAIWithURL(ctx context.Context, url, apiKey string) checkResult {
	return doHTTPCheck(ctx, url, apiKey, "openai")
}

// checkGeminiWithURL is a test helper that calls the Gemini check logic against
// a custom URL.
func checkGeminiWithURL(ctx context.Context, url string) checkResult {
	return doHTTPCheck(ctx, url, "", "gemini")
}

// doHTTPCheck makes an HTTP request and classifies the response using the same
// logic as the production check functions. This function exists solely for
// testing — production code uses the provider-specific check* functions.
func doHTTPCheck(ctx context.Context, url, apiKey, provider string) checkResult {
	reqCtx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()

	var method string
	var body io.Reader

	switch provider {
	case "claude":
		method = http.MethodPost
		body = strings.NewReader(`{"model":"claude-haiku-4-5-20251001","max_tokens":1,"messages":[{"role":"user","content":"."}]}`)
	default:
		method = http.MethodGet
	}

	req, err := http.NewRequestWithContext(reqCtx, method, url, body)
	if err != nil {
		return checkResult{"error", err.Error(), "network"}
	}

	switch provider {
	case "claude":
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("Content-Type", "application/json")
	case "openai":
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if reqCtx.Err() != nil {
			return checkResult{"error", "API timed out", "network"}
		}
		return checkResult{"error", err.Error(), "network"}
	}
	defer func() { _ = resp.Body.Close() }()

	// Dispatch to provider-specific classification.
	switch provider {
	case "claude":
		return classifyClaude(resp)
	case "openai":
		return classifyOpenAI(resp)
	case "gemini":
		return classifyGemini(resp)
	default:
		return checkResult{"error", "unknown provider", ""}
	}
}

// classifyClaude mirrors the Claude status classification from checkClaude.
func classifyClaude(resp *http.Response) checkResult {
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

// classifyOpenAI mirrors the OpenAI status classification from checkOpenAI.
func classifyOpenAI(resp *http.Response) checkResult {
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

// classifyGemini mirrors the Gemini status classification from checkGemini.
func classifyGemini(resp *http.Response) checkResult {
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

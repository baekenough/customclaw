package memory

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ─── NewEmbeddingClient ───────────────────────────────────────────────────────

func TestNewEmbeddingClient_returnsNilWhenNoAPIKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	c := NewEmbeddingClient()
	if c != nil {
		t.Error("expected nil client when OPENAI_API_KEY is unset")
	}
}

func TestNewEmbeddingClient_returnsClientWhenAPIKeySet(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test-key")
	c := NewEmbeddingClient()
	if c == nil {
		t.Error("expected non-nil client when OPENAI_API_KEY is set")
	}
}

// ─── truncateText ─────────────────────────────────────────────────────────────

func TestTruncateText_shortTextPassesThrough(t *testing.T) {
	got := truncateText("hello", 10)
	if got != "hello" {
		t.Errorf("truncateText(\"hello\", 10) = %q, want %q", got, "hello")
	}
}

func TestTruncateText_exactLimitPassesThrough(t *testing.T) {
	got := truncateText("hello", 5)
	if got != "hello" {
		t.Errorf("truncateText at exact limit: got %q, want %q", got, "hello")
	}
}

func TestTruncateText_truncatesOverLimitText(t *testing.T) {
	got := truncateText("hello world", 5)
	if got != "hello" {
		t.Errorf("truncateText(\"hello world\", 5) = %q, want %q", got, "hello")
	}
}

func TestTruncateText_handlesKorean(t *testing.T) {
	// Each Korean character is 1 rune.
	got := truncateText("안녕하세요반갑습니다", 5)
	if got != "안녕하세요" {
		t.Errorf("truncateText Korean: got %q, want %q", got, "안녕하세요")
	}
}

func TestTruncateText_emptyString(t *testing.T) {
	got := truncateText("", 100)
	if got != "" {
		t.Errorf("truncateText empty: got %q, want %q", got, "")
	}
}

// ─── Embed — request format ───────────────────────────────────────────────────

// newEmbeddingTestServer creates an httptest.Server that captures the last
// request body and returns a pre-configured response.
func newEmbeddingTestServer(t *testing.T, statusCode int, respBody string) (*httptest.Server, *embeddingRequest) {
	t.Helper()
	var captured embeddingRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &captured)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_, _ = io.WriteString(w, respBody)
	}))
	return srv, &captured
}

// fakeEmbeddingResponse builds a valid OpenAI embeddings API response with
// the given vector dimension filled with a constant value v.
func fakeEmbeddingResponse(dimension int, v float32) string {
	vec := make([]float32, dimension)
	for i := range vec {
		vec[i] = v
	}
	b, _ := json.Marshal(map[string]any{
		"data": []map[string]any{
			{"embedding": vec},
		},
	})
	return string(b)
}

// embedWithServer calls the embedding endpoint at the given URL instead of the
// default OpenAI endpoint. This helper swaps the httpClient's transport to
// redirect requests to the test server.
func embedWithServer(t *testing.T, srv *httptest.Server, text string) ([]float32, error) {
	t.Helper()
	c := &EmbeddingClient{
		apiKey:     "sk-test",
		httpClient: srv.Client(),
	}
	// Override the endpoint by temporarily patching the request URL via a
	// custom RoundTripper that redirects to the test server.
	original := c.httpClient.Transport
	c.httpClient.Transport = &redirectTransport{
		base:      original,
		targetURL: srv.URL,
	}
	return c.Embed(context.Background(), text)
}

// redirectTransport is an http.RoundTripper that rewrites the request URL to
// point to a test server, preserving the path and query.
type redirectTransport struct {
	base      http.RoundTripper
	targetURL string
}

func (rt *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	cloned.URL.Scheme = "http"
	// Extract host from targetURL (strip "http://").
	host := strings.TrimPrefix(rt.targetURL, "http://")
	cloned.URL.Host = host
	transport := rt.base
	if transport == nil {
		transport = http.DefaultTransport
	}
	return transport.RoundTrip(cloned)
}

func TestEmbed_requestBodyFormat(t *testing.T) {
	respBody := fakeEmbeddingResponse(embeddingDimension, 0.5)
	srv, captured := newEmbeddingTestServer(t, http.StatusOK, respBody)
	defer srv.Close()

	_, err := embedWithServer(t, srv, "test input text")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	if captured.Input != "test input text" {
		t.Errorf("request input = %q, want %q", captured.Input, "test input text")
	}
	if captured.Model != embeddingModel {
		t.Errorf("request model = %q, want %q", captured.Model, embeddingModel)
	}
}

func TestEmbed_parsesResponseVector(t *testing.T) {
	respBody := fakeEmbeddingResponse(embeddingDimension, 0.42)
	srv, _ := newEmbeddingTestServer(t, http.StatusOK, respBody)
	defer srv.Close()

	vec, err := embedWithServer(t, srv, "hello")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vec) != embeddingDimension {
		t.Errorf("vector length = %d, want %d", len(vec), embeddingDimension)
	}
	if vec[0] != 0.42 {
		t.Errorf("vec[0] = %f, want 0.42", vec[0])
	}
}

func TestEmbed_returnsErrorOnNon200(t *testing.T) {
	srv, _ := newEmbeddingTestServer(t, http.StatusUnauthorized, `{"error":{"message":"invalid key"}}`)
	defer srv.Close()

	_, err := embedWithServer(t, srv, "test")
	if err == nil {
		t.Error("expected error for 401 response, got nil")
	}
}

func TestEmbed_truncatesLongText(t *testing.T) {
	respBody := fakeEmbeddingResponse(embeddingDimension, 0.1)
	srv, captured := newEmbeddingTestServer(t, http.StatusOK, respBody)
	defer srv.Close()

	// Build a string longer than embeddingMaxChars runes.
	longText := strings.Repeat("a", embeddingMaxChars+1000)

	_, err := embedWithServer(t, srv, longText)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len([]rune(captured.Input)) != embeddingMaxChars {
		t.Errorf("captured input rune count = %d, want %d", len([]rune(captured.Input)), embeddingMaxChars)
	}
}

func TestEmbed_wrongDimensionReturnsError(t *testing.T) {
	// Return a vector with wrong dimension.
	respBody := fakeEmbeddingResponse(10, 0.1) // 10 dimensions instead of 1536
	srv, _ := newEmbeddingTestServer(t, http.StatusOK, respBody)
	defer srv.Close()

	_, err := embedWithServer(t, srv, "test")
	if err == nil {
		t.Error("expected error for wrong dimension, got nil")
	}
}

func TestEmbed_emptyDataReturnsError(t *testing.T) {
	srv, _ := newEmbeddingTestServer(t, http.StatusOK, `{"data":[]}`)
	defer srv.Close()

	_, err := embedWithServer(t, srv, "test")
	if err == nil {
		t.Error("expected error for empty data array, got nil")
	}
}

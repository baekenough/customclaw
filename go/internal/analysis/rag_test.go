package analysis

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// buildOSResponse constructs a minimal OpenSearch _search response body.
func buildOSResponse(hits []map[string]any) map[string]any {
	return map[string]any{
		"hits": map[string]any{
			"hits": hits,
		},
	}
}

func TestSearchRelevantCodeFormatting(t *testing.T) {
	// Build a mock OpenSearch server with two results.
	hits := []map[string]any{
		{
			"_source": map[string]any{
				"file_path":   "/home/user/oh-my-customcode/src/main.go",
				"content":     "package main\n\nfunc main() {}",
				"chunk_index": 0,
			},
		},
		{
			"_source": map[string]any{
				"file_path":   "/home/user/other/util.py",
				"content":     "def helper(): pass",
				"chunk_index": 0,
			},
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(buildOSResponse(hits))
	}))
	defer ts.Close()

	t.Setenv("OPENSEARCH_URL", ts.URL)

	result := searchRelevantCode(context.Background(), "test issue", "some body text")

	if result == "" {
		t.Fatal("expected non-empty result")
	}

	// Path inside /oh-my-customcode/ should be shortened.
	if strings.Contains(result, "/home/user/oh-my-customcode/") {
		t.Error("expected path to be shortened, but full path found")
	}
	if !strings.Contains(result, "src/main.go") {
		t.Error("expected shortened path 'src/main.go' in result")
	}

	// Non-shortpath file should appear as-is.
	if !strings.Contains(result, "/home/user/other/util.py") {
		t.Error("expected full path for non-omc file")
	}

	// Code content should be wrapped in markdown code blocks.
	if !strings.Contains(result, "```") {
		t.Error("expected markdown code block in result")
	}
	if !strings.Contains(result, "package main") {
		t.Error("expected Go source content in result")
	}
}

func TestSearchRelevantCodeDeduplication(t *testing.T) {
	// Two hits with the same file_path — only first should appear.
	hits := []map[string]any{
		{
			"_source": map[string]any{
				"file_path":   "/repo/pkg/foo.go",
				"content":     "chunk 0",
				"chunk_index": 0,
			},
		},
		{
			"_source": map[string]any{
				"file_path":   "/repo/pkg/foo.go",
				"content":     "chunk 1",
				"chunk_index": 1,
			},
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(buildOSResponse(hits))
	}))
	defer ts.Close()

	t.Setenv("OPENSEARCH_URL", ts.URL)

	result := searchRelevantCode(context.Background(), "title", "body")

	// Only one block for foo.go.
	count := strings.Count(result, "foo.go")
	if count != 1 {
		t.Errorf("expected 1 occurrence of foo.go, got %d", count)
	}

	// Second chunk's content should not appear.
	if strings.Contains(result, "chunk 1") {
		t.Error("expected second chunk to be deduplicated")
	}
}

func TestSearchRelevantCodeEmpty(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(buildOSResponse(nil))
	}))
	defer ts.Close()

	t.Setenv("OPENSEARCH_URL", ts.URL)

	result := searchRelevantCode(context.Background(), "title", "body")
	if result != "" {
		t.Errorf("expected empty result for zero hits, got %q", result)
	}
}

func TestSearchRelevantCodeServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	t.Setenv("OPENSEARCH_URL", ts.URL)

	// Should return "" without panicking.
	result := searchRelevantCode(context.Background(), "title", "body")
	if result != "" {
		t.Errorf("expected empty result on server error, got %q", result)
	}
}

func TestSearchRelevantCodeTruncation(t *testing.T) {
	// Content longer than ragMaxChunk should be truncated.
	longContent := strings.Repeat("x", ragMaxChunk+100)
	hits := []map[string]any{
		{
			"_source": map[string]any{
				"file_path":   "/repo/big.go",
				"content":     longContent,
				"chunk_index": 0,
			},
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(buildOSResponse(hits))
	}))
	defer ts.Close()

	t.Setenv("OPENSEARCH_URL", ts.URL)

	result := searchRelevantCode(context.Background(), "title", "body")
	if !strings.Contains(result, "(truncated)") {
		t.Error("expected truncation marker in result for long content")
	}
	if strings.Contains(result, longContent) {
		t.Error("expected original long content to be truncated")
	}
}

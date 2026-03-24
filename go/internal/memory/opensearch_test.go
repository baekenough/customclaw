package memory

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ─── indexSettings structure ─────────────────────────────────────────────────

func TestIndexSettings_hasAnalysisBlock(t *testing.T) {
	b, err := json.Marshal(indexSettings)
	if err != nil {
		t.Fatalf("marshal indexSettings: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal indexSettings: %v", err)
	}

	settings, ok := m["settings"].(map[string]any)
	if !ok {
		t.Fatal("indexSettings: missing top-level 'settings' key")
	}
	analysis, ok := settings["analysis"].(map[string]any)
	if !ok {
		t.Fatal("indexSettings: missing settings.analysis")
	}
	analyzer, ok := analysis["analyzer"].(map[string]any)
	if !ok {
		t.Fatal("indexSettings: missing settings.analysis.analyzer")
	}
	korean, ok := analyzer["korean"].(map[string]any)
	if !ok {
		t.Fatal("indexSettings: missing settings.analysis.analyzer.korean")
	}
	if korean["tokenizer"] != "nori_tokenizer" {
		t.Errorf("korean tokenizer = %v, want nori_tokenizer", korean["tokenizer"])
	}
}

func TestIndexSettings_hasMappingsWithRequiredFields(t *testing.T) {
	b, err := json.Marshal(indexSettings)
	if err != nil {
		t.Fatalf("marshal indexSettings: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal indexSettings: %v", err)
	}

	mappings, ok := m["mappings"].(map[string]any)
	if !ok {
		t.Fatal("indexSettings: missing 'mappings'")
	}
	props, ok := mappings["properties"].(map[string]any)
	if !ok {
		t.Fatal("indexSettings: missing mappings.properties")
	}

	required := []string{"bot_id", "user_id", "category", "content", "created_at"}
	for _, field := range required {
		if _, exists := props[field]; !exists {
			t.Errorf("indexSettings: missing field %q in mappings.properties", field)
		}
	}
}

func TestIndexSettings_contentUsesKoreanAnalyzer(t *testing.T) {
	b, err := json.Marshal(indexSettings)
	if err != nil {
		t.Fatalf("marshal indexSettings: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	mappings := m["mappings"].(map[string]any)
	props := mappings["properties"].(map[string]any)
	content := props["content"].(map[string]any)

	if content["analyzer"] != "korean" {
		t.Errorf("content.analyzer = %v, want 'korean'", content["analyzer"])
	}
}

func TestIndexSettings_keywordFields(t *testing.T) {
	b, err := json.Marshal(indexSettings)
	if err != nil {
		t.Fatalf("marshal indexSettings: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	mappings := m["mappings"].(map[string]any)
	props := mappings["properties"].(map[string]any)

	for _, field := range []string{"bot_id", "user_id", "category"} {
		f, ok := props[field].(map[string]any)
		if !ok {
			t.Fatalf("field %q missing", field)
		}
		if f["type"] != "keyword" {
			t.Errorf("field %q type = %v, want 'keyword'", field, f["type"])
		}
	}
}

// ─── search request body construction ────────────────────────────────────────

// buildSearchBody mirrors the logic inside OpenSearchClient.Search so we can
// unit-test the JSON structure without making actual HTTP calls.
func buildSearchBody(botID, query string, topK int) ([]byte, error) {
	reqBody := osSearchRequest{
		Query: osQuery{
			Bool: osBool{
				Must: []osMatch{
					{
						Match: map[string]osMatchField{
							"content": {
								Query:    query,
								Analyzer: "korean",
							},
						},
					},
				},
				Filter: []osTerm{
					{Term: map[string]string{"bot_id": botID}},
				},
			},
		},
		Size: topK,
	}
	return json.Marshal(reqBody)
}

func TestSearchRequestBody_hasQueryAndSize(t *testing.T) {
	body, err := buildSearchBody("bot1", "서버 상태", 5)
	if err != nil {
		t.Fatalf("buildSearchBody: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("unmarshal search body: %v", err)
	}
	if int(m["size"].(float64)) != 5 {
		t.Errorf("size = %v, want 5", m["size"])
	}
	if _, ok := m["query"]; !ok {
		t.Error("search body missing 'query'")
	}
}

func TestSearchRequestBody_filterByBotID(t *testing.T) {
	body, err := buildSearchBody("mybot", "test", 3)
	if err != nil {
		t.Fatalf("buildSearchBody: %v", err)
	}
	// The bot_id filter must appear somewhere in the JSON.
	if !bytes.Contains(body, []byte("mybot")) {
		t.Errorf("search body does not contain bot_id 'mybot': %s", body)
	}
	if !bytes.Contains(body, []byte("bot_id")) {
		t.Errorf("search body does not contain 'bot_id' key: %s", body)
	}
}

func TestSearchRequestBody_usesKoreanAnalyzer(t *testing.T) {
	body, err := buildSearchBody("bot1", "안녕", 5)
	if err != nil {
		t.Fatalf("buildSearchBody: %v", err)
	}
	if !bytes.Contains(body, []byte("korean")) {
		t.Errorf("search body does not mention 'korean' analyzer: %s", body)
	}
}

func TestSearchRequestBody_matchQueryField(t *testing.T) {
	body, err := buildSearchBody("bot1", "확인해줘", 10)
	if err != nil {
		t.Fatalf("buildSearchBody: %v", err)
	}
	if !bytes.Contains(body, []byte("확인해줘")) {
		t.Errorf("search body does not contain query text: %s", body)
	}
	if !bytes.Contains(body, []byte("content")) {
		t.Errorf("search body does not target 'content' field: %s", body)
	}
}

// ─── OpenSearchClient HTTP behaviour ─────────────────────────────────────────

// newTestServer creates a minimal OpenSearch stub that handles:
//   - HEAD /{index} → 404 (so ensureIndex creates it)
//   - PUT /{index} → 200
//   - POST /{index}/_search → returns a single hit
//   - DELETE /{index}/_doc/{id} → 200
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodHead:
			w.WriteHeader(http.StatusNotFound)

		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, `{"acknowledged":true}`) //nolint:errcheck

		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/_search"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, `{
				"hits": {
					"hits": [
						{
							"_id": "mem1",
							"_score": 3.5,
							"_source": {
								"content": "서버 상태 정상",
								"category": "status",
								"created_at": "2025-01-01T00:00:00Z"
							}
						}
					]
				}
			}`) //nolint:errcheck

		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, `{"result":"deleted"}`) //nolint:errcheck

		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/_doc/"):
			w.WriteHeader(http.StatusCreated)
			io.WriteString(w, `{"result":"created"}`) //nolint:errcheck

		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
}

func TestNewOpenSearchClient_connectsToTestServer(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	client, err := NewOpenSearchClient(srv.URL)
	if err != nil {
		t.Fatalf("NewOpenSearchClient: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestOpenSearchClient_Search_returnsResults(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	client, err := NewOpenSearchClient(srv.URL)
	if err != nil {
		t.Fatalf("NewOpenSearchClient: %v", err)
	}

	results, err := client.Search(t.Context(), "bot1", "서버", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Search: got %d results, want 1", len(results))
	}
	if results[0].Content != "서버 상태 정상" {
		t.Errorf("result content = %q, want %q", results[0].Content, "서버 상태 정상")
	}
	if results[0].Score != 3.5 {
		t.Errorf("result score = %f, want 3.5", results[0].Score)
	}
}

func TestOpenSearchClient_Delete_succeeds(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	client, err := NewOpenSearchClient(srv.URL)
	if err != nil {
		t.Fatalf("NewOpenSearchClient: %v", err)
	}

	if err := client.Delete(t.Context(), "mem1"); err != nil {
		t.Errorf("Delete: unexpected error: %v", err)
	}
}

func TestOpenSearchClient_IndexMemory_succeeds(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	client, err := NewOpenSearchClient(srv.URL)
	if err != nil {
		t.Fatalf("NewOpenSearchClient: %v", err)
	}

	err = client.IndexMemory(t.Context(), "id1", "bot1", "test content", "status", "user1")
	if err != nil {
		t.Errorf("IndexMemory: unexpected error: %v", err)
	}
}

func TestNewOpenSearchClient_unavailableServerReturnsError(t *testing.T) {
	_, err := NewOpenSearchClient("http://127.0.0.1:19999")
	if err == nil {
		t.Error("expected error for unreachable server, got nil")
	}
}

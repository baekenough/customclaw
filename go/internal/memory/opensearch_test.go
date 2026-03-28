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

	required := []string{"bot_id", "user_id", "category", "content", "created_at", "embedding_version", "embedded_at", "content_vector"}
	for _, field := range required {
		if _, exists := props[field]; !exists {
			t.Errorf("indexSettings: missing field %q in mappings.properties", field)
		}
	}
}

func TestIndexSettings_contentVectorIsKNNField(t *testing.T) {
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
	cv, ok := props["content_vector"].(map[string]any)
	if !ok {
		t.Fatal("indexSettings: missing content_vector field")
	}
	if cv["type"] != "knn_vector" {
		t.Errorf("content_vector type = %v, want 'knn_vector'", cv["type"])
	}
	// JSON unmarshal decodes numbers as float64.
	dim, ok := cv["dimension"].(float64)
	if !ok {
		t.Fatalf("content_vector dimension is not float64 (JSON number): %T", cv["dimension"])
	}
	if int(dim) != embeddingDimension {
		t.Errorf("content_vector dimension = %d, want %d", int(dim), embeddingDimension)
	}
	method, ok := cv["method"].(map[string]any)
	if !ok {
		t.Fatal("content_vector: missing method")
	}
	if method["name"] != "hnsw" {
		t.Errorf("content_vector method.name = %v, want 'hnsw'", method["name"])
	}
	if method["space_type"] != "cosinesimil" {
		t.Errorf("content_vector method.space_type = %v, want 'cosinesimil'", method["space_type"])
	}
}

func TestIndexSettings_knnEnabledInSettings(t *testing.T) {
	b, err := json.Marshal(indexSettings)
	if err != nil {
		t.Fatalf("marshal indexSettings: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	settings, ok := m["settings"].(map[string]any)
	if !ok {
		t.Fatal("indexSettings: missing 'settings'")
	}
	index, ok := settings["index"].(map[string]any)
	if !ok {
		t.Fatal("indexSettings: missing settings.index")
	}
	if index["knn"] != true {
		t.Errorf("settings.index.knn = %v, want true", index["knn"])
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

	for _, field := range []string{"bot_id", "user_id", "category", "embedding_version"} {
		f, ok := props[field].(map[string]any)
		if !ok {
			t.Fatalf("field %q missing", field)
		}
		if f["type"] != "keyword" {
			t.Errorf("field %q type = %v, want 'keyword'", field, f["type"])
		}
	}
}

func TestIndexSettings_embeddedAtIsDateField(t *testing.T) {
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
	f, ok := props["embedded_at"].(map[string]any)
	if !ok {
		t.Fatal("indexSettings: missing 'embedded_at' field")
	}
	if f["type"] != "date" {
		t.Errorf("embedded_at type = %v, want 'date'", f["type"])
	}
}

// ─── versionedIndexName ───────────────────────────────────────────────────────

func TestVersionedIndexName(t *testing.T) {
	got := versionedIndexName(1)
	want := "customclaw-memories-v1"
	if got != want {
		t.Errorf("versionedIndexName(1) = %q, want %q", got, want)
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

// newTestServer creates a minimal OpenSearch stub that simulates a fresh install
// (no alias, no legacy index). It handles:
//   - GET  /_alias/{name}        → 404  (no alias yet)
//   - HEAD /{index}              → 404  (no plain index)
//   - PUT  /{index}              → 200  (create index)
//   - POST /_aliases             → 200  (create alias)
//   - POST /{alias}/_search      → one hit
//   - POST /{alias}/_doc/{id}    → 201
//   - DELETE /{alias}/_doc/{id}  → 200
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		// Alias existence check — always 404 (no alias, fresh install).
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/_alias/"):
			w.WriteHeader(http.StatusNotFound)

		// Plain index existence check — always 404 (no legacy, fresh install).
		case r.Method == http.MethodHead:
			w.WriteHeader(http.StatusNotFound)

		// Create index.
		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"acknowledged":true}`)

		// Create alias.
		case r.Method == http.MethodPost && r.URL.Path == "/_aliases":
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"acknowledged":true}`)

		// Search.
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/_search"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{
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
			}`)

		// Delete document.
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/_doc/"):
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"result":"deleted"}`)

		// Index document.
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/_doc/"):
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"result":"created"}`)

		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
}

// newLegacyMigrationTestServer simulates a cluster that still has the
// unversioned plain index (legacy) but no alias. It records which endpoints
// were called so tests can assert migration steps were executed.
func newLegacyMigrationTestServer(t *testing.T) (*httptest.Server, *migrationTrace) {
	t.Helper()
	trace := &migrationTrace{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		// Alias check — 404 (alias does not exist yet).
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/_alias/"):
			w.WriteHeader(http.StatusNotFound)

		// HEAD /{index}: legacy exists, versioned does not.
		case r.Method == http.MethodHead:
			if r.URL.Path == "/"+legacyIndexName {
				w.WriteHeader(http.StatusOK) // legacy plain index exists
			} else {
				w.WriteHeader(http.StatusNotFound) // versioned index absent
			}

		// Create versioned index.
		case r.Method == http.MethodPut:
			trace.createIndexCalled = true
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"acknowledged":true}`)

		// POST /_reindex.
		case r.Method == http.MethodPost && r.URL.Path == "/_reindex":
			trace.reindexCalled = true
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"total":1,"created":1}`)

		// POST /_aliases.
		case r.Method == http.MethodPost && r.URL.Path == "/_aliases":
			trace.aliasCalled = true
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"acknowledged":true}`)

		// DELETE /{index} — legacy cleanup.
		case r.Method == http.MethodDelete && !strings.Contains(r.URL.Path, "/_doc/"):
			trace.deleteLegacyCalled = true
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"acknowledged":true}`)

		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	return srv, trace
}

// migrationTrace records which migration steps were exercised.
type migrationTrace struct {
	createIndexCalled  bool
	reindexCalled      bool
	aliasCalled        bool
	deleteLegacyCalled bool
}

// newAliasAlreadyExistsTestServer simulates a cluster where the alias is
// already in place. ensureIndex should be a no-op.
func newAliasAlreadyExistsTestServer(t *testing.T) (*httptest.Server, *bool) {
	t.Helper()
	createCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		// Alias exists — return 200 with alias metadata.
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/_alias/"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"customclaw-memories-v1":{"aliases":{"customclaw-memories":{}}}}`)

		// Any PUT would mean we tried to create an index unexpectedly.
		case r.Method == http.MethodPut:
			createCalled = true
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"acknowledged":true}`)

		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	return srv, &createCalled
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

	err = client.IndexMemory(t.Context(), "id1", "bot1", "test content", "status", "user1", nil)
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

// ─── alias / migration behaviour ─────────────────────────────────────────────

func TestEnsureIndex_freshInstall_createsVersionedIndexAndAlias(t *testing.T) {
	srv := newTestServer(t) // no alias, no legacy
	defer srv.Close()

	// NewOpenSearchClient calls ensureIndex internally; success means both
	// createIndex and createAlias were invoked without error.
	client, err := NewOpenSearchClient(srv.URL)
	if err != nil {
		t.Fatalf("NewOpenSearchClient (fresh install): %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestEnsureIndex_aliasAlreadyExists_isNoop(t *testing.T) {
	srv, createCalled := newAliasAlreadyExistsTestServer(t)
	defer srv.Close()

	client, err := NewOpenSearchClient(srv.URL)
	if err != nil {
		t.Fatalf("NewOpenSearchClient (alias exists): %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if *createCalled {
		t.Error("ensureIndex should not call PUT (create index) when alias already exists")
	}
}

func TestEnsureIndex_legacyIndexExists_performsMigration(t *testing.T) {
	srv, trace := newLegacyMigrationTestServer(t)
	defer srv.Close()

	client, err := NewOpenSearchClient(srv.URL)
	if err != nil {
		t.Fatalf("NewOpenSearchClient (legacy migration): %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}

	if !trace.createIndexCalled {
		t.Error("migration: versioned index was not created")
	}
	if !trace.reindexCalled {
		t.Error("migration: _reindex was not called")
	}
	if !trace.aliasCalled {
		t.Error("migration: alias was not created")
	}
	if !trace.deleteLegacyCalled {
		t.Error("migration: legacy index was not deleted")
	}
}

func TestIndexURL_usesAliasName(t *testing.T) {
	c := &OpenSearchClient{baseURL: "http://localhost:9200"}
	got := c.indexURL("/_search")
	want := "http://localhost:9200/" + AliasName + "/_search"
	if got != want {
		t.Errorf("indexURL = %q, want %q", got, want)
	}
}

// ─── IndexMemory with vector ──────────────────────────────────────────────────

func TestOpenSearchClient_IndexMemory_withVector(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/_alias/"):
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodHead:
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"acknowledged":true}`)
		case r.Method == http.MethodPost && r.URL.Path == "/_aliases":
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"acknowledged":true}`)
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/_doc/"):
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &captured)
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"result":"created"}`)
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	client, err := NewOpenSearchClient(srv.URL)
	if err != nil {
		t.Fatalf("NewOpenSearchClient: %v", err)
	}

	vec := make([]float32, embeddingDimension)
	for i := range vec {
		vec[i] = 0.1
	}

	if err := client.IndexMemory(t.Context(), "id1", "bot1", "test content", "status", "user1", vec); err != nil {
		t.Fatalf("IndexMemory with vector: %v", err)
	}

	if _, ok := captured["content_vector"]; !ok {
		t.Error("IndexMemory with vector: document missing content_vector field")
	}
	if captured["embedding_version"] != "v1" {
		t.Errorf("embedding_version = %v, want 'v1'", captured["embedding_version"])
	}
}

func TestOpenSearchClient_IndexMemory_nilVectorOmitsField(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/_alias/"):
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodHead:
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"acknowledged":true}`)
		case r.Method == http.MethodPost && r.URL.Path == "/_aliases":
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"acknowledged":true}`)
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/_doc/"):
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &captured)
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"result":"created"}`)
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	client, err := NewOpenSearchClient(srv.URL)
	if err != nil {
		t.Fatalf("NewOpenSearchClient: %v", err)
	}

	if err := client.IndexMemory(t.Context(), "id2", "bot1", "test content", "status", "user1", nil); err != nil {
		t.Fatalf("IndexMemory nil vector: %v", err)
	}

	if _, ok := captured["content_vector"]; ok {
		t.Error("IndexMemory with nil vector should not include content_vector in document")
	}
	if captured["embedding_version"] != "v1-bm25" {
		t.Errorf("embedding_version = %v, want 'v1-bm25'", captured["embedding_version"])
	}
}

// ─── HybridSearchOS ───────────────────────────────────────────────────────────

func TestHybridSearchOS_withVector_usesHybridQuery(t *testing.T) {
	var capturedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/_alias/"):
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodHead:
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"acknowledged":true}`)
		case r.Method == http.MethodPost && r.URL.Path == "/_aliases":
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"acknowledged":true}`)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/_search"):
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &capturedBody)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"hits":{"hits":[{"_id":"m1","_score":0.9,"_source":{"content":"hybrid result","category":"fact","created_at":"2025-01-01T00:00:00Z"}}]}}`)
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	client, err := NewOpenSearchClient(srv.URL)
	if err != nil {
		t.Fatalf("NewOpenSearchClient: %v", err)
	}

	vec := make([]float32, embeddingDimension)
	results, err := client.HybridSearchOS(t.Context(), "bot1", "서버 상태", vec, 5)
	if err != nil {
		t.Fatalf("HybridSearchOS: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("HybridSearchOS: got %d results, want 1", len(results))
	}
	if results[0].Content != "hybrid result" {
		t.Errorf("result content = %q, want %q", results[0].Content, "hybrid result")
	}

	// Verify the request body contains the hybrid query structure.
	query, ok := capturedBody["query"].(map[string]any)
	if !ok {
		t.Fatal("request body missing 'query'")
	}
	if _, ok := query["hybrid"]; !ok {
		t.Error("hybrid search: query does not use 'hybrid' query type")
	}

	// Verify _source.excludes contains content_vector.
	src, ok := capturedBody["_source"].(map[string]any)
	if !ok {
		t.Fatal("request body missing '_source'")
	}
	excludes, ok := src["excludes"].([]any)
	if !ok {
		t.Fatal("_source missing 'excludes'")
	}
	found := false
	for _, e := range excludes {
		if e == "content_vector" {
			found = true
			break
		}
	}
	if !found {
		t.Error("_source.excludes should contain 'content_vector'")
	}
}

func TestHybridSearchOS_nilVector_fallsBackToBM25(t *testing.T) {
	var capturedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/_alias/"):
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodHead:
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"acknowledged":true}`)
		case r.Method == http.MethodPost && r.URL.Path == "/_aliases":
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"acknowledged":true}`)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/_search"):
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &capturedBody)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"hits":{"hits":[]}}`)
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	client, err := NewOpenSearchClient(srv.URL)
	if err != nil {
		t.Fatalf("NewOpenSearchClient: %v", err)
	}

	// nil vector should use the BM25 Search path, which builds a bool query.
	_, err = client.HybridSearchOS(t.Context(), "bot1", "서버", nil, 5)
	if err != nil {
		t.Fatalf("HybridSearchOS nil vector: %v", err)
	}

	// BM25 path uses bool query, not hybrid.
	query, ok := capturedBody["query"].(map[string]any)
	if !ok {
		t.Fatal("request body missing 'query'")
	}
	if _, ok := query["hybrid"]; ok {
		t.Error("nil vector should NOT use hybrid query type")
	}
	if _, ok := query["bool"]; !ok {
		t.Error("nil vector should fall back to bool (BM25) query")
	}
}

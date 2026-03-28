package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	// AliasName is the OpenSearch alias that points to the active versioned index.
	// All read and write operations go through this alias transparently.
	AliasName = "customclaw-memories"

	// currentIndexVersion is bumped when the mapping changes require reindexing.
	// A bump triggers blue-green migration on the next startup.
	currentIndexVersion = 1

	// legacyIndexName is the pre-alias plain index name used before versioning
	// was introduced. It is only referenced during one-time migration.
	legacyIndexName = "customclaw-memories"
)

// versionedIndexName returns the concrete index name for the given version.
func versionedIndexName(version int) string {
	return fmt.Sprintf("customclaw-memories-v%d", version)
}

// indexSettings is the JSON body for creating the memories index with the
// nori Korean analyzer and kNN vector field for hybrid search.
var indexSettings = map[string]any{
	"settings": map[string]any{
		"index": map[string]any{
			"knn": true, // Enable kNN for this index (required for knn_vector fields).
		},
		"analysis": map[string]any{
			"analyzer": map[string]any{
				"korean": map[string]any{
					"type":      "custom",
					"tokenizer": "nori_tokenizer",
					"filter":    []string{"nori_readingform", "lowercase"},
				},
			},
		},
	},
	"mappings": map[string]any{
		"properties": map[string]any{
			"bot_id":            map[string]any{"type": "keyword"},
			"user_id":           map[string]any{"type": "keyword"},
			"category":          map[string]any{"type": "keyword"},
			"content":           map[string]any{"type": "text", "analyzer": "korean"},
			"created_at":        map[string]any{"type": "date"},
			"embedding_version": map[string]any{"type": "keyword"},
			"embedded_at":       map[string]any{"type": "date"},
			"content_vector": map[string]any{
				"type":      "knn_vector",
				"dimension": embeddingDimension,
				"method": map[string]any{
					"name":       "hnsw",
					"space_type": "cosinesimil",
					"engine":     "lucene",
				},
			},
		},
	},
}

// OpenSearchClient wraps the OpenSearch REST API using net/http.
// It is safe for concurrent use.
type OpenSearchClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewOpenSearchClient creates a client, connects to url, and ensures the
// memories alias and versioned index exist. If url is empty it falls back to
// the OPENSEARCH_URL environment variable (default "http://opensearch:9200").
func NewOpenSearchClient(url string) (*OpenSearchClient, error) {
	if url == "" {
		url = os.Getenv("OPENSEARCH_URL")
	}
	if url == "" {
		url = "http://opensearch:9200"
	}
	url = strings.TrimRight(url, "/")

	c := &OpenSearchClient{
		baseURL:    url,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
	if err := c.ensureIndex(context.Background()); err != nil {
		return nil, fmt.Errorf("opensearch ensureIndex: %w", err)
	}
	return c, nil
}

// ensureIndex establishes the alias-backed versioned index. It is idempotent.
//
// Decision tree:
//  1. If AliasName is already an alias → nothing to do.
//  2. If the unversioned legacy index "customclaw-memories" exists as a plain
//     index → migrate it to v1 and create the alias.
//  3. Otherwise → create the v1 index and the alias from scratch.
func (c *OpenSearchClient) ensureIndex(ctx context.Context) error {
	aliasExists, err := c.aliasExists(ctx, AliasName)
	if err != nil {
		return fmt.Errorf("check alias: %w", err)
	}
	if aliasExists {
		return nil // alias already in place; nothing to do
	}

	// Alias does not exist. Check if the legacy plain index is still around.
	legacyExists, err := c.plainIndexExists(ctx, legacyIndexName)
	if err != nil {
		return fmt.Errorf("check legacy index: %w", err)
	}

	target := versionedIndexName(currentIndexVersion)

	if legacyExists {
		return c.migrateToVersionedIndex(ctx, target)
	}

	// Fresh install: create v1 index + alias.
	if err := c.createIndex(ctx, target); err != nil {
		return fmt.Errorf("create index %s: %w", target, err)
	}
	if err := c.createAlias(ctx, target, AliasName); err != nil {
		return fmt.Errorf("create alias %s→%s: %w", AliasName, target, err)
	}
	log.Printf("opensearch: created index %s with alias %s", target, AliasName)
	return nil
}

// migrateToVersionedIndex performs the one-time migration from a legacy plain
// index to a versioned index with an alias. It is idempotent: if the versioned
// index already exists the reindex step is skipped and the alias is (re-)applied.
//
// Migration steps:
//  1. Create versioned index with current mappings.
//  2. Reindex documents from legacy → versioned.
//  3. Create alias pointing to versioned index.
//  4. Delete legacy index.
func (c *OpenSearchClient) migrateToVersionedIndex(ctx context.Context, target string) error {
	log.Printf("opensearch: migrating legacy index %q to %s", legacyIndexName, target)

	// Step 1: create versioned index (may already exist if a previous migration
	// was interrupted after the create but before the alias creation).
	targetExists, err := c.plainIndexExists(ctx, target)
	if err != nil {
		return fmt.Errorf("check target index %s: %w", target, err)
	}
	if !targetExists {
		if err := c.createIndex(ctx, target); err != nil {
			return fmt.Errorf("create index %s: %w", target, err)
		}
		log.Printf("opensearch: created versioned index %s", target)
	}

	// Step 2: reindex legacy → versioned.
	if err := c.reindex(ctx, legacyIndexName, target); err != nil {
		return fmt.Errorf("reindex %s→%s: %w", legacyIndexName, target, err)
	}
	log.Printf("opensearch: reindexed %s → %s", legacyIndexName, target)

	// Step 3: create alias.
	if err := c.createAlias(ctx, target, AliasName); err != nil {
		return fmt.Errorf("create alias %s→%s: %w", AliasName, target, err)
	}
	log.Printf("opensearch: alias %s → %s created", AliasName, target)

	// Step 4: delete the legacy plain index now that the alias owns the name.
	if err := c.deleteIndex(ctx, legacyIndexName); err != nil {
		return fmt.Errorf("delete legacy index %s: %w", legacyIndexName, err)
	}
	log.Printf("opensearch: deleted legacy index %s", legacyIndexName)
	return nil
}

// ─── low-level helpers ────────────────────────────────────────────────────────

// aliasExists reports whether name is an OpenSearch alias (not a plain index).
// GET /_alias/{name} returns 200 if it is an alias, 404 otherwise.
func (c *OpenSearchClient) aliasExists(ctx context.Context, name string) (bool, error) {
	url := c.baseURL + "/_alias/" + name
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	_ = resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("GET /_alias/%s status %d", name, resp.StatusCode)
	}
}

// plainIndexExists reports whether name exists as a concrete index.
// HEAD /{name} returns 200 if it exists, 404 otherwise.
func (c *OpenSearchClient) plainIndexExists(ctx context.Context, name string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, c.baseURL+"/"+name, nil)
	if err != nil {
		return false, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	_ = resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("HEAD /%s status %d", name, resp.StatusCode)
	}
}

// createIndex creates a new index with the standard index settings and mappings.
func (c *OpenSearchClient) createIndex(ctx context.Context, name string) error {
	body, err := json.Marshal(indexSettings)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+"/"+name, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("PUT /%s status %d: %s", name, resp.StatusCode, b)
	}
	return nil
}

// createAlias creates an alias pointing to the given index.
// POST /_aliases with an add action is the canonical approach.
func (c *OpenSearchClient) createAlias(ctx context.Context, index, alias string) error {
	payload := map[string]any{
		"actions": []map[string]any{
			{"add": map[string]any{"index": index, "alias": alias}},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/_aliases", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("POST /_aliases status %d: %s", resp.StatusCode, b)
	}
	return nil
}

// reindex copies all documents from src to dst using the OpenSearch _reindex API.
func (c *OpenSearchClient) reindex(ctx context.Context, src, dst string) error {
	payload := map[string]any{
		"source": map[string]any{"index": src},
		"dest":   map[string]any{"index": dst},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/_reindex", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("POST /_reindex status %d: %s", resp.StatusCode, b)
	}
	return nil
}

// deleteIndex deletes a plain index by name.
func (c *OpenSearchClient) deleteIndex(ctx context.Context, name string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+"/"+name, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("DELETE /%s status %d: %s", name, resp.StatusCode, b)
	}
	return nil
}

// IndexMemory indexes a single memory document.
// memoryID is used as the document _id for consistency with PostgreSQL.
// contentVector is the embedding vector for the content field. When nil,
// the content_vector field is omitted and the document falls back to BM25-only
// search — this preserves backward compatibility with existing documents.
func (c *OpenSearchClient) IndexMemory(ctx context.Context, memoryID, botID, content, category, userID string, contentVector []float32) error {
	now := time.Now().UTC().Format(time.RFC3339)
	embVersion := "v1-bm25"
	if contentVector != nil {
		embVersion = "v1"
	}
	doc := map[string]any{
		"bot_id":            botID,
		"user_id":           userID,
		"category":          category,
		"content":           content,
		"created_at":        now,
		"embedding_version": embVersion,
		"embedded_at":       now,
	}
	if contentVector != nil {
		doc["content_vector"] = contentVector
	}
	body, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	// POST /{alias}/_doc/{id}
	url := c.indexURL("/_doc/" + memoryID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("index doc status %d: %s", resp.StatusCode, b)
	}
	return nil
}

// osSearchRequest is the JSON body sent to OpenSearch _search.
type osSearchRequest struct {
	Query osQuery `json:"query"`
	Size  int     `json:"size"`
}

type osQuery struct {
	Bool osBool `json:"bool"`
}

type osBool struct {
	Must   []osMatch `json:"must"`
	Filter []osTerm  `json:"filter"`
}

type osMatch struct {
	Match map[string]osMatchField `json:"match"`
}

type osMatchField struct {
	Query    string `json:"query"`
	Analyzer string `json:"analyzer"`
}

type osTerm struct {
	Term map[string]string `json:"term"`
}

// osSearchResponse partially decodes the OpenSearch _search response.
type osSearchResponse struct {
	Hits struct {
		Hits []struct {
			ID    string  `json:"_id"`
			Score float64 `json:"_score"`
			Source struct {
				Content   string `json:"content"`
				Category  string `json:"category"`
				CreatedAt string `json:"created_at"`
			} `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
}

// Search searches memories by Korean keyword using the nori analyzer.
// It returns at most topK results, sorted by relevance score descending.
// Errors are logged and an empty slice is returned so callers can treat
// OpenSearch as optional.
func (c *OpenSearchClient) Search(ctx context.Context, botID, query string, topK int) ([]SearchResult, error) {
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

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	url := c.indexURL("/_search")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("search status %d: %s", resp.StatusCode, b)
	}

	var osResp osSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&osResp); err != nil {
		return nil, fmt.Errorf("decode search response: %w", err)
	}

	results := make([]SearchResult, 0, len(osResp.Hits.Hits))
	for _, hit := range osResp.Hits.Hits {
		results = append(results, SearchResult{
			Content:   hit.Source.Content,
			Category:  hit.Source.Category,
			Score:     hit.Score,
			CreatedAt: hit.Source.CreatedAt,
		})
	}
	return results, nil
}

// Delete removes a memory document by its ID.
func (c *OpenSearchClient) Delete(ctx context.Context, memoryID string) error {
	url := c.indexURL("/_doc/" + memoryID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete doc status %d: %s", resp.StatusCode, b)
	}
	return nil
}

// HybridSearchOS performs a hybrid query combining BM25 text match and kNN
// vector similarity using the OpenSearch neural-search plugin's hybrid query.
// If queryVector is nil, it falls back to the pure BM25 Search method.
// The content_vector field is excluded from the returned _source to avoid
// returning large vector payloads to callers.
func (c *OpenSearchClient) HybridSearchOS(ctx context.Context, botID, query string, queryVector []float32, topK int) ([]SearchResult, error) {
	if queryVector == nil {
		return c.Search(ctx, botID, query, topK)
	}

	// Build the hybrid query using the neural-search plugin's "hybrid" query type.
	// The two sub-queries are:
	//   1. BM25 match on "content" with the Korean analyzer.
	//   2. kNN nearest-neighbour search on "content_vector".
	reqBody := map[string]any{
		"size": topK,
		"_source": map[string]any{
			"excludes": []string{"content_vector"},
		},
		"query": map[string]any{
			"hybrid": map[string]any{
				"queries": []map[string]any{
					{
						"bool": map[string]any{
							"must": []map[string]any{
								{
									"match": map[string]any{
										"content": map[string]any{
											"query":    query,
											"analyzer": "korean",
										},
									},
								},
							},
							"filter": []map[string]any{
								{
									"term": map[string]any{
										"bot_id": botID,
									},
								},
							},
						},
					},
					{
						"knn": map[string]any{
							"content_vector": map[string]any{
								"vector": queryVector,
								"k":      topK,
							},
						},
					},
				},
			},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	url := c.indexURL("/_search")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("hybrid search status %d: %s", resp.StatusCode, b)
	}

	var osResp osSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&osResp); err != nil {
		return nil, fmt.Errorf("decode hybrid search response: %w", err)
	}

	results := make([]SearchResult, 0, len(osResp.Hits.Hits))
	for _, hit := range osResp.Hits.Hits {
		results = append(results, SearchResult{
			Content:   hit.Source.Content,
			Category:  hit.Source.Category,
			Score:     hit.Score,
			CreatedAt: hit.Source.CreatedAt,
		})
	}
	return results, nil
}

// indexURL returns the full URL for an operation on the memories alias.
// The alias resolves transparently to the underlying versioned index.
// suffix should start with "/" or be empty.
func (c *OpenSearchClient) indexURL(suffix string) string {
	return c.baseURL + "/" + AliasName + suffix
}

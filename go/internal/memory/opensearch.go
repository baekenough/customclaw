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

// IndexName is the OpenSearch index used for all memory documents.
const IndexName = "customclaw-memories"

// indexSettings is the JSON body for creating the memories index with the
// nori Korean analyzer. It mirrors the Python opensearch_client.py definition.
var indexSettings = map[string]any{
	"settings": map[string]any{
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
			"bot_id":     map[string]any{"type": "keyword"},
			"user_id":    map[string]any{"type": "keyword"},
			"category":   map[string]any{"type": "keyword"},
			"content":    map[string]any{"type": "text", "analyzer": "korean"},
			"created_at": map[string]any{"type": "date"},
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
// memories index exists. If url is empty it falls back to the
// OPENSEARCH_URL environment variable (default "http://opensearch:9200").
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

// ensureIndex creates the memories index if it does not yet exist.
func (c *OpenSearchClient) ensureIndex(ctx context.Context) error {
	// HEAD /{index} — 200 means exists, 404 means absent.
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, c.indexURL(""), nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return nil // already exists
	}
	if resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("unexpected HEAD status: %d", resp.StatusCode)
	}

	// PUT /{index} — create it.
	body, err := json.Marshal(indexSettings)
	if err != nil {
		return err
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodPut, c.indexURL(""), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err = c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("create index status %d: %s", resp.StatusCode, b)
	}
	log.Printf("opensearch: created index %s", IndexName)
	return nil
}

// IndexMemory indexes a single memory document.
// memoryID is used as the document _id for consistency with PostgreSQL.
func (c *OpenSearchClient) IndexMemory(ctx context.Context, memoryID, botID, content, category, userID string) error {
	doc := map[string]any{
		"bot_id":     botID,
		"user_id":    userID,
		"category":   category,
		"content":    content,
		"created_at": time.Now().UTC().Format(time.RFC3339),
	}
	body, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	// POST /{index}/_doc/{id}
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
	defer resp.Body.Close()
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
	Must   []osMatch  `json:"must"`
	Filter []osTerm   `json:"filter"`
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
			ID     string `json:"_id"`
			Score  float64 `json:"_score"`
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
	defer resp.Body.Close()

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
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete doc status %d: %s", resp.StatusCode, b)
	}
	return nil
}

// indexURL returns the full URL for an operation on the memories index.
// suffix should start with "/" or be empty.
func (c *OpenSearchClient) indexURL(suffix string) string {
	return c.baseURL + "/" + IndexName + suffix
}

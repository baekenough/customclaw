// Package analysis implements the GitHub issue/PR analysis pipeline.
package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	codebaseIndex = "omc-codebase"
	ragTopK       = 10
	ragMaxChunk   = 1500
)

// ragClient performs OpenSearch queries against the codebase index.
type ragClient struct {
	baseURL    string
	httpClient *http.Client
}

func newRAGClient() *ragClient {
	url := os.Getenv("OPENSEARCH_URL")
	if url == "" {
		url = "http://opensearch:9200"
	}
	url = strings.TrimRight(url, "/")
	return &ragClient{
		baseURL:    url,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// searchRelevantCode searches the omc-codebase OpenSearch index for code
// relevant to the given issue title and body.
// Returns a formatted markdown string of code snippets, or "" on failure.
func searchRelevantCode(ctx context.Context, title, body string) string {
	c := newRAGClient()

	// Limit body used for query to first 500 chars — mirrors Python behaviour.
	bodyExcerpt := body
	if len(bodyExcerpt) > 500 {
		bodyExcerpt = bodyExcerpt[:500]
	}
	queryText := title + " " + bodyExcerpt

	reqBody := map[string]any{
		"size": ragTopK,
		"query": map[string]any{
			"multi_match": map[string]any{
				"query":     queryText,
				"fields":    []string{"content", "file_path^2"},
				"type":      "best_fields",
				"fuzziness": "AUTO",
			},
		},
		"_source": []string{"file_path", "content", "chunk_index"},
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		slog.Warn("rag: marshal query failed", "error", err)
		return ""
	}

	url := fmt.Sprintf("%s/%s/_search", c.baseURL, codebaseIndex)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		slog.Warn("rag: build request failed", "error", err)
		return ""
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		slog.Warn("rag: search request failed (non-blocking)", "error", err)
		return ""
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		slog.Warn("rag: non-200 response", "status", resp.StatusCode, "body", string(b))
		return ""
	}

	var osResp struct {
		Hits struct {
			Hits []struct {
				Source struct {
					FilePath   string `json:"file_path"`
					Content    string `json:"content"`
					ChunkIndex int    `json:"chunk_index"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&osResp); err != nil {
		slog.Warn("rag: decode response failed", "error", err)
		return ""
	}

	hits := osResp.Hits.Hits
	if len(hits) == 0 {
		slog.Info("rag: search returned 0 results")
		return ""
	}

	var sections []string
	seen := make(map[string]bool)
	for _, hit := range hits {
		fp := hit.Source.FilePath
		if seen[fp] {
			continue // keep first chunk per file
		}
		seen[fp] = true

		// Shorten path for readability.
		shortPath := fp
		if idx := strings.Index(fp, "/oh-my-customcode/"); idx >= 0 {
			shortPath = fp[idx+len("/oh-my-customcode/"):]
		}

		content := hit.Source.Content
		if len(content) > ragMaxChunk {
			content = content[:ragMaxChunk] + "\n... (truncated)"
		}
		sections = append(sections, fmt.Sprintf("### %s\n```\n%s\n```", shortPath, content))
	}

	slog.Info("rag: search complete", "hits", len(hits), "unique_files", len(seen))
	return strings.Join(sections, "\n\n")
}

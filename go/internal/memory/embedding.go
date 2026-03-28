package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const (
	embeddingModel     = "text-embedding-3-small"
	embeddingDimension = 1536
	embeddingEndpoint  = "https://api.openai.com/v1/embeddings"

	// embeddingMaxChars is a safe character limit for the embedding input.
	// text-embedding-3-small supports up to 8191 tokens; 30000 chars is a
	// conservative upper bound for Korean/English mixed text.
	embeddingMaxChars = 30000
)

// embeddingRequest is the JSON body sent to the OpenAI embeddings endpoint.
type embeddingRequest struct {
	Input string `json:"input"`
	Model string `json:"model"`
}

// embeddingResponse partially decodes the OpenAI embeddings API response.
type embeddingResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// EmbeddingClient generates text embeddings via the OpenAI embeddings API.
// It is safe for concurrent use.
type EmbeddingClient struct {
	apiKey     string
	httpClient *http.Client
}

// NewEmbeddingClient creates an EmbeddingClient using the OPENAI_API_KEY
// environment variable. Returns nil if the variable is unset or empty,
// allowing callers to treat OpenAI as optional and fall back to BM25.
func NewEmbeddingClient() *EmbeddingClient {
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		return nil
	}
	return &EmbeddingClient{
		apiKey:     key,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// Embed returns the embedding vector for the given text.
// The text is truncated to embeddingMaxChars runes before the API call.
// Returns a []float32 of length embeddingDimension on success.
func (c *EmbeddingClient) Embed(ctx context.Context, text string) ([]float32, error) {
	text = truncateText(text, embeddingMaxChars)

	reqBody := embeddingRequest{
		Input: text,
		Model: embeddingModel,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal embedding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, embeddingEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create embedding request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding API call: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("embedding API status %d: %s", resp.StatusCode, b)
	}

	var embResp embeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&embResp); err != nil {
		return nil, fmt.Errorf("decode embedding response: %w", err)
	}
	if len(embResp.Data) == 0 {
		return nil, fmt.Errorf("embedding API returned empty data")
	}

	vec := embResp.Data[0].Embedding
	if len(vec) != embeddingDimension {
		return nil, fmt.Errorf("unexpected embedding dimension: got %d, want %d", len(vec), embeddingDimension)
	}
	return vec, nil
}

// truncateText truncates text to at most maxChars runes.
func truncateText(text string, maxChars int) string {
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}
	return string(runes[:maxChars])
}

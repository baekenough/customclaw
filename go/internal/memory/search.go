// Package memory provides conversation history storage and retrieval,
// including OpenSearch-backed hybrid search for Korean text.
package memory

import (
	"context"
	"log"
	"strings"
)

// SearchResult is a single memory match returned by HybridSearch.
type SearchResult struct {
	Content   string
	Category  string
	Score     float64
	CreatedAt string
}

// suffixes lists Korean verb/adjective endings that should be stripped when
// generating query variants.  Order matters: longer suffixes are listed first
// so "해주세요" is tried before "해".
var suffixes = []string{
	"해주세요",
	"해봐",
	"해줘",
	"이어서해",
	"계속해",
	"해",
	"되고",
	"되는중",
	"되는",
	"보강할건",
	"보강",
	"점검",
	"확인",
	"상태",
	"품질",
	"회수",
}

// actionKeywords are injected when the query is a bare action word.
var actionKeywords = []string{
	"해", "하자", "해줘", "해봐", "보강", "개선", "점검", "확인", "계속", "이어서",
}

// shortActionSet is the set of normalised queries that count as "short action"
// queries, triggering a PostgreSQL-first search path.
var shortActionSet = map[string]bool{
	"해":      true,
	"해줘":    true,
	"해봐":    true,
	"계속해":   true,
	"이어서해":  true,
	"계속 해":  true,
	"이어서 해": true,
}

// normalizeQuery collapses whitespace and lowercases text, matching
// Python's _normalize_query.
func normalizeQuery(text string) string {
	fields := strings.Fields(text)
	return strings.ToLower(strings.Join(fields, " "))
}

// isShortActionQuery returns true when the query is one of the well-known
// bare Korean action words that give no useful search signal on their own.
func isShortActionQuery(text string) bool {
	return shortActionSet[normalizeQuery(text)]
}

// expandQueries builds a deduplicated list of query variants from the
// normalised text, including collapsed forms, n-gram windows, suffix-stripped
// stems, and action-keyword expansions.
func expandQueries(text string) []string {
	normalized := normalizeQuery(text)
	if normalized == "" {
		return nil
	}

	variants := []string{normalized}

	collapsed := strings.ReplaceAll(normalized, " ", "")
	if collapsed != normalized {
		variants = append(variants, collapsed)
	}

	words := strings.Fields(normalized)
	if len(words) > 1 {
		variants = append(variants, strings.Join(words[len(words)-2:], " "))
		variants = append(variants, strings.Join(words[:2], " "))
		if len(words) > 2 {
			variants = append(variants, strings.Join(words[len(words)-3:], " "))
		}
	}

	for _, suffix := range suffixes {
		if strings.HasSuffix(normalized, suffix) {
			stem := strings.TrimSpace(normalized[:len(normalized)-len(suffix)])
			if len([]rune(stem)) >= 2 {
				variants = append(variants, stem)
			}
		}
	}

	// Bare action words → inject full keyword list.
	bareActions := map[string]bool{
		"해": true, "해줘": true, "해봐": true, "계속해": true, "이어서해": true,
	}
	if bareActions[normalized] {
		variants = append(variants, actionKeywords...)
	}

	// Boost queries containing action-improvement keywords.
	for _, kw := range []string{"보강", "개선", "점검", "확인"} {
		if strings.Contains(normalized, kw) {
			variants = append(variants, "계속", "이어서", "먼저 말", "기다리지 말고")
			break
		}
	}

	// Deduplicate while preserving order.
	seen := make(map[string]bool, len(variants))
	deduped := make([]string, 0, len(variants))
	for _, v := range variants {
		cleaned := normalizeQuery(v)
		runes := []rune(cleaned)
		// Allow the single rune "해" as a special case; otherwise require ≥2 runes.
		if (len(runes) < 2 && cleaned != "해") || seen[cleaned] {
			continue
		}
		seen[cleaned] = true
		deduped = append(deduped, cleaned)
	}
	return deduped
}

// HybridSearch combines OpenSearch hybrid search (BM25 + kNN vector) with a
// PostgreSQL fallback. osClient may be nil when OpenSearch is unavailable;
// the search then falls back entirely to PostgreSQL. embedder may be nil when
// OPENAI_API_KEY is unset; search then uses BM25 only.
type HybridSearch struct {
	osClient *OpenSearchClient // nil if OpenSearch is unavailable
	store    *MessageStore
	embedder *EmbeddingClient // nil when OpenAI unavailable; BM25-only fallback
}

// NewHybridSearch creates a HybridSearch. It attempts to connect to OpenSearch
// at opensearchURL; if that fails it logs a warning and continues without it.
// If opensearchURL is empty, the OPENSEARCH_URL env var is consulted
// (default "http://opensearch:9200"). An EmbeddingClient is created from
// OPENAI_API_KEY; if the key is absent, hybrid search falls back to BM25.
func NewHybridSearch(store *MessageStore, opensearchURL string) *HybridSearch {
	h := &HybridSearch{
		store:    store,
		embedder: NewEmbeddingClient(),
	}
	client, err := NewOpenSearchClient(opensearchURL)
	if err != nil {
		log.Printf("opensearch client init failed (search will use PostgreSQL fallback): %v", err)
		return h
	}
	h.osClient = client
	return h
}

// recentMessagesLookback is the number of recent messages to scan in the
// PostgreSQL fallback (mirrors Python's lookback=200 default).
const recentMessagesLookback = 200

// DeleteByPlatformMsgID deletes memories associated with a platform message.
// Currently a no-op stub — requires a source_message_id foreign key in the
// memories table to locate which memory rows to remove.
func (h *HybridSearch) DeleteByPlatformMsgID(ctx context.Context, botID, platformMsgID string) error {
	log.Printf("opensearch cascade: not yet implemented (requires memories.source_message_id) bot_id=%s platform_msg_id=%s",
		botID, platformMsgID)
	return nil
}

// Search returns up to topK memory entries relevant to queryText for the
// given bot.  The algorithm mirrors the Python HybridSearch.search:
//
//  1. Short action query → PostgreSQL memories + recent messages with boosted scores.
//  2. OpenSearch available → search each expanded query variant.
//  3. Fallback → PostgreSQL keyword search.
//  4. Still short → recent message search to cover extraction lag.
//  5. Sort by score descending, return top topK.
func (h *HybridSearch) Search(ctx context.Context, botID, channelID, queryText string, topK int) ([]SearchResult, error) {
	var results []SearchResult
	seen := make(map[[2]string]bool) // dedup by [category, content]

	queries := expandQueries(queryText)
	if len(queries) == 0 {
		queries = []string{queryText}
	}
	isShortAction := isShortActionQuery(queryText)

	// --- Step 1: short action query path ---
	if isShortAction {
		if items, err := h.store.SearchMemories(ctx, botID, queryText, topK*2); err == nil {
			for _, item := range items {
				if item.Category == "preference" {
					item.Score += 4.0
				}
				key := [2]string{item.Category, item.Content}
				if seen[key] {
					continue
				}
				seen[key] = true
				results = append(results, item)
			}
		}

		recentLimit := topK
		if recentLimit < 1 {
			recentLimit = 1
		}
		if items, err := h.store.SearchRecentMessages(ctx, botID, channelID, queryText, recentLimit, recentMessagesLookback); err == nil {
			for _, item := range items {
				if item.Category == "recent_context" {
					item.Score += 2.0
				}
				key := [2]string{item.Category, item.Content}
				if seen[key] {
					continue
				}
				seen[key] = true
				results = append(results, item)
			}
		}

		sortByScore(results)
		minNeeded := topK
		if minNeeded > 3 {
			minNeeded = 3
		}
		if len(results) >= minNeeded {
			return truncate(results, topK), nil
		}
	}

	// --- Step 2: OpenSearch hybrid search (BM25 + kNN when embedder available) ---
	if h.osClient != nil {
		// Attempt to embed the original query text for vector similarity.
		// Use the first (most representative) query variant for embedding.
		var queryVector []float32
		if h.embedder != nil {
			vec, err := h.embedder.Embed(ctx, queryText)
			if err != nil {
				log.Printf("embedding failed, falling back to BM25: %v", err)
			} else {
				queryVector = vec
			}
		}

		if queryVector != nil {
			// Hybrid search: single call combines BM25 + kNN ranking.
			hits, err := h.osClient.HybridSearchOS(ctx, botID, queries[0], queryVector, topK)
			if err != nil {
				log.Printf("opensearch hybrid search failed: %v", err)
			} else {
				for _, item := range hits {
					key := [2]string{item.Category, item.Content}
					if seen[key] {
						continue
					}
					seen[key] = true
					results = append(results, item)
				}
			}
		} else {
			// BM25-only fallback: iterate over all query variants.
			for _, q := range queries {
				hits, err := h.osClient.Search(ctx, botID, q, topK)
				if err != nil {
					log.Printf("opensearch memory search failed: %v", err)
					break
				}
				for _, item := range hits {
					key := [2]string{item.Category, item.Content}
					if seen[key] {
						continue
					}
					seen[key] = true
					results = append(results, item)
				}
			}
		}
	}

	// --- Step 3: PostgreSQL fallback ---
	if len(results) < topK {
		for _, q := range queries {
			items, err := h.store.SearchMemories(ctx, botID, q, topK)
			if err != nil {
				break
			}
			for _, item := range items {
				key := [2]string{item.Category, item.Content}
				if seen[key] {
					continue
				}
				seen[key] = true
				results = append(results, item)
				if len(results) >= topK {
					break
				}
			}
			if len(results) >= topK {
				break
			}
		}
	}

	memoryCount := len(results)

	// --- Step 4: recent messages when still short ---
	minNeeded := topK
	if minNeeded > 3 {
		minNeeded = 3
	}
	if memoryCount < minNeeded {
		for _, q := range queries {
			remaining := topK - len(results)
			if remaining < 1 {
				remaining = 1
			}
			items, err := h.store.SearchRecentMessages(ctx, botID, channelID, q, remaining, recentMessagesLookback)
			if err != nil {
				break
			}
			for _, item := range items {
				key := [2]string{item.Category, item.Content}
				if seen[key] {
					continue
				}
				seen[key] = true
				results = append(results, item)
				if len(results) >= topK {
					break
				}
			}
			if len(results) >= topK {
				break
			}
		}
	}

	sortByScore(results)
	return truncate(results, topK), nil
}

// sortByScore sorts results descending by Score in place using insertion sort,
// which is efficient for the small slices expected (topK ≤ ~20).
func sortByScore(results []SearchResult) {
	for i := 1; i < len(results); i++ {
		key := results[i]
		j := i - 1
		for j >= 0 && results[j].Score < key.Score {
			results[j+1] = results[j]
			j--
		}
		results[j+1] = key
	}
}

// truncate returns the first n elements of s, or all of s if len(s) ≤ n.
func truncate(s []SearchResult, n int) []SearchResult {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

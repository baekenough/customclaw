package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	l1CacheTTL        = 5 * time.Minute
	l1CacheMaxEntries = 1000
	l2CacheTTL        = 15 * time.Minute
	l2MaxPerBot       = 50
	// l2SimilarityThreshold is the minimum cosine similarity for an L2 cache hit.
	l2SimilarityThreshold = 0.95
)

// SearchCache provides multi-tier caching for search results.
//
// L1 is an in-memory exact-match cache (no network round-trip, 5 min TTL).
// L2 is a Redis-backed semantic cache that compares query embeddings by
// cosine similarity (15 min TTL, 0.95 threshold).
//
// Both tiers degrade gracefully: L1 is never nil; L2 is disabled when rdb is
// nil or when a query has no embedding vector.
type SearchCache struct {
	// L1: exact hash cache
	mu      sync.RWMutex
	entries map[string]*l1Entry
	order   []string // insertion-order key slice for LRU eviction

	// L2: semantic cache via Redis (nil when unavailable)
	rdb *redis.Client
}

type l1Entry struct {
	botID    string
	results  []SearchResult
	cachedAt time.Time
}

// NewSearchCache creates a SearchCache. rdb may be nil to disable L2.
func NewSearchCache(rdb *redis.Client) *SearchCache {
	return &SearchCache{
		entries: make(map[string]*l1Entry),
		rdb:     rdb,
	}
}

// l1Key returns the L1 cache key for the (botID, query) pair.
// The query is normalised before hashing so whitespace variants share a key.
func l1Key(botID, query string) string {
	h := sha256.Sum256([]byte(botID + "|" + normalizeQuery(query)))
	return hex.EncodeToString(h[:])
}

// GetL1 checks the L1 exact-match cache.
// Returns the cached results and true on a live hit; nil and false otherwise.
func (c *SearchCache) GetL1(botID, query string) ([]SearchResult, bool) {
	key := l1Key(botID, query)
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok || time.Since(entry.cachedAt) > l1CacheTTL {
		return nil, false
	}
	return entry.results, true
}

// PutL1 stores results in the L1 cache.
// When the cache is at capacity, expired entries are pruned first; if still
// over the limit, the oldest insertion-order entry is evicted (simple LRU).
func (c *SearchCache) PutL1(botID, query string, results []SearchResult) {
	key := l1Key(botID, query)
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.entries[key]; !exists {
		// Only enforce capacity for new keys.
		if len(c.entries) >= l1CacheMaxEntries {
			c.evictOldestLocked()
		}
		c.order = append(c.order, key)
	}
	c.entries[key] = &l1Entry{botID: botID, results: results, cachedAt: time.Now()}
}

// evictOldestLocked removes expired entries then, if still over capacity,
// removes the oldest entry by insertion order. Must be called with c.mu held.
func (c *SearchCache) evictOldestLocked() {
	now := time.Now()
	for k, v := range c.entries {
		if now.Sub(v.cachedAt) > l1CacheTTL {
			delete(c.entries, k)
		}
	}
	// Rebuild order slice to drop keys that were just expired-out.
	n := 0
	for _, k := range c.order {
		if _, ok := c.entries[k]; ok {
			c.order[n] = k
			n++
		}
	}
	c.order = c.order[:n]

	// Still over limit: remove the oldest by insertion order.
	for len(c.entries) >= l1CacheMaxEntries && len(c.order) > 0 {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}
}

// l2CacheEntry is the value stored in a Redis hash field.
type l2CacheEntry struct {
	Vector   []float32      `json:"v"`
	Results  []SearchResult `json:"r"`
	CachedAt time.Time      `json:"t"`
}

// GetL2 checks the Redis semantic cache.
//
// It reads all cached entries for the bot, computes cosine similarity between
// queryVector and each stored vector, and returns the result set of the best
// match that exceeds l2SimilarityThreshold.
//
// Returns nil and false when Redis is unavailable, the query has no vector,
// or no sufficiently similar entry is found.  L2 failures are logged at Debug
// level and never propagate errors to the caller.
func (c *SearchCache) GetL2(ctx context.Context, botID string, queryVector []float32) ([]SearchResult, bool) {
	if c.rdb == nil || len(queryVector) == 0 {
		return nil, false
	}

	hashKey := l2HashKey(botID)
	entries, err := c.rdb.HGetAll(ctx, hashKey).Result()
	if err != nil {
		slog.Debug("search cache L2 get failed", "bot", botID, "error", err)
		return nil, false
	}
	if len(entries) == 0 {
		return nil, false
	}

	now := time.Now()
	var bestResults []SearchResult
	bestSim := float64(0)

	for _, val := range entries {
		var cached l2CacheEntry
		if err := json.Unmarshal([]byte(val), &cached); err != nil {
			continue
		}
		if now.Sub(cached.CachedAt) > l2CacheTTL {
			continue
		}
		sim := cosineSimilarity(queryVector, cached.Vector)
		if sim > l2SimilarityThreshold && sim > bestSim {
			bestSim = sim
			bestResults = cached.Results
		}
	}

	if bestResults != nil {
		return bestResults, true
	}
	return nil, false
}

// PutL2 stores the query embedding and results in the Redis semantic cache.
//
// The hash key is per-bot; each field is keyed by a hash of the first 8
// embedding dimensions (cheap, collision-tolerant field identity).  After
// writing, excess entries are pruned to l2MaxPerBot by removing the first
// (arbitrarily ordered) fields returned by HKEYS.
//
// Failures are silently dropped; L2 is advisory and must never block search.
func (c *SearchCache) PutL2(ctx context.Context, botID string, queryVector []float32, results []SearchResult) {
	if c.rdb == nil || len(queryVector) == 0 {
		return
	}

	entry := l2CacheEntry{
		Vector:   queryVector,
		Results:  results,
		CachedAt: time.Now(),
	}
	data, err := json.Marshal(entry)
	if err != nil {
		slog.Debug("search cache L2 marshal failed", "bot", botID, "error", err)
		return
	}

	hashKey := l2HashKey(botID)
	fieldKey := l2FieldKey(queryVector)

	if err := c.rdb.HSet(ctx, hashKey, fieldKey, data).Err(); err != nil {
		slog.Debug("search cache L2 hset failed", "bot", botID, "error", err)
		return
	}
	// Refresh TTL on every write so the hash lives as long as it is active.
	_ = c.rdb.Expire(ctx, hashKey, l2CacheTTL).Err()

	// Enforce per-bot entry cap.
	count, err := c.rdb.HLen(ctx, hashKey).Result()
	if err != nil {
		return
	}
	if count > int64(l2MaxPerBot) {
		fields, err := c.rdb.HKeys(ctx, hashKey).Result()
		if err != nil {
			return
		}
		excess := int(count) - l2MaxPerBot
		if excess > 0 && len(fields) >= excess {
			_ = c.rdb.HDel(ctx, hashKey, fields[:excess]...).Err()
		}
	}
}

// l2HashKey returns the Redis hash key for a bot's semantic cache.
func l2HashKey(botID string) string {
	return fmt.Sprintf("search_cache:%s", botID)
}

// l2FieldKey returns a stable field name derived from the first 8 dimensions
// of the embedding vector.  Using a hash avoids storing raw float strings as
// field names while providing sufficient field identity for deduplication.
func l2FieldKey(vec []float32) string {
	n := 8
	if len(vec) < n {
		n = len(vec)
	}
	h := sha256.New()
	for _, f := range vec[:n] {
		// Write the 4-byte IEEE 754 representation of each float.
		b := [4]byte{
			byte(math.Float32bits(f)),
			byte(math.Float32bits(f) >> 8),
			byte(math.Float32bits(f) >> 16),
			byte(math.Float32bits(f) >> 24),
		}
		_, _ = h.Write(b[:])
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// InvalidateBot removes all L1 and L2 cache entries for the given bot.
// Called after delete/edit cascade so stale search results are not served.
// L1 entries store botID for selective eviction; L2 issues a single Redis DEL.
// Failures are best-effort — logged at Debug level, never returned to caller.
func (c *SearchCache) InvalidateBot(ctx context.Context, botID string) {
	c.mu.Lock()
	n := 0
	for _, k := range c.order {
		entry, ok := c.entries[k]
		if ok && entry.botID == botID {
			delete(c.entries, k)
		} else if ok {
			c.order[n] = k
			n++
		}
	}
	c.order = c.order[:n]
	c.mu.Unlock()

	// L2: delete the bot's Redis hash key in one round-trip.
	if c.rdb != nil {
		hashKey := l2HashKey(botID)
		if err := c.rdb.Del(ctx, hashKey).Err(); err != nil {
			slog.Debug("search cache L2 invalidate failed", "bot", botID, "error", err)
		}
	}
}

// cosineSimilarity returns the cosine similarity between two float32 vectors.
// Returns 0 for empty or mismatched-length inputs.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		fa := float64(a[i])
		fb := float64(b[i])
		dot += fa * fb
		normA += fa * fa
		normB += fb * fb
	}
	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return dot / denom
}

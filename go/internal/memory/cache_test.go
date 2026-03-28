package memory

import (
	"context"
	"math"
	"testing"
	"time"
)

// ─── cosineSimilarity ────────────────────────────────────────────────────────

func TestCosineSimilarity_identicalVectors(t *testing.T) {
	v := []float32{1, 2, 3, 4}
	got := cosineSimilarity(v, v)
	if math.Abs(got-1.0) > 1e-6 {
		t.Errorf("cosineSimilarity identical: got %f, want 1.0", got)
	}
}

func TestCosineSimilarity_orthogonalVectors(t *testing.T) {
	a := []float32{1, 0}
	b := []float32{0, 1}
	got := cosineSimilarity(a, b)
	if math.Abs(got) > 1e-6 {
		t.Errorf("cosineSimilarity orthogonal: got %f, want 0.0", got)
	}
}

func TestCosineSimilarity_sameDirection(t *testing.T) {
	a := []float32{1, 2, 3}
	b := []float32{2, 4, 6} // same direction, different magnitude
	got := cosineSimilarity(a, b)
	if math.Abs(got-1.0) > 1e-5 {
		t.Errorf("cosineSimilarity same direction: got %f, want ~1.0", got)
	}
}

// ─── cosineSimilarity edge cases ─────────────────────────────────────────────

func TestCosineSimilarityEdgeCases_emptyVectors(t *testing.T) {
	got := cosineSimilarity([]float32{}, []float32{})
	if got != 0 {
		t.Errorf("cosineSimilarity empty: got %f, want 0", got)
	}
}

func TestCosineSimilarityEdgeCases_differentLengths(t *testing.T) {
	a := []float32{1, 2, 3}
	b := []float32{1, 2}
	got := cosineSimilarity(a, b)
	if got != 0 {
		t.Errorf("cosineSimilarity different lengths: got %f, want 0", got)
	}
}

// ─── L1 cache ────────────────────────────────────────────────────────────────

func TestL1CacheHitMiss(t *testing.T) {
	c := NewSearchCache(nil)
	results := []SearchResult{{Content: "hello", Score: 1.0}}

	// Miss before put.
	if _, ok := c.GetL1("bot1", "query"); ok {
		t.Error("GetL1: expected miss before put, got hit")
	}

	c.PutL1("bot1", "query", results)

	// Hit after put.
	got, ok := c.GetL1("bot1", "query")
	if !ok {
		t.Error("GetL1: expected hit after put, got miss")
	}
	if len(got) != 1 || got[0].Content != "hello" {
		t.Errorf("GetL1: unexpected results %+v", got)
	}

	// Different bot — should still miss.
	if _, ok := c.GetL1("bot2", "query"); ok {
		t.Error("GetL1: different bot should miss")
	}
}

func TestL1CacheHitMiss_afterTTLExpiry(t *testing.T) {
	c := NewSearchCache(nil)
	results := []SearchResult{{Content: "stale"}}
	c.PutL1("bot1", "query", results)

	// Manually backdate the entry to simulate TTL expiry.
	key := l1Key("bot1", "query")
	c.mu.Lock()
	c.entries[key].cachedAt = time.Now().Add(-(l1CacheTTL + time.Second))
	c.mu.Unlock()

	if _, ok := c.GetL1("bot1", "query"); ok {
		t.Error("GetL1: expected miss after TTL expiry, got hit")
	}
}

func TestL1CacheKeyNormalization(t *testing.T) {
	c := NewSearchCache(nil)
	results := []SearchResult{{Content: "norm"}}

	// Put with extra whitespace.
	c.PutL1("bot1", "  hello  world  ", results)

	// Get with collapsed whitespace — should be the same key.
	got, ok := c.GetL1("bot1", "hello world")
	if !ok {
		t.Error("GetL1: expected hit after normalized put, got miss")
	}
	if len(got) != 1 || got[0].Content != "norm" {
		t.Errorf("GetL1 normalization: unexpected results %+v", got)
	}
}

func TestL1CacheEviction(t *testing.T) {
	c := NewSearchCache(nil)
	results := []SearchResult{{Content: "x"}}

	// Fill to capacity.
	for i := range l1CacheMaxEntries {
		c.PutL1("bot1", string(rune('a'+i%26))+string(rune(i)), results)
	}

	if len(c.entries) > l1CacheMaxEntries {
		t.Errorf("L1 eviction: cache size %d exceeds max %d", len(c.entries), l1CacheMaxEntries)
	}

	// Add one more — should still not exceed max.
	c.PutL1("bot1", "overflow", results)
	if len(c.entries) > l1CacheMaxEntries {
		t.Errorf("L1 eviction after overflow: cache size %d exceeds max %d", len(c.entries), l1CacheMaxEntries)
	}
}

// ─── L2 cache (nil Redis) ────────────────────────────────────────────────────

func TestL2CacheNilRedis(t *testing.T) {
	c := NewSearchCache(nil)
	vec := []float32{0.1, 0.2, 0.3}

	// GetL2 must return false gracefully.
	_, ok := c.GetL2(context.Background(), "bot1", vec)
	if ok {
		t.Error("GetL2 with nil Redis: expected false, got true")
	}

	// PutL2 must not panic.
	c.PutL2(context.Background(), "bot1", vec, []SearchResult{{Content: "x"}})
}

// ─── constructor ─────────────────────────────────────────────────────────────

func TestSearchCacheNewWithNilRedis(t *testing.T) {
	c := NewSearchCache(nil)
	if c == nil {
		t.Fatal("NewSearchCache(nil) returned nil")
	}
	if c.entries == nil {
		t.Error("NewSearchCache: entries map not initialised")
	}
	if c.rdb != nil {
		t.Error("NewSearchCache(nil): expected nil rdb")
	}
}

// ─── InvalidateBot ───────────────────────────────────────────────────────────

func TestInvalidateBot_clearsL1(t *testing.T) {
	c := NewSearchCache(nil) // no Redis
	results := []SearchResult{{Content: "hello", Score: 1.0}}

	// Populate L1 cache for two bots.
	c.PutL1("bot1", "query-a", results)
	c.PutL1("bot1", "query-b", results)
	c.PutL1("bot2", "query-c", results)

	// Invalidate bot1 only.
	c.InvalidateBot(context.Background(), "bot1")

	// bot1 entries should be gone.
	if _, ok := c.GetL1("bot1", "query-a"); ok {
		t.Error("expected bot1 query-a to be invalidated")
	}
	if _, ok := c.GetL1("bot1", "query-b"); ok {
		t.Error("expected bot1 query-b to be invalidated")
	}
	// bot2 entries should survive.
	if _, ok := c.GetL1("bot2", "query-c"); !ok {
		t.Error("expected bot2 query-c to survive, but it was invalidated")
	}
}

func TestInvalidateBot_nilRedis(t *testing.T) {
	c := NewSearchCache(nil)
	// Should not panic with nil Redis.
	c.InvalidateBot(context.Background(), "bot1")
}

func TestInvalidateBot_emptyCache(t *testing.T) {
	c := NewSearchCache(nil)
	// Should succeed on an empty cache without panic.
	c.InvalidateBot(context.Background(), "bot-does-not-exist")
	if len(c.entries) != 0 {
		t.Errorf("entries map should remain empty, got %d entries", len(c.entries))
	}
}

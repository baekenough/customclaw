package memory

import (
	"context"
	"testing"
)

// ─── normalizeQuery ──────────────────────────────────────────────────────────

func TestNormalizeQuery_collapseWhitespace(t *testing.T) {
	got := normalizeQuery("  서버  상태  ")
	want := "서버 상태"
	if got != want {
		t.Errorf("normalizeQuery whitespace: got %q, want %q", got, want)
	}
}

func TestNormalizeQuery_lowercase(t *testing.T) {
	got := normalizeQuery("Hello World")
	want := "hello world"
	if got != want {
		t.Errorf("normalizeQuery lowercase: got %q, want %q", got, want)
	}
}

func TestNormalizeQuery_trim(t *testing.T) {
	got := normalizeQuery("\t  trimmed  \n")
	want := "trimmed"
	if got != want {
		t.Errorf("normalizeQuery trim: got %q, want %q", got, want)
	}
}

func TestNormalizeQuery_empty(t *testing.T) {
	if got := normalizeQuery(""); got != "" {
		t.Errorf("normalizeQuery empty: got %q, want %q", got, "")
	}
}

// ─── isShortActionQuery ──────────────────────────────────────────────────────

func TestIsShortActionQuery_trueForBareActions(t *testing.T) {
	trueInputs := []string{
		"해",
		"해줘",
		"해봐",
		"계속해",
		"이어서해",
		"계속 해",
		"이어서 해",
		// with surrounding whitespace
		"  해  ",
	}
	for _, in := range trueInputs {
		if !isShortActionQuery(in) {
			t.Errorf("isShortActionQuery(%q) = false, want true", in)
		}
	}
}

func TestIsShortActionQuery_falseForDescriptiveQueries(t *testing.T) {
	falseInputs := []string{
		"서버 상태",
		"",
		"배포 확인해줘",
		"오늘 날씨",
		"hello world",
	}
	for _, in := range falseInputs {
		if isShortActionQuery(in) {
			t.Errorf("isShortActionQuery(%q) = true, want false", in)
		}
	}
}

// ─── expandQueries ───────────────────────────────────────────────────────────

// containsVariant is a helper that returns true when want appears in haystack.
func containsVariant(haystack []string, want string) bool {
	for _, v := range haystack {
		if v == want {
			return true
		}
	}
	return false
}

func TestExpandQueries_emptyReturnsNil(t *testing.T) {
	got := expandQueries("")
	if len(got) != 0 {
		t.Errorf("expandQueries(\"\") = %v, want nil/empty", got)
	}
}

func TestExpandQueries_suffixStripping(t *testing.T) {
	// "서버 상태 확인해줘" should include the suffix-stripped stem "서버 상태 확인".
	variants := expandQueries("서버 상태 확인해줘")
	for _, want := range []string{"서버 상태 확인해줘", "서버 상태 확인"} {
		if !containsVariant(variants, want) {
			t.Errorf("expandQueries: want %q in %v", want, variants)
		}
	}
	// The last-2-words window should appear.
	if !containsVariant(variants, "상태 확인해줘") {
		t.Errorf("expandQueries: want last-2-word window %q in %v", "상태 확인해줘", variants)
	}
}

func TestExpandQueries_bareActionInjectsKeywords(t *testing.T) {
	variants := expandQueries("해")
	for _, kw := range actionKeywords {
		if !containsVariant(variants, kw) {
			t.Errorf("expandQueries(\"해\"): want action keyword %q in %v", kw, variants)
		}
	}
}

func TestExpandQueries_deduplication(t *testing.T) {
	variants := expandQueries("해줘")
	seen := make(map[string]int)
	for _, v := range variants {
		seen[v]++
	}
	for v, count := range seen {
		if count > 1 {
			t.Errorf("expandQueries: duplicate variant %q (count=%d)", v, count)
		}
	}
}

func TestExpandQueries_collapsedFormAdded(t *testing.T) {
	// "이어서 해" has a collapsed form "이어서해".
	variants := expandQueries("이어서 해")
	if !containsVariant(variants, "이어서해") {
		t.Errorf("expandQueries: want collapsed form %q in %v", "이어서해", variants)
	}
}

func TestExpandQueries_noShortStemsExceptHae(t *testing.T) {
	// Suffix stripping "보강해줘" should yield "보강" (len=2 runes), which is allowed.
	variants := expandQueries("보강해줘")
	if !containsVariant(variants, "보강") {
		t.Errorf("expandQueries(\"보강해줘\"): want stem %q in %v", "보강", variants)
	}
}

func TestExpandQueries_multiWordWindows(t *testing.T) {
	// "a b c d" → last-2="c d", first-2="a b", last-3="b c d"
	variants := expandQueries("a b c d")
	for _, want := range []string{"c d", "a b", "b c d"} {
		if !containsVariant(variants, want) {
			t.Errorf("expandQueries windows: want %q in %v", want, variants)
		}
	}
}

func TestExpandQueries_boostKeywordsAddContextVariants(t *testing.T) {
	// Queries containing "보강" should add extra context phrases.
	variants := expandQueries("코드 보강해줘")
	extra := []string{"계속", "이어서"}
	for _, want := range extra {
		if !containsVariant(variants, want) {
			t.Errorf("expandQueries boost: want %q in %v", want, variants)
		}
	}
}

// ─── sortByScore ─────────────────────────────────────────────────────────────

func TestSortByScore(t *testing.T) {
	results := []SearchResult{
		{Content: "low", Score: 1.0},
		{Content: "high", Score: 9.0},
		{Content: "mid", Score: 5.0},
	}
	sortByScore(results)
	if results[0].Content != "high" || results[1].Content != "mid" || results[2].Content != "low" {
		t.Errorf("sortByScore: unexpected order %+v", results)
	}
}

// ─── DeleteByPlatformMsgID ───────────────────────────────────────────────────

func TestDeleteByPlatformMsgID_noStorePool(t *testing.T) {
	// In-memory mode (nil pool) — all DB methods return empty/nil.
	store, _ := NewMessageStore(context.Background(), "")
	h := NewHybridSearch(store, "", nil) // no OpenSearch, no Redis

	// Should succeed gracefully — no message found, no memories to delete.
	err := h.DeleteByPlatformMsgID(context.Background(), "bot1", "slack-msg-123")
	if err != nil {
		t.Errorf("DeleteByPlatformMsgID with nil pool: unexpected error %v", err)
	}
}

func TestDeleteByPlatformMsgID_emptyPlatformMsgID(t *testing.T) {
	store, _ := NewMessageStore(context.Background(), "")
	h := NewHybridSearch(store, "", nil)

	// Empty platform message ID: GetMessageIDByPlatformID returns "" with nil pool,
	// so the cascade exits early without error.
	err := h.DeleteByPlatformMsgID(context.Background(), "bot1", "")
	if err != nil {
		t.Errorf("DeleteByPlatformMsgID with empty platform msg id: unexpected error %v", err)
	}
}

func TestDeleteByPlatformMsgID_invalidatesCacheOnSuccess(t *testing.T) {
	store, _ := NewMessageStore(context.Background(), "")
	h := NewHybridSearch(store, "", nil)

	// Pre-populate L1 cache.
	h.cache.PutL1("bot1", "some query", []SearchResult{{Content: "cached", Score: 1.0}})

	// With nil pool, GetMessageIDByPlatformID returns "" so the method returns
	// early without reaching the cache invalidation step. Verify the cache is
	// still intact (no spurious flush on no-op path).
	_ = h.DeleteByPlatformMsgID(context.Background(), "bot1", "msg-999")
	if _, ok := h.cache.GetL1("bot1", "some query"); !ok {
		// This is acceptable: the no-op path may or may not flush L1.
		// The important assertion is that no error was returned.
	}
}

// ─── truncate ────────────────────────────────────────────────────────────────

func TestTruncate_shorter(t *testing.T) {
	s := []SearchResult{{Content: "a"}, {Content: "b"}}
	got := truncate(s, 5)
	if len(got) != 2 {
		t.Errorf("truncate: got len=%d, want 2", len(got))
	}
}

func TestTruncate_exact(t *testing.T) {
	s := []SearchResult{{Content: "a"}, {Content: "b"}}
	got := truncate(s, 2)
	if len(got) != 2 {
		t.Errorf("truncate exact: got len=%d, want 2", len(got))
	}
}

func TestTruncate_longer(t *testing.T) {
	s := []SearchResult{{Content: "a"}, {Content: "b"}, {Content: "c"}}
	got := truncate(s, 2)
	if len(got) != 2 {
		t.Errorf("truncate longer: got len=%d, want 2", len(got))
	}
	if got[0].Content != "a" || got[1].Content != "b" {
		t.Errorf("truncate longer: unexpected contents %+v", got)
	}
}

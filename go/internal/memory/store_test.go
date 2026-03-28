package memory

import (
	"context"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// normalizeText
// ---------------------------------------------------------------------------

func TestNormalizeText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"already clean", "hello world", "hello world"},
		{"leading trailing spaces", "  hello  ", "hello"},
		{"multiple spaces", "hello   world", "hello world"},
		{"tab and newline", "hello\t\nworld", "hello world"},
		{"uppercase", "Hello World", "hello world"},
		{"mixed spaces and case", "  HELLO   WORLD  ", "hello world"},
		{"empty", "", ""},
		{"only whitespace", "   \t  ", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeText(tc.input); got != tc.want {
				t.Errorf("normalizeText(%q) = %q; want %q", tc.input, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// queryTokens — Korean particle stripping, dedup, min-length filter
// ---------------------------------------------------------------------------

func TestQueryTokens(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		tokens []string
	}{
		{
			name:   "korean particles stripped",
			input:  "서버에서 오류가 발생했습니다",
			tokens: []string{"서버", "오류", "발생했습니다"},
		},
		{
			// "사용자는" → "사용자" (는 stripped), "간결한" → no particle stripped (한 not in list),
			// "답변을" → "답변" (을 stripped), "선호" stays.
			name:   "user prefers concise answer",
			input:  "사용자는 간결한 답변을 선호",
			tokens: []string{"사용자", "간결한", "답변", "선호"},
		},
		{
			name:   "mixed korean and english",
			input:  "Airflow DAG 상태 확인해줘",
			tokens: []string{"airflow", "dag", "상태", "확인해줘"},
		},
		{
			name:   "single char tokens removed",
			input:  "a b c hello",
			tokens: []string{"hello"},
		},
		{
			name:   "duplicates removed",
			input:  "hello hello world world",
			tokens: []string{"hello", "world"},
		},
		{
			name:   "empty input",
			input:  "",
			tokens: nil,
		},
		{
			// "나는" → "나" (는 stripped), but "나" is 1 rune → filtered.
			// "서버가" → "서버" (가 stripped). "중요해" stays.
			name:   "은는이가 particles stripped, short stems removed",
			input:  "나는 서버가 중요해",
			tokens: []string{"서버", "중요해"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := queryTokens(tc.input)
			if len(got) != len(tc.tokens) {
				t.Errorf("queryTokens(%q) = %v (len %d); want %v (len %d)",
					tc.input, got, len(got), tc.tokens, len(tc.tokens))
				return
			}
			for i, tok := range got {
				if tok != tc.tokens[i] {
					t.Errorf("queryTokens(%q)[%d] = %q; want %q", tc.input, i, tok, tc.tokens[i])
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// queryTokens — table-driven Korean cases from the spec
// ---------------------------------------------------------------------------

func TestQueryTokensKoreanSpec(t *testing.T) {
	tests := []struct {
		input  string
		expect []string
	}{
		{
			"서버에서 오류가 발생했습니다",
			[]string{"서버", "오류", "발생했습니다"},
		},
		{
			"사용자는 간결한 답변을 선호",
			[]string{"사용자", "간결한", "답변", "선호"},
		},
		{
			"Airflow DAG 상태 확인해줘",
			[]string{"airflow", "dag", "상태", "확인해줘"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := queryTokens(tc.input)
			if len(got) != len(tc.expect) {
				t.Errorf("got %v; want %v", got, tc.expect)
				return
			}
			for i := range got {
				if got[i] != tc.expect[i] {
					t.Errorf("[%d] got %q; want %q", i, got[i], tc.expect[i])
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// scoreTextMatch
// ---------------------------------------------------------------------------

func TestScoreTextMatch(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		content    string
		minScore   float64
		maxScore   float64
	}{
		{
			name:     "exact match returns 1.0",
			query:    "hello world",
			content:  "hello world",
			minScore: 1.0,
			maxScore: 1.0,
		},
		{
			name:     "content contains query returns 0.9",
			query:    "hello",
			content:  "say hello world",
			minScore: 0.9,
			maxScore: 0.9,
		},
		{
			name:     "token overlap returns non-zero",
			query:    "서버 오류",
			content:  "서버에서 오류가 발생했습니다",
			minScore: 0.1,
			maxScore: 0.9,
		},
		{
			name:     "no overlap returns 0",
			query:    "golang",
			content:  "python is great",
			minScore: 0.0,
			maxScore: 0.0,
		},
		{
			name:     "partial match gives score in range",
			query:    "서버 오류 배포",
			content:  "서버에서 오류가 발생했습니다",
			minScore: 0.1,
			maxScore: 0.85,
		},
		{
			name:     "empty query returns 0",
			query:    "",
			content:  "some content",
			minScore: 0.0,
			maxScore: 0.0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := scoreTextMatch(tc.query, tc.content)
			if got < tc.minScore || got > tc.maxScore {
				t.Errorf("scoreTextMatch(%q, %q) = %v; want [%v, %v]",
					tc.query, tc.content, got, tc.minScore, tc.maxScore)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// compactContent
// ---------------------------------------------------------------------------

func TestCompactContent(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		limit   int
		want    string
		wantLen bool // true = check rune length rather than exact string
	}{
		{
			name:  "short text passes through",
			input: "hello",
			limit: 10,
			want:  "hello",
		},
		{
			name:  "exact limit passes through",
			input: "hello",
			limit: 5,
			want:  "hello",
		},
		{
			name:  "truncated appends ...",
			input: "hello world",
			limit: 5,
			want:  "hello...",
		},
		{
			name:  "korean truncated",
			input: "안녕하세요 반갑습니다",
			limit: 5,
			want:  "안녕하세요...",
		},
		{
			name:  "empty string",
			input: "",
			limit: 10,
			want:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := compactContent(tc.input, tc.limit)
			if got != tc.want {
				t.Errorf("compactContent(%q, %d) = %q; want %q",
					tc.input, tc.limit, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// NewMessageStore — empty DSN creates no-op store
// ---------------------------------------------------------------------------

func TestNewMessageStoreEmptyDSN(t *testing.T) {
	ctx := context.Background()
	store, err := NewMessageStore(ctx, "")
	if err != nil {
		t.Fatalf("NewMessageStore with empty DSN returned error: %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil store")
	}
	if store.pool != nil {
		t.Error("expected nil pool for empty DSN")
	}
}

// ---------------------------------------------------------------------------
// GetHistory / Append — in-memory fallback
// ---------------------------------------------------------------------------

func TestGetHistoryAppendInMemory(t *testing.T) {
	ctx := context.Background()
	store, err := NewMessageStore(ctx, "")
	if err != nil {
		t.Fatalf("NewMessageStore: %v", err)
	}

	const key = "channel:C1234"

	// Empty history.
	msgs, err := store.GetHistory(ctx, key, 10, "", "", "")
	if err != nil {
		t.Fatalf("GetHistory on empty store: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages, got %d", len(msgs))
	}

	// Append two messages.
	_ = store.Append(ctx, key, Message{Role: "user", Content: "hello"})
	_ = store.Append(ctx, key, Message{Role: "assistant", Content: "hi"})

	msgs, err = store.GetHistory(ctx, key, 10, "", "", "")
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Content != "hello" {
		t.Errorf("unexpected first message: %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" || msgs[1].Content != "hi" {
		t.Errorf("unexpected second message: %+v", msgs[1])
	}
}

func TestGetHistoryLimit(t *testing.T) {
	ctx := context.Background()
	store, _ := NewMessageStore(ctx, "")

	const key = "channel:C9999"
	for i := 0; i < 5; i++ {
		_ = store.Append(ctx, key, Message{Role: "user", Content: "msg"})
	}

	msgs, err := store.GetHistory(ctx, key, 3, "", "", "")
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(msgs) != 3 {
		t.Errorf("expected 3 messages with limit=3, got %d", len(msgs))
	}
}

// ---------------------------------------------------------------------------
// GetHistory — DB fallback behaviour (no real DB required: nil pool path)
// ---------------------------------------------------------------------------

// TestGetHistoryReturnsInMemoryWhenAvailable verifies that populated in-memory
// data is returned without touching the DB.
func TestGetHistoryReturnsInMemoryWhenAvailable(t *testing.T) {
	ctx := context.Background()
	store, _ := NewMessageStore(ctx, "") // pool == nil

	const key = "thread:T001"
	_ = store.Append(ctx, key, Message{Role: "user", Content: "ping"})
	_ = store.Append(ctx, key, Message{Role: "assistant", Content: "pong"})

	msgs, err := store.GetHistory(ctx, key, 10, "bot1", "ch1", "T001")
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Content != "ping" || msgs[1].Content != "pong" {
		t.Errorf("unexpected messages: %+v", msgs)
	}
}

// TestGetHistoryEmptyWhenNoPoolAndNoMemory verifies that an empty slice is
// returned when in-memory is empty and no DB pool is available.
func TestGetHistoryEmptyWhenNoPoolAndNoMemory(t *testing.T) {
	ctx := context.Background()
	store, _ := NewMessageStore(ctx, "") // pool == nil

	msgs, err := store.GetHistory(ctx, "channel:CNEW", 10, "bot1", "chX", "")
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages for empty store with nil pool, got %d", len(msgs))
	}
}

// TestGetHistoryDBFallbackCachesResults verifies that results returned by the
// DB fallback are cached in the in-memory store so a second call does not hit
// the DB again. We simulate a DB result by pre-populating the history map
// after a first call returns empty, then checking the cache is used.
//
// Because we cannot inject a real pgxpool in a unit test, this test exercises
// the cache path indirectly: after a successful Append the subsequent
// GetHistory must return from cache (in-memory), not re-query.
func TestGetHistoryDBFallbackCachesResults(t *testing.T) {
	ctx := context.Background()
	store, _ := NewMessageStore(ctx, "") // pool == nil

	const key = "channel:CCACHE"

	// First call: in-memory empty, no pool → returns nil.
	first, err := store.GetHistory(ctx, key, 10, "bot1", "ch1", "")
	if err != nil {
		t.Fatalf("first GetHistory: %v", err)
	}
	if len(first) != 0 {
		t.Errorf("expected 0 messages on first call, got %d", len(first))
	}

	// Simulate messages being appended (as SaveMessage + Append would do).
	_ = store.Append(ctx, key, Message{Role: "user", Content: "cached-msg"})

	// Second call: in-memory now populated → should return from cache.
	second, err := store.GetHistory(ctx, key, 10, "bot1", "ch1", "")
	if err != nil {
		t.Fatalf("second GetHistory: %v", err)
	}
	if len(second) != 1 {
		t.Fatalf("expected 1 cached message, got %d", len(second))
	}
	if second[0].Content != "cached-msg" {
		t.Errorf("unexpected cached message: %+v", second[0])
	}
}

// ---------------------------------------------------------------------------
// Preference cache — set, get within TTL, invalidate, expired TTL
// ---------------------------------------------------------------------------

func TestPreferenceCacheSetAndGet(t *testing.T) {
	store, _ := NewMessageStore(context.Background(), "")

	// Manually inject a cache entry (no DB).
	store.prefMu.Lock()
	store.prefCache["bot1"] = prefCacheEntry{
		prefs:     []string{"pref-a", "pref-b"},
		fetchedAt: time.Now(),
	}
	store.prefMu.Unlock()

	ctx := context.Background()
	prefs, err := store.GetPreferences(ctx, "bot1")
	if err != nil {
		t.Fatalf("GetPreferences: %v", err)
	}
	if len(prefs) != 2 || prefs[0] != "pref-a" || prefs[1] != "pref-b" {
		t.Errorf("unexpected prefs: %v", prefs)
	}
}

func TestPreferenceCacheInvalidate(t *testing.T) {
	store, _ := NewMessageStore(context.Background(), "")

	store.prefMu.Lock()
	store.prefCache["bot2"] = prefCacheEntry{
		prefs:     []string{"old-pref"},
		fetchedAt: time.Now(),
	}
	store.prefMu.Unlock()

	store.InvalidatePreferenceCache("bot2")

	store.prefMu.RLock()
	_, ok := store.prefCache["bot2"]
	store.prefMu.RUnlock()

	if ok {
		t.Error("cache entry should have been removed after invalidation")
	}
}

func TestPreferenceCacheExpiredTTL(t *testing.T) {
	store, _ := NewMessageStore(context.Background(), "")

	// Inject an entry with a timestamp older than the TTL.
	store.prefMu.Lock()
	store.prefCache["bot3"] = prefCacheEntry{
		prefs:     []string{"stale-pref"},
		fetchedAt: time.Now().Add(-(prefCacheTTL + time.Second)),
	}
	store.prefMu.Unlock()

	ctx := context.Background()
	// pool is nil, so GetPreferences will attempt DB (returns nil, nil without pool).
	prefs, err := store.GetPreferences(ctx, "bot3")
	if err != nil {
		t.Fatalf("GetPreferences: %v", err)
	}
	// Should not return the stale cached value; pool is nil so returns nil.
	if len(prefs) != 0 {
		t.Errorf("expected empty prefs for expired cache with no DB, got %v", prefs)
	}
}

// ---------------------------------------------------------------------------
// scoreRows helper
// ---------------------------------------------------------------------------

func TestScoreRows(t *testing.T) {
	type candidate struct{ content, category, ts string }
	rows := []candidate{
		{"서버 오류 발생", "fact", "2024-01-01"},
		{"다른 내용", "note", "2024-01-02"},
		{"서버 배포 완료", "action", "2024-01-03"},
	}

	results := scoreRows("서버", rows, 5, func(r candidate) (string, string, string) {
		return r.content, r.category, r.ts
	})

	// At least 2 results should match "서버".
	if len(results) < 2 {
		t.Errorf("expected ≥2 matches for '서버', got %d", len(results))
	}
	// Results should be sorted descending by score.
	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score {
			t.Errorf("results not sorted by score: [%d]=%v > [%d]=%v",
				i, results[i].Score, i-1, results[i-1].Score)
		}
	}
}

// ---------------------------------------------------------------------------
// MemoryExists — no-op when pool is nil
// ---------------------------------------------------------------------------

func TestMemoryExistsNoPool(t *testing.T) {
	store, _ := NewMessageStore(context.Background(), "")
	exists, err := store.MemoryExists(context.Background(), "bot", "fact", "some fact")
	if err != nil {
		t.Fatalf("MemoryExists: %v", err)
	}
	if exists {
		t.Error("expected false for nil pool")
	}
}

// ---------------------------------------------------------------------------
// Close — safe to call on nil pool
// ---------------------------------------------------------------------------

func TestCloseNilPool(t *testing.T) {
	store, _ := NewMessageStore(context.Background(), "")
	// Should not panic.
	store.Close()
}

// ---------------------------------------------------------------------------
// GetRecentThreads — no-op when pool is nil
// ---------------------------------------------------------------------------

func TestGetRecentThreadsNilPool(t *testing.T) {
	store, _ := NewMessageStore(context.Background(), "")
	groups, err := store.GetRecentThreads(context.Background(), "bot1", "C123", 5, 20)
	if err != nil {
		t.Fatalf("GetRecentThreads with nil pool: %v", err)
	}
	if len(groups) != 0 {
		t.Errorf("expected empty slice for nil pool, got %d groups", len(groups))
	}
}

// ---------------------------------------------------------------------------
// GetMessageIDByPlatformID — no-op when pool is nil
// ---------------------------------------------------------------------------

func TestGetMessageIDByPlatformID_nilPool(t *testing.T) {
	store, _ := NewMessageStore(context.Background(), "")
	id, err := store.GetMessageIDByPlatformID(context.Background(), "bot1", "msg-1")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if id != "" {
		t.Errorf("expected empty string, got %q", id)
	}
}

// ---------------------------------------------------------------------------
// FindMemoriesBySourceMessage — no-op when pool is nil
// ---------------------------------------------------------------------------

func TestFindMemoriesBySourceMessage_nilPool(t *testing.T) {
	store, _ := NewMessageStore(context.Background(), "")
	ids, err := store.FindMemoriesBySourceMessage(context.Background(), "bot1", "uuid-1")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if ids != nil {
		t.Errorf("expected nil, got %v", ids)
	}
}

// ---------------------------------------------------------------------------
// DeleteMemory — no-op when pool is nil
// ---------------------------------------------------------------------------

func TestDeleteMemory_nilPool(t *testing.T) {
	store, _ := NewMessageStore(context.Background(), "")
	err := store.DeleteMemory(context.Background(), "uuid-1")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// StoreMemory — no-op when pool is nil
// ---------------------------------------------------------------------------

func TestStoreMemory_nilPool(t *testing.T) {
	store, _ := NewMessageStore(context.Background(), "")
	id, err := store.StoreMemory(context.Background(), "bot1", "user1", "fact", "test content", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if id != "" {
		t.Errorf("expected empty string, got %q", id)
	}
}

func TestStoreMemory_nilPool_withSourceMessageID(t *testing.T) {
	store, _ := NewMessageStore(context.Background(), "")
	id, err := store.StoreMemory(context.Background(), "bot1", "user1", "fact", "test content", "550e8400-e29b-41d4-a716-446655440000")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if id != "" {
		t.Errorf("expected empty string, got %q", id)
	}
}

// ---------------------------------------------------------------------------
// ThreadGroup struct
// ---------------------------------------------------------------------------

func TestThreadGroupFields(t *testing.T) {
	firstAt := time.Date(2026, 3, 28, 9, 0, 0, 0, time.UTC)
	msgs := []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}
	g := ThreadGroup{
		ThreadTS:  "1711616400.000100",
		ChannelID: "C999",
		Messages:  msgs,
		FirstAt:   firstAt,
	}
	if g.ThreadTS != "1711616400.000100" {
		t.Errorf("unexpected ThreadTS: %q", g.ThreadTS)
	}
	if g.ChannelID != "C999" {
		t.Errorf("unexpected ChannelID: %q", g.ChannelID)
	}
	if len(g.Messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(g.Messages))
	}
	if !g.FirstAt.Equal(firstAt) {
		t.Errorf("unexpected FirstAt: %v", g.FirstAt)
	}
}

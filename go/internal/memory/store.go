// Package memory provides conversation history storage and retrieval.
package memory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/singleflight"
)

// _koreanParticles lists Korean particles to strip during tokenisation.
// Longer particles are listed first so that the most-specific match wins.
var _koreanParticles = []string{
	// 3-character
	"으로는", "에서는", "에게서", "로부터", "에서도", "에게도",
	// 2-character
	"에서", "에게", "한테", "으로", "부터", "까지", "마다", "보다",
	"처럼", "만큼", "에는", "로는", "이나", "거나", "라도", "이라도",
	"하고", "이랑", "이며", "으며", "이고",
	// 1-character
	"은", "는", "이", "가", "을", "를", "에", "의", "도", "만",
	"와", "과", "로", "라", "야",
}

// whitespaceRe collapses consecutive whitespace into a single space.
var whitespaceRe = regexp.MustCompile(`\s+`)

// tokenRe matches Korean syllable sequences or ASCII word characters.
var tokenRe = regexp.MustCompile(`[\p{Hangul}a-zA-Z0-9_]+`)

// prefCacheTTL is how long a preference cache entry stays fresh.
const prefCacheTTL = 5 * time.Minute

// prefCacheEntry is a single slot in the preference TTL cache.
type prefCacheEntry struct {
	prefs     []string
	fetchedAt time.Time
}

// Message is a single entry in the conversation history.
type Message struct {
	Role          string // "user" or "assistant"
	Content       string
	Timestamp     string // ISO 8601; empty for in-memory messages
	PlatformMsgID string // Platform-specific message ID (Slack ts, Discord snowflake)
}

// maxInMemoryKeys is the maximum number of distinct history keys kept in the
// in-memory fallback store. When the cap is exceeded the oldest key is evicted.
const maxInMemoryKeys = 1000

// MessageStore persists and retrieves conversation history.
// When pool is nil, all PostgreSQL methods are no-ops and
// GetHistory/Append use an in-memory fallback so the application
// can run without a database.
type MessageStore struct {
	pool *pgxpool.Pool

	// in-memory fallback used when pool == nil
	mu         sync.Mutex
	history    map[string][]Message
	historyOrd []string // insertion-order key tracking for eviction

	// sf coalesces concurrent DB fallback queries for the same history key,
	// preventing duplicate DB round-trips on process restart.
	sf singleflight.Group

	// preference TTL cache
	prefMu    sync.RWMutex
	prefCache map[string]prefCacheEntry
}

// NewMessageStore opens a pgxpool connection to dsn.
// If dsn is empty the store operates in no-op / in-memory mode.
func NewMessageStore(ctx context.Context, dsn string) (*MessageStore, error) {
	s := &MessageStore{
		history:   make(map[string][]Message),
		prefCache: make(map[string]prefCacheEntry),
		// historyOrd is lazily allocated on first Append.
	}
	if dsn == "" {
		slog.Warn("DATABASE_DSN not set; MessageStore running in no-op mode")
		return s, nil
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("pgxpool.New: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pgxpool ping: %w", err)
	}
	s.pool = pool
	return s, nil
}

// Close releases the underlying connection pool.
func (s *MessageStore) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
}

// Pool returns the underlying pgxpool.Pool so callers that need direct pool
// access (e.g. credprobe) do not have to maintain a separate connection pool.
// Returns nil when the store is running in no-op / in-memory mode.
func (s *MessageStore) Pool() *pgxpool.Pool {
	return s.pool
}

// ---------------------------------------------------------------------------
// Core persistence
// ---------------------------------------------------------------------------

// SaveMessage inserts a single message row.
// platformMsgID is the platform-native identifier (e.g. Slack ts, Discord snowflake).
// Pass an empty string when the platform message ID is not yet available.
func (s *MessageStore) SaveMessage(
	ctx context.Context,
	botID, channelID, threadTS, userID, role, content, platformMsgID string,
) error {
	if s.pool == nil {
		return nil
	}
	const q = `
		INSERT INTO messages (bot_id, channel_id, thread_ts, user_id, role, content, platform_message_id)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''))`
	if _, err := s.pool.Exec(ctx, q, botID, channelID, threadTS, userID, role, content, platformMsgID); err != nil {
		return err
	}
	return nil
}

// SoftDeleteMessage marks a message as deleted by setting deleted_at.
// It looks up the message by platform_message_id and bot_id.
// A debug log is emitted when no matching active message is found; this is
// not treated as an error because the message may have already been deleted.
func (s *MessageStore) SoftDeleteMessage(ctx context.Context, botID, platformMsgID string) error {
	if s.pool == nil {
		return nil
	}
	const q = `UPDATE messages_data SET deleted_at = NOW() WHERE bot_id = $1 AND platform_message_id = $2 AND deleted_at IS NULL`
	tag, err := s.pool.Exec(ctx, q, botID, platformMsgID)
	if err != nil {
		return fmt.Errorf("soft delete message: %w", err)
	}
	if tag.RowsAffected() == 0 {
		slog.Debug("soft delete: no matching message found", "bot_id", botID, "platform_msg_id", platformMsgID)
	}
	return nil
}

// UpdateMessageContent updates the content of a message and records the edit.
// The original content is preserved in original_content on the first edit only
// (COALESCE ensures it is not overwritten on subsequent edits).
func (s *MessageStore) UpdateMessageContent(ctx context.Context, botID, platformMsgID, newContent string) error {
	if s.pool == nil {
		return nil
	}
	const q = `UPDATE messages_data
		SET content = $3,
		    edited_at = NOW(),
		    original_content = COALESCE(original_content, content)
		WHERE bot_id = $1 AND platform_message_id = $2 AND deleted_at IS NULL`
	tag, err := s.pool.Exec(ctx, q, botID, platformMsgID, newContent)
	if err != nil {
		return fmt.Errorf("update message content: %w", err)
	}
	if tag.RowsAffected() == 0 {
		slog.Debug("update content: no matching message found", "bot_id", botID, "platform_msg_id", platformMsgID)
	}
	return nil
}

// RemoveFromHistory removes messages with the given platform message ID from
// the in-memory cache. This is the cache-side counterpart of SoftDeleteMessage.
func (s *MessageStore) RemoveFromHistory(key, platformMsgID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	msgs, ok := s.history[key]
	if !ok {
		return
	}
	filtered := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		if m.PlatformMsgID != platformMsgID {
			filtered = append(filtered, m)
		}
	}
	s.history[key] = filtered
}

// UpdateInHistory updates the content of messages with the given platform
// message ID in the in-memory cache. This is the cache-side counterpart of
// UpdateMessageContent.
func (s *MessageStore) UpdateInHistory(key, platformMsgID, newContent string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	msgs, ok := s.history[key]
	if !ok {
		return
	}
	for i := range msgs {
		if msgs[i].PlatformMsgID == platformMsgID {
			msgs[i].Content = newContent
		}
	}
}

// GetThreadMessages returns up to limit messages for the given thread,
// ordered oldest-first.
func (s *MessageStore) GetThreadMessages(
	ctx context.Context,
	botID, channelID, threadTS string,
	limit int,
) ([]Message, error) {
	if s.pool == nil {
		return nil, nil
	}
	const q = `
		SELECT role, content, timestamp
		FROM messages
		WHERE bot_id = $1 AND channel_id = $2 AND thread_ts = $3
		ORDER BY timestamp DESC
		LIMIT $4`
	rows, err := s.pool.Query(ctx, q, botID, channelID, threadTS, limit)
	if err != nil {
		slog.Warn("GetThreadMessages failed", "error", err)
		return nil, err
	}
	defer rows.Close()
	msgs, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}
	reverseMessages(msgs)
	return msgs, nil
}

// GetChannelMessages returns up to limit channel-level messages, optionally
// excluding a specific thread.
func (s *MessageStore) GetChannelMessages(
	ctx context.Context,
	botID, channelID string,
	limit int,
	excludeThreadTS string,
) ([]Message, error) {
	if s.pool == nil {
		return nil, nil
	}
	const q = `
		SELECT role, content, timestamp
		FROM messages
		WHERE bot_id = $1
		  AND channel_id = $2
		  AND (thread_ts IS NULL OR thread_ts = '' OR thread_ts != $3)
		ORDER BY timestamp DESC
		LIMIT $4`
	rows, err := s.pool.Query(ctx, q, botID, channelID, excludeThreadTS, limit)
	if err != nil {
		slog.Warn("GetChannelMessages failed", "error", err)
		return nil, err
	}
	defer rows.Close()
	msgs, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}
	reverseMessages(msgs)
	return msgs, nil
}

// GetRecentMessages returns thread messages first; if none exist it falls back
// to channel messages.
func (s *MessageStore) GetRecentMessages(
	ctx context.Context,
	botID, channelID, threadTS string,
	limit int,
) ([]Message, error) {
	if threadTS != "" {
		msgs, err := s.GetThreadMessages(ctx, botID, channelID, threadTS, limit)
		if err != nil {
			return nil, err
		}
		if len(msgs) > 0 {
			return msgs, nil
		}
	}
	return s.GetChannelMessages(ctx, botID, channelID, limit, threadTS)
}

// ---------------------------------------------------------------------------
// Processor compatibility: GetHistory / Append
// ---------------------------------------------------------------------------

// GetHistory returns up to limit recent messages for key.
// The key is an opaque string from the processor (e.g. "thread:xxx" or
// "channel:yyy"). When in-memory history is non-empty it is returned
// directly. When in-memory is empty and a DB pool is available the method
// falls back to querying the DB using (botID, channelID, threadTS) as
// composite coordinates. Results fetched from DB are cached in the
// in-memory store for subsequent calls in the same process lifetime.
//
// botID, channelID, and threadTS are only used for the DB fallback; they
// are ignored when in-memory history is already populated.
func (s *MessageStore) GetHistory(ctx context.Context, key string, limit int, botID, channelID, threadTS string) ([]Message, error) {
	s.mu.Lock()
	all := s.history[key]
	if len(all) > 0 {
		result := s.sliceHistory(all, limit)
		s.mu.Unlock()
		return result, nil
	}
	s.mu.Unlock()

	// In-memory is empty. Try DB fallback when pool is available.
	// singleflight coalesces concurrent callers with the same key so that
	// only one DB round-trip is issued on process restart.
	if s.pool != nil && botID != "" && channelID != "" {
		type sfResult struct {
			msgs []Message
			err  error
		}
		val, _, _ := s.sf.Do(key, func() (interface{}, error) {
			var dbMsgs []Message
			var dbErr error
			if threadTS != "" {
				dbMsgs, dbErr = s.GetThreadMessages(ctx, botID, channelID, threadTS, limit)
			} else {
				dbMsgs, dbErr = s.GetChannelMessages(ctx, botID, channelID, limit, "")
			}
			return &sfResult{dbMsgs, dbErr}, nil
		})
		res := val.(*sfResult)
		if res.err != nil {
			slog.Warn("GetHistory DB fallback failed", "key", key, "error", res.err)
			return nil, res.err
		}
		if len(res.msgs) > 0 {
			s.mu.Lock()
			// Re-check: another goroutine may have populated via Append while
			// the DB query was in flight.
			if existing := s.history[key]; len(existing) > 0 {
				result := s.sliceHistory(existing, limit)
				s.mu.Unlock()
				return result, nil
			}
			// Cache DB results so subsequent calls skip the DB round-trip.
			if _, exists := s.history[key]; !exists {
				s.historyOrd = append(s.historyOrd, key)
				if len(s.historyOrd) > maxInMemoryKeys {
					oldest := s.historyOrd[0]
					s.historyOrd = s.historyOrd[1:]
					delete(s.history, oldest)
				}
			}
			s.history[key] = res.msgs
			result := s.sliceHistory(res.msgs, limit)
			s.mu.Unlock()
			return result, nil
		}
	}

	return nil, nil
}

// sliceHistory returns a copy of the last limit messages from all.
// Callers must hold s.mu.
func (s *MessageStore) sliceHistory(all []Message, limit int) []Message {
	if len(all) <= limit {
		out := make([]Message, len(all))
		copy(out, all)
		return out
	}
	out := make([]Message, limit)
	copy(out, all[len(all)-limit:])
	return out
}

// Append adds a message for key to the in-memory store.
// The DB schema has no history_key column; processor-facing history writes
// go to the in-memory store. Durable persistence is handled separately via
// SaveMessage which accepts explicit (bot_id, channel_id, thread_ts) coords.
func (s *MessageStore) Append(_ context.Context, key string, msg Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.history[key]; !exists {
		// New key: track insertion order and evict oldest when cap is exceeded.
		s.historyOrd = append(s.historyOrd, key)
		if len(s.historyOrd) > maxInMemoryKeys {
			oldest := s.historyOrd[0]
			s.historyOrd = s.historyOrd[1:]
			delete(s.history, oldest)
		}
	}
	s.history[key] = append(s.history[key], msg)
	return nil
}

// ---------------------------------------------------------------------------
// Preferences
// ---------------------------------------------------------------------------

// GetPreferences returns preference strings for botID, caching for prefCacheTTL.
func (s *MessageStore) GetPreferences(ctx context.Context, botID string) ([]string, error) {
	s.prefMu.RLock()
	entry, ok := s.prefCache[botID]
	s.prefMu.RUnlock()
	if ok && time.Since(entry.fetchedAt) < prefCacheTTL {
		return entry.prefs, nil
	}

	if s.pool == nil {
		return nil, nil
	}

	const q = `
		SELECT content
		FROM memories
		WHERE bot_id = $1 AND category = 'preference'
		ORDER BY created_at DESC`
	rows, err := s.pool.Query(ctx, q, botID)
	if err != nil {
		slog.Warn("GetPreferences failed", "bot_id", botID, "error", err)
		return nil, err
	}
	defer rows.Close()

	var prefs []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		prefs = append(prefs, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	s.prefMu.Lock()
	s.prefCache[botID] = prefCacheEntry{prefs: prefs, fetchedAt: time.Now()}
	s.prefMu.Unlock()

	return prefs, nil
}

// InvalidatePreferenceCache evicts the cached preferences for botID.
func (s *MessageStore) InvalidatePreferenceCache(botID string) {
	s.prefMu.Lock()
	delete(s.prefCache, botID)
	s.prefMu.Unlock()
}

// ---------------------------------------------------------------------------
// Memory search
// ---------------------------------------------------------------------------

// SearchMemories runs a keyword-scored search over bot_memories.
func (s *MessageStore) SearchMemories(
	ctx context.Context,
	botID, queryText string,
	limit int,
) ([]SearchResult, error) {
	if s.pool == nil {
		return nil, nil
	}
	// Fetch a larger candidate pool then re-rank in Go.
	const q = `
		SELECT content, category, created_at
		FROM memories
		WHERE bot_id = $1
		ORDER BY created_at DESC
		LIMIT $2`
	rows, err := s.pool.Query(ctx, q, botID, limit*5)
	if err != nil {
		slog.Warn("SearchMemories failed", "error", err)
		return nil, err
	}
	defer rows.Close()

	type row struct{ content, category, createdAt string }
	var candidates []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.content, &r.category, &r.createdAt); err != nil {
			return nil, err
		}
		candidates = append(candidates, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return scoreRows(queryText, candidates, limit, func(r row) (string, string, string) {
		return r.content, r.category, r.createdAt
	}), nil
}

// SearchRecentMessages scores recent messages in a specific channel against queryText.
func (s *MessageStore) SearchRecentMessages(
	ctx context.Context,
	botID, channelID, queryText string,
	limit, lookback int,
) ([]SearchResult, error) {
	if s.pool == nil {
		return nil, nil
	}
	const q = `
		SELECT content, timestamp
		FROM messages
		WHERE bot_id = $1
		  AND channel_id = $2
		ORDER BY timestamp DESC
		LIMIT $3`
	rows, err := s.pool.Query(ctx, q, botID, channelID, lookback)
	if err != nil {
		slog.Warn("SearchRecentMessages failed", "error", err)
		return nil, err
	}
	defer rows.Close()

	type row struct{ content, createdAt string }
	var candidates []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.content, &r.createdAt); err != nil {
			return nil, err
		}
		candidates = append(candidates, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return scoreRows(queryText, candidates, limit, func(r row) (string, string, string) {
		return r.content, "", r.createdAt
	}), nil
}

// MemoryExists reports whether a matching memory row already exists.
// Case-insensitive, whitespace-trimmed comparison is used to avoid
// near-duplicate entries.
func (s *MessageStore) MemoryExists(
	ctx context.Context,
	botID, category, content string,
) (bool, error) {
	if s.pool == nil {
		return false, nil
	}
	const q = `
		SELECT 1 FROM memories
		WHERE bot_id = $1 AND category = $2 AND lower(trim(content)) = lower(trim($3))
		LIMIT 1`
	var found int
	err := s.pool.QueryRow(ctx, q, botID, category, content).Scan(&found)
	if err != nil {
		// pgx returns pgx.ErrNoRows (which wraps sql.ErrNoRows) when SELECT 1
		// finds no matching row. That is not an error — it means "does not exist".
		if isNoRows(err) {
			return false, nil
		}
		slog.Warn("MemoryExists failed", "error", err)
		return false, err
	}
	return true, nil
}

// StoreMemory persists a single extracted memory entry to the memories table
// and returns the generated UUID so the caller can use it for downstream
// indexing. A 1024-dimension zero-vector placeholder is stored for the
// embedding column (matching the actual pgvector column definition) until a
// proper embedding pipeline is available.
// When pool is nil the operation is a no-op and an empty string is returned.
func (s *MessageStore) StoreMemory(
	ctx context.Context,
	botID, userID, category, content string,
) (string, error) {
	if s.pool == nil {
		return "", nil
	}
	const q = `
		INSERT INTO memories (bot_id, user_id, category, content, embedding)
		VALUES ($1, $2, $3, $4, array_fill(0, ARRAY[1024])::vector)
		RETURNING id::text`
	var id string
	if err := s.pool.QueryRow(ctx, q, botID, userID, category, content).Scan(&id); err != nil {
		slog.Warn("StoreMemory failed", "error", err)
		return "", err
	}
	return id, nil
}

// ---------------------------------------------------------------------------
// Korean text processing
// ---------------------------------------------------------------------------

// normalizeText collapses whitespace, trims, and lowercases text.
func normalizeText(text string) string {
	text = whitespaceRe.ReplaceAllString(text, " ")
	return strings.ToLower(strings.TrimSpace(text))
}

// queryTokens tokenises text, strips Korean particles, deduplicates, and
// removes tokens shorter than 2 runes.
func queryTokens(text string) []string {
	norm := normalizeText(text)
	raw := tokenRe.FindAllString(norm, -1)

	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, tok := range raw {
		tok = stripKoreanParticle(tok)
		if len([]rune(tok)) < 2 {
			continue
		}
		if _, dup := seen[tok]; dup {
			continue
		}
		seen[tok] = struct{}{}
		out = append(out, tok)
	}
	return out
}

// stripKoreanParticle removes a trailing Korean particle from tok when the
// remaining stem has at least one word character.
func stripKoreanParticle(tok string) string {
	runes := []rune(tok)
	for _, p := range _koreanParticles {
		pr := []rune(p)
		if len(runes) > len(pr) && strings.HasSuffix(tok, p) {
			stem := string(runes[:len(runes)-len(pr)])
			if hasWordChar(stem) {
				return stem
			}
		}
	}
	return tok
}

// hasWordChar reports whether s contains at least one letter or digit.
func hasWordChar(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// scoreTextMatch returns a [0, 1] relevance score for content against queryText.
func scoreTextMatch(queryText, content string) float64 {
	normQuery := normalizeText(queryText)
	if normQuery == "" {
		return 0.0
	}
	normContent := normalizeText(content)

	if normContent == normQuery {
		return 1.0
	}
	if strings.Contains(normContent, normQuery) {
		return 0.9
	}

	tokens := queryTokens(queryText)
	if len(tokens) == 0 {
		return 0.0
	}

	matches := 0
	for _, tok := range tokens {
		if strings.Contains(normContent, tok) {
			matches++
		}
	}
	if matches == 0 {
		return 0.0
	}
	// Token overlap in [0.1, 0.8].
	return 0.1 + (float64(matches)/float64(len(tokens)))*0.7
}

// compactContent truncates content to limit runes, appending "..." when cut.
func compactContent(content string, limit int) string {
	runes := []rune(content)
	if len(runes) <= limit {
		return content
	}
	return string(runes[:limit]) + "..."
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// isNoRows reports whether err represents a "no rows" result from pgx.
// pgx.ErrNoRows is returned by QueryRow.Scan when the query returns zero rows.
func isNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

// pgxRows is the minimal pgx.Rows subset used by scanMessages.
type pgxRows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}

// scanMessages reads (role, content, created_at) rows into []Message.
func scanMessages(rows pgxRows) ([]Message, error) {
	var msgs []Message
	for rows.Next() {
		var m Message
		var ts any
		if err := rows.Scan(&m.Role, &m.Content, &ts); err != nil {
			return nil, err
		}
		switch v := ts.(type) {
		case time.Time:
			m.Timestamp = v.UTC().Format(time.RFC3339)
		case string:
			m.Timestamp = v
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// reverseMessages reverses msgs in place.
func reverseMessages(msgs []Message) {
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
}

// scoreRows scores each T against queryText, sorts descending, and returns the
// top limit results.
func scoreRows[T any](
	queryText string,
	rows []T,
	limit int,
	extract func(T) (content, category, createdAt string),
) []SearchResult {
	type scored struct {
		res   SearchResult
		score float64
	}
	candidates := make([]scored, 0, len(rows))
	for _, r := range rows {
		content, category, createdAt := extract(r)
		sc := scoreTextMatch(queryText, content)
		if sc <= 0 {
			continue
		}
		candidates = append(candidates, scored{
			res:   SearchResult{Content: content, Category: category, Score: sc, CreatedAt: createdAt},
			score: sc,
		})
	}
	// Insertion sort — candidate sets are small (< ~500 rows).
	for i := 1; i < len(candidates); i++ {
		for j := i; j > 0 && candidates[j].score > candidates[j-1].score; j-- {
			candidates[j], candidates[j-1] = candidates[j-1], candidates[j]
		}
	}
	if limit > len(candidates) {
		limit = len(candidates)
	}
	out := make([]SearchResult, limit)
	for i := range out {
		out[i] = candidates[i].res
	}
	return out
}

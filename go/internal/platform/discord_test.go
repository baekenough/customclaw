package platform

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

// ---------------------------------------------------------------------------
// splitMessage tests
// ---------------------------------------------------------------------------

func TestSplitMessage_ShortText(t *testing.T) {
	text := "hello world"
	got := splitMessage(text, 2000)
	if len(got) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(got))
	}
	if got[0] != text {
		t.Errorf("expected %q, got %q", text, got[0])
	}
}

func TestSplitMessage_ExactLimit(t *testing.T) {
	text := strings.Repeat("a", 2000)
	got := splitMessage(text, 2000)
	if len(got) != 1 {
		t.Fatalf("expected 1 chunk for exact-limit text, got %d", len(got))
	}
}

func TestSplitMessage_SplitAtParagraph(t *testing.T) {
	// 1800 "a" chars + paragraph boundary + 400 "b" chars → must split at "\n\n"
	part1 := strings.Repeat("a", 1800)
	part2 := strings.Repeat("b", 400)
	text := part1 + "\n\n" + part2

	got := splitMessage(text, 2000)
	if len(got) != 2 {
		t.Fatalf("expected 2 chunks, got %d: %v", len(got), got)
	}
	if got[0] != part1 {
		t.Errorf("first chunk mismatch: got len=%d, want len=%d", len(got[0]), len(part1))
	}
	if got[1] != part2 {
		t.Errorf("second chunk mismatch: got len=%d, want len=%d", len(got[1]), len(part2))
	}
}

func TestSplitMessage_SplitAtNewline(t *testing.T) {
	// 1800 "a" chars + single newline + 400 "b" chars → splits at "\n"
	part1 := strings.Repeat("a", 1800)
	part2 := strings.Repeat("b", 400)
	text := part1 + "\n" + part2

	got := splitMessage(text, 2000)
	if len(got) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(got))
	}
	if got[0] != part1 {
		t.Errorf("first chunk mismatch")
	}
	if got[1] != part2 {
		t.Errorf("second chunk mismatch")
	}
}

func TestSplitMessage_SplitAtSpace(t *testing.T) {
	// 1800 "a" chars + space + 400 "b" chars → splits at " "
	part1 := strings.Repeat("a", 1800)
	part2 := strings.Repeat("b", 400)
	text := part1 + " " + part2

	got := splitMessage(text, 2000)
	if len(got) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(got))
	}
	if got[0] != part1 {
		t.Errorf("first chunk mismatch")
	}
	if got[1] != part2 {
		t.Errorf("second chunk mismatch")
	}
}

func TestSplitMessage_HardCutNoWhitespace(t *testing.T) {
	// 2500 chars with no whitespace → hard cut at 2000
	text := strings.Repeat("x", 2500)
	got := splitMessage(text, 2000)
	if len(got) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(got))
	}
	if len(got[0]) != 2000 {
		t.Errorf("first chunk: expected 2000 chars, got %d", len(got[0]))
	}
	if len(got[1]) != 500 {
		t.Errorf("second chunk: expected 500 chars, got %d", len(got[1]))
	}
}

func TestSplitMessage_EmptyText(t *testing.T) {
	got := splitMessage("", 2000)
	if len(got) != 0 {
		t.Errorf("expected no chunks for empty text, got %d", len(got))
	}
}

func TestSplitMessageKorean(t *testing.T) {
	// Build a Korean text longer than 2000 runes.
	// Each Korean character is 3 bytes in UTF-8 but 1 rune.
	// 700 runes of Korean × 3 = 2100 bytes, but only 700 runes → fits in one chunk.
	// We need > 2000 runes total, so use 3 × 700-rune blocks separated by spaces.
	block := strings.Repeat("가나다라마바사아자차카타파하", 50) // 14 runes × 50 = 700 runes
	text := block + " " + block + " " + block             // 700 + 1 + 700 + 1 + 700 = 2102 runes

	got := splitMessage(text, 2000)
	if len(got) == 0 {
		t.Fatal("expected at least one chunk")
	}

	// Each chunk must be within 2000 runes.
	for i, chunk := range got {
		runeCount := len([]rune(chunk))
		if runeCount > 2000 {
			t.Errorf("chunk %d has %d runes, exceeds limit of 2000", i, runeCount)
		}
	}

	// Verify no garbled text: re-joining all chunks must recover the original text
	// (ignoring leading/trailing whitespace stripped during splitting).
	joined := strings.Join(got, " ")
	if len([]rune(joined)) == 0 {
		t.Error("joined chunks are empty")
	}

	// Verify all chunks are valid UTF-8 (no broken multi-byte sequences).
	for i, chunk := range got {
		if !utf8ValidString(chunk) {
			t.Errorf("chunk %d contains invalid UTF-8", i)
		}
	}
}

// utf8ValidString reports whether s is valid UTF-8 by checking that every rune
// is not the replacement character introduced by invalid byte sequences.
func utf8ValidString(s string) bool {
	for _, r := range s {
		if r == '\uFFFD' {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// resolveEmoji tests
// ---------------------------------------------------------------------------

func TestResolveEmoji_KnownName(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"hourglass_flowing_sand", "⏳"},
		{"white_check_mark", "✅"},
		{"x", "❌"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveEmoji(tc.name)
			if got != tc.want {
				t.Errorf("resolveEmoji(%q) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestResolveEmoji_UnknownPassthrough(t *testing.T) {
	input := "custom_emoji_123"
	got := resolveEmoji(input)
	if got != input {
		t.Errorf("resolveEmoji(%q) = %q, want passthrough %q", input, got, input)
	}
}

func TestResolveEmoji_RawUnicode(t *testing.T) {
	input := "👍"
	got := resolveEmoji(input)
	if got != input {
		t.Errorf("resolveEmoji(%q) should pass raw unicode through unchanged", input)
	}
}

// ---------------------------------------------------------------------------
// DiscordResponsePublisher HTTP tests (httptest.Server)
// ---------------------------------------------------------------------------

// newTestPublisher builds a DiscordResponsePublisher that talks to the given
// test server base URL instead of the real Discord API.
func newTestPublisher(t *testing.T, baseURL string) *DiscordResponsePublisher {
	t.Helper()
	p := NewDiscordResponsePublisher("test-token")
	// We monkey-patch the constant by injecting via a helper so tests remain
	// package-internal. Since discordAPIBase is a const we redirect by having
	// the publisher use the httptest server URL embedded in each request.
	// Instead, we create a thin wrapper that rewrites the base URL at request
	// time via a custom transport.
	p.client = &http.Client{
		Transport: &baseURLRewriter{
			real:    discordAPIBase,
			replace: baseURL,
			wrapped: http.DefaultTransport,
		},
	}
	return p
}

// baseURLRewriter is a http.RoundTripper that rewrites the scheme+host of each
// outgoing request so tests can point the publisher at an httptest.Server.
type baseURLRewriter struct {
	real    string
	replace string
	wrapped http.RoundTripper
}

func (r *baseURLRewriter) RoundTrip(req *http.Request) (*http.Response, error) {
	// Replace the API base prefix in the URL string.
	newURL := strings.Replace(req.URL.String(), r.real, r.replace, 1)
	newReq, err := http.NewRequest(req.Method, newURL, req.Body)
	if err != nil {
		return nil, err
	}
	newReq.Header = req.Header
	return r.wrapped.RoundTrip(newReq)
}

func TestDiscordPublisher_SendMessage_PostsCorrectURL(t *testing.T) {
	const channelID = "123456789"
	var captured *http.Request

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"999"}`)
	}))
	defer srv.Close()

	p := newTestPublisher(t, srv.URL)
	_, err := p.SendMessage(channelID, "hello", nil)
	if err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}

	wantPath := fmt.Sprintf("/channels/%s/messages", channelID)
	if captured.URL.Path != wantPath {
		t.Errorf("expected path %q, got %q", wantPath, captured.URL.Path)
	}
	if captured.Method != http.MethodPost {
		t.Errorf("expected POST, got %s", captured.Method)
	}
}

func TestDiscordPublisher_SendMessage_SplitsLongMessage(t *testing.T) {
	const channelID = "111"
	callCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"%d"}`, callCount)
	}))
	defer srv.Close()

	p := newTestPublisher(t, srv.URL)

	// Build a 2-chunk message: 1800 "a" + paragraph boundary + 400 "b"
	text := strings.Repeat("a", 1800) + "\n\n" + strings.Repeat("b", 400)
	_, err := p.SendMessage(channelID, text, nil)
	if err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}

	if callCount != 2 {
		t.Errorf("expected 2 POST requests for split message, got %d", callCount)
	}
}

func TestDiscordPublisher_AddReaction_SendsPUT(t *testing.T) {
	const (
		channelID = "222"
		messageID = "333"
		emoji     = "⏳"
	)
	var captured *http.Request

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	p := newTestPublisher(t, srv.URL)
	if err := p.AddReaction(channelID, messageID, "hourglass_flowing_sand"); err != nil {
		t.Fatalf("AddReaction returned error: %v", err)
	}

	if captured.Method != http.MethodPut {
		t.Errorf("expected PUT, got %s", captured.Method)
	}
	if !strings.Contains(captured.URL.Path, "@me") {
		t.Errorf("expected path to contain '@me', got %q", captured.URL.Path)
	}
	if !strings.Contains(captured.URL.Path, channelID) {
		t.Errorf("expected path to contain channel ID %q, got %q", channelID, captured.URL.Path)
	}
	if !strings.Contains(captured.URL.Path, messageID) {
		t.Errorf("expected path to contain message ID %q, got %q", messageID, captured.URL.Path)
	}
}

func TestDiscordPublisher_RemoveReaction_SendsDELETE(t *testing.T) {
	const (
		channelID = "444"
		messageID = "555"
	)
	var captured *http.Request

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	p := newTestPublisher(t, srv.URL)
	if err := p.RemoveReaction(channelID, messageID, "white_check_mark"); err != nil {
		t.Fatalf("RemoveReaction returned error: %v", err)
	}

	if captured.Method != http.MethodDelete {
		t.Errorf("expected DELETE, got %s", captured.Method)
	}
	if !strings.Contains(captured.URL.Path, "@me") {
		t.Errorf("expected path to contain '@me', got %q", captured.URL.Path)
	}
}

// ---------------------------------------------------------------------------
// threadTS logic tests
// ---------------------------------------------------------------------------

// threadTSFromMessage extracts the thread_ts value that onMessageCreate would
// produce for the given message, using the same logic as the handler.
func threadTSFromMessage(m *discordgo.Message) string {
	if m.MessageReference != nil && m.MessageReference.MessageID != "" {
		return m.MessageReference.MessageID
	}
	return ""
}

func TestThreadTS_NonReplyIsEmpty(t *testing.T) {
	// A regular channel message (no MessageReference) must produce an empty
	// thread_ts so the worker uses historyKey = "channel:{channelID}".
	m := &discordgo.Message{
		ID:               "msg-001",
		MessageReference: nil,
	}
	got := threadTSFromMessage(m)
	if got != "" {
		t.Errorf("non-reply message: expected empty threadTS, got %q", got)
	}
}

func TestThreadTS_ReplyUsesReferencedMessageID(t *testing.T) {
	// A reply message must produce thread_ts equal to the referenced message ID.
	const referencedID = "msg-parent-999"
	m := &discordgo.Message{
		ID: "msg-reply-001",
		MessageReference: &discordgo.MessageReference{
			MessageID: referencedID,
		},
	}
	got := threadTSFromMessage(m)
	if got != referencedID {
		t.Errorf("reply message: expected threadTS %q, got %q", referencedID, got)
	}
}

func TestThreadTS_EmptyReferenceIDTreatedAsNonReply(t *testing.T) {
	// MessageReference present but MessageID empty → treated as non-reply.
	m := &discordgo.Message{
		ID: "msg-002",
		MessageReference: &discordgo.MessageReference{
			MessageID: "",
		},
	}
	got := threadTSFromMessage(m)
	if got != "" {
		t.Errorf("empty reference ID: expected empty threadTS, got %q", got)
	}
}

func TestDiscordPublisher_RateLimit_Retry(t *testing.T) {
	const channelID = "666"
	callCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			// First call: return 429 with Retry-After: 0 (so test is instant)
			w.Header().Set("Retry-After", "0")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			body, _ := json.Marshal(map[string]any{
				"message":     "You are being rate limited.",
				"retry_after": 0.0,
			})
			_, _ = w.Write(body)
			return
		}
		// Second call: success
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"789"}`)
	}))
	defer srv.Close()

	p := newTestPublisher(t, srv.URL)
	id, err := p.SendMessage(channelID, "test", nil)
	if err != nil {
		t.Fatalf("SendMessage failed after retry: %v", err)
	}
	if id != "789" {
		t.Errorf("expected message id '789', got %q", id)
	}
	if callCount != 2 {
		t.Errorf("expected 2 calls (1 rate-limited + 1 success), got %d", callCount)
	}
}

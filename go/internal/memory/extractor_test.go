package memory

import (
	"testing"
)

// ---------------------------------------------------------------------------
// parseExtraction
// ---------------------------------------------------------------------------

func TestParseExtraction_ValidJSON(t *testing.T) {
	raw := `[{"category":"fact","content":"The project uses Go 1.25"},{"category":"decision","content":"Use pgx for database access"}]`
	got := parseExtraction(raw)
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d", len(got))
	}
	if got[0].Category != "fact" || got[0].Content != "The project uses Go 1.25" {
		t.Errorf("unexpected first entry: %+v", got[0])
	}
	if got[1].Category != "decision" {
		t.Errorf("unexpected second category: %q", got[1].Category)
	}
}

func TestParseExtraction_MarkdownCodeBlock(t *testing.T) {
	raw := "```json\n[{\"category\":\"preference\",\"content\":\"user likes concise answers\"}]\n```"
	got := parseExtraction(raw)
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d", len(got))
	}
	if got[0].Content != "user likes concise answers" {
		t.Errorf("unexpected content: %q", got[0].Content)
	}
}

func TestParseExtraction_MarkdownCodeBlockNoLang(t *testing.T) {
	raw := "```\n[{\"category\":\"fact\",\"content\":\"server runs on port 8080\"}]\n```"
	got := parseExtraction(raw)
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d", len(got))
	}
}

func TestParseExtraction_InvalidJSON(t *testing.T) {
	raw := `not json at all`
	got := parseExtraction(raw)
	if len(got) != 0 {
		t.Errorf("want empty slice, got %d entries", len(got))
	}
}

func TestParseExtraction_EmptyArray(t *testing.T) {
	raw := `[]`
	got := parseExtraction(raw)
	if len(got) != 0 {
		t.Errorf("want empty slice, got %d entries", len(got))
	}
}

func TestParseExtraction_MissingContentFiltered(t *testing.T) {
	// One entry has content, one has empty content, one is missing the field entirely.
	raw := `[
		{"category":"fact","content":"valid entry"},
		{"category":"decision","content":""},
		{"category":"context"}
	]`
	got := parseExtraction(raw)
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d: %+v", len(got), got)
	}
	if got[0].Content != "valid entry" {
		t.Errorf("unexpected content: %q", got[0].Content)
	}
}

func TestParseExtraction_WhitespaceOnlyContentFiltered(t *testing.T) {
	raw := `[{"category":"fact","content":"   "},{"category":"decision","content":"real decision"}]`
	got := parseExtraction(raw)
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d", len(got))
	}
}

func TestParseExtraction_JSONWithLeadingText(t *testing.T) {
	// LLM sometimes emits text before the JSON array.
	raw := "Here are the extracted memories:\n[{\"category\":\"fact\",\"content\":\"important fact\"}]"
	got := parseExtraction(raw)
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d", len(got))
	}
}

// ---------------------------------------------------------------------------
// inferPreferences
// ---------------------------------------------------------------------------

func TestInferPreferences_ContinuePattern(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "계속 해줘"},
		{Role: "assistant", Content: "알겠습니다, 계속하겠습니다."},
	}
	got := inferPreferences(messages)
	if !containsPreference(got, "continue") {
		t.Errorf("expected continue preference, got %+v", got)
	}
}

func TestInferPreferences_ContinuePatternVariants(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{"이어서해", "이어서 해"},
		{"보강해", "보강해줘"},
		{"계속진행", "계속 진행해"},
		{"계속하", "계속하자"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msgs := []Message{{Role: "user", Content: tc.text}}
			got := inferPreferences(msgs)
			if !containsPreference(got, "continue") {
				t.Errorf("pattern %q: expected continue preference, got %+v", tc.text, got)
			}
		})
	}
}

func TestInferPreferences_ProactivePattern(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "기다리지 말고 먼저 해줘"},
	}
	got := inferPreferences(messages)
	if !containsPreference(got, "proactive") {
		t.Errorf("expected proactive preference, got %+v", got)
	}
}

func TestInferPreferences_ProactivePatternVariants(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{"기다리지", "기다리지 마"},
		{"대기하지", "대기하지 말고"},
		{"먼저말", "먼저 말해줘"},
		{"알아서", "알아서 해"},
		{"먼저해", "먼저 해줘"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msgs := []Message{{Role: "user", Content: tc.text}}
			got := inferPreferences(msgs)
			if !containsPreference(got, "proactive") {
				t.Errorf("pattern %q: expected proactive preference, got %+v", tc.text, got)
			}
		})
	}
}

func TestInferPreferences_ShortFollowUp(t *testing.T) {
	// A short user message after a long assistant response signals follow-up behaviour.
	messages := []Message{
		{Role: "user", Content: "파일 정리해줘"},
		{Role: "assistant", Content: "네, 파일 정리를 시작하겠습니다. 먼저..."},
		{Role: "user", Content: "해"},
	}
	got := inferPreferences(messages)
	if !containsPreference(got, "followup") {
		t.Errorf("expected followup preference, got %+v", got)
	}
}

func TestInferPreferences_NoPatterns(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "현재 파이프라인 상태 알려줘"},
		{Role: "assistant", Content: "파이프라인이 정상 동작 중입니다."},
	}
	got := inferPreferences(messages)
	if len(got) != 0 {
		t.Errorf("expected no preferences, got %+v", got)
	}
}

func TestInferPreferences_Empty(t *testing.T) {
	got := inferPreferences(nil)
	if len(got) != 0 {
		t.Errorf("expected nil/empty for nil input, got %+v", got)
	}
}

func TestInferPreferences_Deduplication(t *testing.T) {
	// Multiple continue patterns in the same conversation should produce one entry.
	messages := []Message{
		{Role: "user", Content: "계속 해"},
		{Role: "assistant", Content: "계속하겠습니다."},
		{Role: "user", Content: "이어서 해줘"},
	}
	got := inferPreferences(messages)
	count := 0
	for _, m := range got {
		if m.Category == "preference" && m.Content == "User prefers that work continues without waiting for confirmation" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("want exactly 1 continue preference, got %d: %+v", count, got)
	}
}

func TestInferPreferences_ProactiveDeduplication(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "기다리지 말고 대기하지 마"},
	}
	got := inferPreferences(messages)
	count := 0
	for _, m := range got {
		if m.Content == "User wants proactive responses without being asked first" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("want exactly 1 proactive preference, got %d: %+v", count, got)
	}
}

func TestInferPreferences_ShortFollowUpNotTriggedForUserFirst(t *testing.T) {
	// Short message as first turn (no preceding assistant message) should not trigger.
	messages := []Message{
		{Role: "user", Content: "해"},
	}
	got := inferPreferences(messages)
	if containsPreference(got, "followup") {
		t.Errorf("should not detect followup when there is no preceding assistant message: %+v", got)
	}
}

// ---------------------------------------------------------------------------
// buildConversationText
// ---------------------------------------------------------------------------

func TestBuildConversationText_FiveMessages(t *testing.T) {
	messages := make([]Message, 5)
	for i := range messages {
		messages[i] = Message{Role: "user", Content: "message"}
	}
	text := buildConversationText(messages)
	lines := nonEmptyLines(text)
	if len(lines) != 5 {
		t.Errorf("want 5 lines, got %d", len(lines))
	}
}

func TestBuildConversationText_FifteenMessagesOnlyLast10(t *testing.T) {
	messages := make([]Message, 15)
	for i := range messages {
		messages[i] = Message{Role: "user", Content: "msg"}
	}
	// Mark the first 5 with distinctive content that should be excluded.
	for i := 0; i < 5; i++ {
		messages[i].Content = "excluded"
	}
	text := buildConversationText(messages)
	if contains(text, "excluded") {
		t.Errorf("early messages should not appear in output (only last 10 expected)")
	}
	lines := nonEmptyLines(text)
	if len(lines) != 10 {
		t.Errorf("want 10 lines for 15 messages, got %d", len(lines))
	}
}

func TestBuildConversationText_LongMessageTruncated(t *testing.T) {
	longContent := repeatRune('A', maxMessageRunes+100)
	messages := []Message{
		{Role: "user", Content: longContent},
	}
	text := buildConversationText(messages)
	// The formatted line is "User: " + content, so rune count of content portion
	// should equal maxMessageRunes.
	// Find the content after "User: "
	prefix := "User: "
	idx := indexString(text, prefix)
	if idx < 0 {
		t.Fatal("expected 'User: ' prefix in output")
	}
	content := text[idx+len(prefix):]
	// Strip trailing newline.
	content = trimRight(content, "\n")
	if runeCount(content) != maxMessageRunes {
		t.Errorf("want %d runes, got %d", maxMessageRunes, runeCount(content))
	}
}

func TestBuildConversationText_EmptyMessagesFiltered(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: ""},
		{Role: "assistant", Content: "   "},
		{Role: "user", Content: "real message"},
	}
	text := buildConversationText(messages)
	lines := nonEmptyLines(text)
	if len(lines) != 1 {
		t.Errorf("want 1 line (non-empty messages only), got %d", len(lines))
	}
}

func TestBuildConversationText_RoleLabels(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "world"},
	}
	text := buildConversationText(messages)
	if !contains(text, "User: hello") {
		t.Errorf("expected 'User: hello' in output:\n%s", text)
	}
	if !contains(text, "Assistant: world") {
		t.Errorf("expected 'Assistant: world' in output:\n%s", text)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// containsPreference reports whether any entry matches the given key.
// key is one of "continue", "proactive", "followup".
func containsPreference(mems []extractedMemory, key string) bool {
	var want string
	switch key {
	case "continue":
		want = "User prefers that work continues without waiting for confirmation"
	case "proactive":
		want = "User wants proactive responses without being asked first"
	case "followup":
		want = "User often sends short follow-up messages to continue work"
	}
	for _, m := range mems {
		if m.Category == "preference" && m.Content == want {
			return true
		}
	}
	return false
}

func nonEmptyLines(s string) []string {
	var lines []string
	for _, l := range splitLines(s) {
		if trimRight(l, " \t\r\n") != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i, c := range s {
		if c == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func contains(s, substr string) bool {
	return indexString(s, substr) >= 0
}

func indexString(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func trimRight(s, cutset string) string {
	for len(s) > 0 {
		last := s[len(s)-1]
		found := false
		for i := 0; i < len(cutset); i++ {
			if cutset[i] == last {
				found = true
				break
			}
		}
		if !found {
			break
		}
		s = s[:len(s)-1]
	}
	return s
}

func repeatRune(r rune, n int) string {
	runes := make([]rune, n)
	for i := range runes {
		runes[i] = r
	}
	return string(runes)
}

func runeCount(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

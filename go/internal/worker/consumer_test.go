package worker

import (
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

// ---------------------------------------------------------------------------
// Stream key constant tests
// ---------------------------------------------------------------------------

func TestStreamKeyConstants(t *testing.T) {
	t.Parallel()

	if StreamKey == "" {
		t.Error("StreamKey is empty")
	}
	if LegacyStreamKey == "" {
		t.Error("LegacyStreamKey is empty")
	}
	if StreamKey == LegacyStreamKey {
		t.Errorf("StreamKey (%q) should differ from LegacyStreamKey (%q)", StreamKey, LegacyStreamKey)
	}
}

func TestStreamKeyIsPlatformMessages(t *testing.T) {
	t.Parallel()

	const want = "customclaw:platform-messages"
	if StreamKey != want {
		t.Errorf("StreamKey = %q, want %q", StreamKey, want)
	}
}

func TestLegacyStreamKeyIsOldValue(t *testing.T) {
	t.Parallel()

	const want = "customclaw:slack-messages"
	if LegacyStreamKey != want {
		t.Errorf("LegacyStreamKey = %q, want %q", LegacyStreamKey, want)
	}
}

func TestStreamKeyNotContainSlack(t *testing.T) {
	t.Parallel()

	if strings.Contains(StreamKey, "slack") {
		t.Errorf("StreamKey %q should not contain 'slack'", StreamKey)
	}
}

func TestLegacyStreamKeyContainsSlack(t *testing.T) {
	t.Parallel()

	if !strings.Contains(LegacyStreamKey, "slack") {
		t.Errorf("LegacyStreamKey %q should contain 'slack' (legacy Slack stream name)", LegacyStreamKey)
	}
}

func TestStreamKeyPrefix(t *testing.T) {
	t.Parallel()

	const prefix = "customclaw:"
	if !strings.HasPrefix(StreamKey, prefix) {
		t.Errorf("StreamKey %q should start with %q", StreamKey, prefix)
	}
	if !strings.HasPrefix(LegacyStreamKey, prefix) {
		t.Errorf("LegacyStreamKey %q should start with %q", LegacyStreamKey, prefix)
	}
}

// ---------------------------------------------------------------------------
// decodeStreamMessage tests
// ---------------------------------------------------------------------------

func TestDecodeStreamMessage_AllFields(t *testing.T) {
	t.Parallel()

	msg := redis.XMessage{
		ID: "1700000000000-0",
		Values: map[string]any{
			"bot_id":             "bot1",
			"channel_id":         "C123",
			"user_id":            "U456",
			"thread_ts":          "111.222",
			"message_ts":         "333.444",
			"text":               "hello world",
			"platform":           "slack",
			"bot_token":          "xoxb-test",
			"event_type":         "create",
			"platform_message_id": "PM001",
		},
	}

	got := decodeStreamMessage(msg)

	if got.StreamID != "1700000000000-0" {
		t.Errorf("StreamID = %q, want %q", got.StreamID, "1700000000000-0")
	}
	if got.BotID != "bot1" {
		t.Errorf("BotID = %q, want %q", got.BotID, "bot1")
	}
	if got.ChannelID != "C123" {
		t.Errorf("ChannelID = %q, want %q", got.ChannelID, "C123")
	}
	if got.UserID != "U456" {
		t.Errorf("UserID = %q, want %q", got.UserID, "U456")
	}
	if got.ThreadID != "111.222" {
		t.Errorf("ThreadID = %q, want %q (thread_ts key)", got.ThreadID, "111.222")
	}
	if got.MessageID != "333.444" {
		t.Errorf("MessageID = %q, want %q (message_ts key)", got.MessageID, "333.444")
	}
	if got.Text != "hello world" {
		t.Errorf("Text = %q, want %q", got.Text, "hello world")
	}
	if got.Platform != "slack" {
		t.Errorf("Platform = %q, want %q", got.Platform, "slack")
	}
	if got.BotToken != "xoxb-test" {
		t.Errorf("BotToken = %q, want %q", got.BotToken, "xoxb-test")
	}
	if got.EventType != "create" {
		t.Errorf("EventType = %q, want %q", got.EventType, "create")
	}
	if got.PlatformMsgID != "PM001" {
		t.Errorf("PlatformMsgID = %q, want %q", got.PlatformMsgID, "PM001")
	}
}

func TestDecodeStreamMessage_EmptyValues(t *testing.T) {
	t.Parallel()

	msg := redis.XMessage{
		ID:     "0-0",
		Values: map[string]any{},
	}

	got := decodeStreamMessage(msg)

	if got.StreamID != "0-0" {
		t.Errorf("StreamID = %q, want %q", got.StreamID, "0-0")
	}
	if got.BotID != "" {
		t.Errorf("BotID = %q, want empty for missing key", got.BotID)
	}
	if got.ChannelID != "" {
		t.Errorf("ChannelID = %q, want empty for missing key", got.ChannelID)
	}
	if got.Text != "" {
		t.Errorf("Text = %q, want empty for missing key", got.Text)
	}
	if got.EventType != "" {
		t.Errorf("EventType = %q, want empty for missing key", got.EventType)
	}
}

func TestDecodeStreamMessage_LegacyThreadTSKey(t *testing.T) {
	t.Parallel()

	// Python adapters write "thread_ts" not "thread_id" — verify the mapping.
	msg := redis.XMessage{
		ID: "1-0",
		Values: map[string]any{
			"thread_ts":  "1234.5678",
			"message_ts": "9876.5432",
		},
	}

	got := decodeStreamMessage(msg)

	if got.ThreadID != "1234.5678" {
		t.Errorf("ThreadID = %q, want %q (mapped from thread_ts)", got.ThreadID, "1234.5678")
	}
	if got.MessageID != "9876.5432" {
		t.Errorf("MessageID = %q, want %q (mapped from message_ts)", got.MessageID, "9876.5432")
	}
}

func TestDecodeStreamMessage_NonStringValuesIgnored(t *testing.T) {
	t.Parallel()

	// Ensures type assertion for non-string values returns empty string gracefully.
	msg := redis.XMessage{
		ID: "2-0",
		Values: map[string]any{
			"bot_id":  42,    // int, not string
			"user_id": true,  // bool, not string
			"text":    nil,   // nil
		},
	}

	got := decodeStreamMessage(msg)

	if got.BotID != "" {
		t.Errorf("BotID = %q, want empty for non-string int value", got.BotID)
	}
	if got.UserID != "" {
		t.Errorf("UserID = %q, want empty for non-string bool value", got.UserID)
	}
	if got.Text != "" {
		t.Errorf("Text = %q, want empty for nil value", got.Text)
	}
}

func TestDecodeStreamMessage_EventTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		eventType string
	}{
		{"create event", "create"},
		{"delete event", "delete"},
		{"edit event", "edit"},
		{"empty event", ""},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			msg := redis.XMessage{
				ID:     "1-0",
				Values: map[string]any{"event_type": tc.eventType},
			}
			got := decodeStreamMessage(msg)
			if got.EventType != tc.eventType {
				t.Errorf("EventType = %q, want %q", got.EventType, tc.eventType)
			}
		})
	}
}

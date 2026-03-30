package notify

import (
	"context"
	"testing"
)

func TestLogNotifier_SendMessage(t *testing.T) {
	n := NewLogNotifier()
	ts, err := n.SendMessage(context.Background(), "C123", "hello", "")
	if err != nil {
		t.Errorf("LogNotifier.SendMessage() error = %v, want nil", err)
	}
	if ts != "" {
		t.Errorf("LogNotifier.SendMessage() ts = %q, want empty", ts)
	}
}

func TestLogNotifier_SendMessageWithThread(t *testing.T) {
	n := NewLogNotifier()
	ts, err := n.SendMessage(context.Background(), "C123", "reply", "111.222")
	if err != nil {
		t.Errorf("LogNotifier.SendMessage() error = %v, want nil", err)
	}
	if ts != "" {
		t.Errorf("LogNotifier.SendMessage() ts = %q, want empty", ts)
	}
}

func TestLogNotifier_SendMessage_EmptyChannel(t *testing.T) {
	n := NewLogNotifier()
	ts, err := n.SendMessage(context.Background(), "", "text with no channel", "")
	if err != nil {
		t.Errorf("LogNotifier.SendMessage() error = %v, want nil", err)
	}
	if ts != "" {
		t.Errorf("LogNotifier.SendMessage() ts = %q, want empty", ts)
	}
}

func TestLogNotifier_SendMessage_EmptyText(t *testing.T) {
	n := NewLogNotifier()
	ts, err := n.SendMessage(context.Background(), "C123", "", "")
	if err != nil {
		t.Errorf("LogNotifier.SendMessage() error = %v, want nil", err)
	}
	if ts != "" {
		t.Errorf("LogNotifier.SendMessage() ts = %q, want empty", ts)
	}
}

func TestLogNotifier_AddReaction(t *testing.T) {
	n := NewLogNotifier()
	err := n.AddReaction(context.Background(), "C123", "111.222", "thumbsup")
	if err != nil {
		t.Errorf("LogNotifier.AddReaction() error = %v, want nil", err)
	}
}

func TestLogNotifier_AddReaction_EmptyFields(t *testing.T) {
	n := NewLogNotifier()
	err := n.AddReaction(context.Background(), "", "", "")
	if err != nil {
		t.Errorf("LogNotifier.AddReaction() error = %v, want nil", err)
	}
}

func TestNew_EmptyToken_ReturnsLogNotifier(t *testing.T) {
	n := New("", "C123")
	if _, ok := n.(*LogNotifier); !ok {
		t.Errorf("New(\"\", ...) returned %T, want *LogNotifier", n)
	}
}

func TestNew_WithToken_ReturnsSlackNotifier(t *testing.T) {
	n := New("xoxb-test", "C123")
	if _, ok := n.(*SlackNotifier); !ok {
		t.Errorf("New(\"xoxb-test\", ...) returned %T, want *SlackNotifier", n)
	}
}

func TestNew_WithTokenAndEmptyChannel_ReturnsSlackNotifier(t *testing.T) {
	n := New("xoxb-test", "")
	if _, ok := n.(*SlackNotifier); !ok {
		t.Errorf("New(\"xoxb-test\", \"\") returned %T, want *SlackNotifier", n)
	}
}

func TestNew_EmptyTokenAndChannel_ReturnsLogNotifier(t *testing.T) {
	n := New("", "")
	if _, ok := n.(*LogNotifier); !ok {
		t.Errorf("New(\"\", \"\") returned %T, want *LogNotifier", n)
	}
}

func TestTruncate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		n     int
		want  string
	}{
		{"short string", "hello", 10, "hello"},
		{"long string", "hello world", 5, "hello..."},
		{"empty string", "", 5, ""},
		{"exact length", "abc", 3, "abc"},
		{"one over limit", "abcd", 3, "abc..."},
		{"long text with unicode", "hello", 3, "hel..."},
		{"zero limit", "abc", 0, "..."},
		{"single char over limit", "ab", 1, "a..."},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := truncate(tt.input, tt.n)
			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.n, got, tt.want)
			}
		})
	}
}

func TestNotifierInterface(t *testing.T) {
	// Compile-time interface satisfaction checks.
	var _ Notifier = (*LogNotifier)(nil)
	var _ Notifier = (*SlackNotifier)(nil)
}

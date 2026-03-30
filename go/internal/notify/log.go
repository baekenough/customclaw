package notify

import (
	"context"
	"log/slog"
)

// LogNotifier logs notifications instead of sending them.
// Used as a fallback when no platform credentials are configured.
type LogNotifier struct{}

// NewLogNotifier creates a LogNotifier.
func NewLogNotifier() *LogNotifier {
	return &LogNotifier{}
}

func (l *LogNotifier) SendMessage(_ context.Context, channel, text, _ string) (string, error) {
	slog.Info("notify/log: message", "channel", channel, "text", truncate(text, 200))
	return "", nil
}

func (l *LogNotifier) AddReaction(_ context.Context, channel, timestamp, emoji string) error {
	slog.Debug("notify/log: reaction", "emoji", emoji, "channel", channel, "ts", timestamp)
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

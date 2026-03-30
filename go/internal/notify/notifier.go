// Package notify provides a pluggable notification interface.
// Implementations deliver messages to Slack, log, or other backends.
package notify

import "context"

// Notifier sends notifications to a messaging platform.
// All methods are best-effort: implementations should log errors
// rather than returning them for non-critical notification failures.
type Notifier interface {
	// SendMessage posts a message to the given channel.
	// Returns the message timestamp/ID for threading, or "" on failure.
	SendMessage(ctx context.Context, channel, text string, threadTS string) (string, error)

	// AddReaction adds an emoji reaction to a message.
	AddReaction(ctx context.Context, channel, timestamp, emoji string) error
}

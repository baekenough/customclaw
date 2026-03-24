// Package platform defines the platform-agnostic adapter and publisher interfaces.
package platform

import "context"

// MessageEvent is a normalised, platform-agnostic representation of an
// incoming message from any supported messaging platform.
type MessageEvent struct {
	// Platform is the originating platform identifier, e.g. "slack", "discord".
	Platform  string
	ChannelID string
	UserID    string
	Text      string
	// ThreadID is the root message identifier for threaded replies.
	// It is empty when the message starts a new thread.
	ThreadID  string
	MessageID string
	// Raw holds the original, unmodified platform event payload.
	Raw       map[string]any
}

// PlatformAdapter receives messages from a messaging platform and publishes
// them to the Redis Stream for processing by the worker layer.
type PlatformAdapter interface {
	// Start begins listening for incoming messages.
	// It blocks until ctx is cancelled or an unrecoverable error occurs.
	Start(ctx context.Context) error
	// Stop gracefully shuts down the adapter and releases platform connections.
	Stop() error
	// AddReaction attaches an emoji reaction to the given message.
	AddReaction(channelID, messageID, emoji string) error
	// RemoveReaction detaches an emoji reaction from the given message.
	RemoveReaction(channelID, messageID, emoji string) error
}

// ResponsePublisher sends processed responses back to the originating platform.
// The worker layer calls these methods after completing LLM processing.
type ResponsePublisher interface {
	// SendMessage posts text to a channel, optionally within a thread.
	// Returns the platform-specific message ID of the sent message.
	SendMessage(channelID, text string, threadID *string) (string, error)
	// AddReaction attaches an emoji reaction to the given message.
	AddReaction(channelID, messageID, emoji string) error
	// RemoveReaction detaches an emoji reaction from the given message.
	RemoveReaction(channelID, messageID, emoji string) error
	// GetThreadReplies retrieves up to limit replies in a thread.
	// Each reply map contains at minimum "user_id", "text", and "ts" keys.
	GetThreadReplies(channelID, threadID string, limit int) ([]map[string]any, error)
}

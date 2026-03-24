package platform

import (
	"fmt"
	"log/slog"
	"sync"
)

// PublisherFactory creates and caches ResponsePublisher instances keyed by
// platform and bot token. A cached publisher is returned on subsequent calls
// with the same key, avoiding redundant client allocations.
//
// Call Clear after a hot-reload to force new publishers to be created with
// updated tokens.
type PublisherFactory struct {
	mu    sync.RWMutex
	cache map[string]ResponsePublisher // "platform:botToken" → publisher
}

// NewPublisherFactory allocates an empty PublisherFactory.
func NewPublisherFactory() *PublisherFactory {
	return &PublisherFactory{
		cache: make(map[string]ResponsePublisher),
	}
}

// Get returns a ResponsePublisher for the given platform and botToken.
// The result is cached; subsequent calls with the same arguments return the
// same instance.
//
// Supported platforms: "slack", "discord".
// Unsupported platforms return a noopPublisher that logs a warning on each call.
func (f *PublisherFactory) Get(platform, botToken string) ResponsePublisher {
	key := fmt.Sprintf("%s:%s", platform, botToken)

	f.mu.RLock()
	if p, ok := f.cache[key]; ok {
		f.mu.RUnlock()
		return p
	}
	f.mu.RUnlock()

	f.mu.Lock()
	defer f.mu.Unlock()

	// Double-check after acquiring the write lock.
	if p, ok := f.cache[key]; ok {
		return p
	}

	var p ResponsePublisher
	switch platform {
	case "slack":
		p = NewSlackResponsePublisher(botToken)
	case "discord":
		p = NewDiscordResponsePublisher(botToken)
	default:
		slog.Warn("no publisher implemented for platform, using no-op",
			"platform", platform,
		)
		p = &noopPublisher{platform: platform}
	}

	f.cache[key] = p
	return p
}

// Clear removes all cached publishers. Call this after a bot configuration
// hot-reload to ensure stale tokens are not reused.
func (f *PublisherFactory) Clear() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cache = make(map[string]ResponsePublisher)
}

// noopPublisher is a ResponsePublisher that logs a warning for every call.
// It is returned by PublisherFactory.Get for unsupported platforms so that
// callers receive a valid interface value rather than nil.
type noopPublisher struct {
	platform string
}

func (n *noopPublisher) SendMessage(channelID, _ string, _ *string) (string, error) {
	slog.Warn("noop publisher: SendMessage called for unsupported platform",
		"platform", n.platform,
		"channel_id", channelID,
	)
	return "", nil
}

func (n *noopPublisher) AddReaction(channelID, messageID, emoji string) error {
	slog.Warn("noop publisher: AddReaction called for unsupported platform",
		"platform", n.platform,
		"channel_id", channelID,
		"message_id", messageID,
		"emoji", emoji,
	)
	return nil
}

func (n *noopPublisher) RemoveReaction(channelID, messageID, emoji string) error {
	slog.Warn("noop publisher: RemoveReaction called for unsupported platform",
		"platform", n.platform,
		"channel_id", channelID,
		"message_id", messageID,
		"emoji", emoji,
	)
	return nil
}

func (n *noopPublisher) GetThreadReplies(channelID, threadID string, _ int) ([]map[string]any, error) {
	slog.Warn("noop publisher: GetThreadReplies called for unsupported platform",
		"platform", n.platform,
		"channel_id", channelID,
		"thread_id", threadID,
	)
	return nil, nil
}

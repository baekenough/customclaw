package platform

import (
	"fmt"

	"github.com/slack-go/slack"
)

// SlackResponsePublisher implements ResponsePublisher for the Slack platform.
// It wraps a *slack.Client and delegates each operation to the Web API.
type SlackResponsePublisher struct {
	client *slack.Client
}

// NewSlackResponsePublisher creates a SlackResponsePublisher using the given
// Slack bot token.
func NewSlackResponsePublisher(botToken string) *SlackResponsePublisher {
	return &SlackResponsePublisher{client: slack.New(botToken)}
}

// SendMessage posts text to channelID, optionally within a thread identified by
// threadID. It returns the timestamp of the sent message, which Slack uses as a
// unique message identifier.
func (p *SlackResponsePublisher) SendMessage(channelID, text string, threadID *string) (string, error) {
	opts := []slack.MsgOption{slack.MsgOptionText(text, false)}
	if threadID != nil && *threadID != "" {
		opts = append(opts, slack.MsgOptionTS(*threadID))
	}

	_, ts, err := p.client.PostMessage(channelID, opts...)
	if err != nil {
		return "", fmt.Errorf("slack post message: %w", err)
	}
	return ts, nil
}

// AddReaction attaches an emoji reaction to the message identified by
// messageID (a Slack timestamp) in channelID.
func (p *SlackResponsePublisher) AddReaction(channelID, messageID, emoji string) error {
	ref := slack.ItemRef{Channel: channelID, Timestamp: messageID}
	if err := p.client.AddReaction(emoji, ref); err != nil {
		return fmt.Errorf("slack add reaction: %w", err)
	}
	return nil
}

// RemoveReaction detaches an emoji reaction from the message identified by
// messageID in channelID.
func (p *SlackResponsePublisher) RemoveReaction(channelID, messageID, emoji string) error {
	ref := slack.ItemRef{Channel: channelID, Timestamp: messageID}
	if err := p.client.RemoveReaction(emoji, ref); err != nil {
		return fmt.Errorf("slack remove reaction: %w", err)
	}
	return nil
}

// GetThreadReplies retrieves up to limit replies from the thread rooted at
// threadID in channelID. Each reply is returned as a map with at minimum
// "user_id", "text", and "ts" keys. The root message is excluded.
func (p *SlackResponsePublisher) GetThreadReplies(channelID, threadID string, limit int) ([]map[string]any, error) {
	params := &slack.GetConversationRepliesParameters{
		ChannelID: channelID,
		Timestamp: threadID,
		Limit:     limit,
	}

	msgs, _, _, err := p.client.GetConversationReplies(params)
	if err != nil {
		return nil, fmt.Errorf("slack get conversation replies: %w", err)
	}

	// The first message in the response is always the root; skip it.
	if len(msgs) > 0 {
		msgs = msgs[1:]
	}

	replies := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		replies = append(replies, map[string]any{
			"user_id": m.User,
			"text":    m.Text,
			"ts":      m.Timestamp,
		})
	}
	return replies, nil
}

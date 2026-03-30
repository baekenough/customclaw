package notify

import (
	"context"
	"log/slog"

	"github.com/slack-go/slack"
)

// SlackNotifier sends notifications via the Slack Web API.
type SlackNotifier struct {
	client  *slack.Client
	channel string // default channel
}

// NewSlackNotifier creates a Slack-backed notifier.
// If token is empty, callers should use LogNotifier instead.
func NewSlackNotifier(token, defaultChannel string) *SlackNotifier {
	return &SlackNotifier{
		client:  slack.New(token),
		channel: defaultChannel,
	}
}

func (s *SlackNotifier) SendMessage(ctx context.Context, channel, text, threadTS string) (string, error) {
	ch := channel
	if ch == "" {
		ch = s.channel
	}
	if ch == "" {
		slog.Warn("notify/slack: no channel specified, skipping")
		return "", nil
	}

	opts := []slack.MsgOption{
		slack.MsgOptionText(text, false),
		slack.MsgOptionDisableLinkUnfurl(),
	}
	if threadTS != "" {
		opts = append(opts, slack.MsgOptionTS(threadTS))
	}

	_, ts, err := s.client.PostMessageContext(ctx, ch, opts...)
	if err != nil {
		slog.Warn("notify/slack: post message failed", "error", err)
		return "", err
	}
	return ts, nil
}

func (s *SlackNotifier) AddReaction(ctx context.Context, channel, timestamp, emoji string) error {
	ch := channel
	if ch == "" {
		ch = s.channel
	}
	err := s.client.AddReactionContext(ctx, emoji, slack.ItemRef{
		Channel:   ch,
		Timestamp: timestamp,
	})
	if err != nil {
		slog.Debug("notify/slack: add reaction failed", "error", err)
	}
	return err
}

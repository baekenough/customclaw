// Package platform provides messaging platform adapters and response publishers.
package platform

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/redis/go-redis/v9"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"

	"github.com/baekenough/customclaw/internal/config"
)

const slackStreamKey = "customclaw:slack-messages"

// SlackAdapter listens for Slack messages via Socket Mode and publishes them
// to the Redis Stream for downstream processing.
//
// One SlackAdapter is created per unique app_token. Multiple BotConfigs that
// share the same app_token are multiplexed over a single Socket Mode connection.
type SlackAdapter struct {
	configs    []*config.BotConfig
	rdb        *redis.Client
	appToken   string
	channelMap map[string]*config.BotConfig // channel_id → config
	defaultCfg *config.BotConfig
	client     *slack.Client
	smClient   *socketmode.Client
	stopOnce   sync.Once
	stopCh     chan struct{}
}

// NewSlackAdapter creates a SlackAdapter for the given set of BotConfigs that
// share the same SlackAppToken. rdb is used to publish incoming messages.
//
// configs must be non-empty. The first config's tokens are used for the Socket
// Mode connection; each config's security settings govern channel routing.
func NewSlackAdapter(configs []*config.BotConfig, rdb *redis.Client) *SlackAdapter {
	if len(configs) == 0 {
		panic("platform: NewSlackAdapter requires at least one BotConfig")
	}

	primary := configs[0]
	channelMap := make(map[string]*config.BotConfig, len(configs)*4)

	var defaultCfg *config.BotConfig
	for _, cfg := range configs {
		if len(cfg.Security.AllowedChannels) == 0 {
			// Config with no channel restrictions becomes the default fallback.
			defaultCfg = cfg
			continue
		}
		for _, ch := range cfg.Security.AllowedChannels {
			channelMap[ch] = cfg
		}
	}

	api := slack.New(
		primary.SlackBotToken,
		slack.OptionAppLevelToken(primary.SlackAppToken),
	)
	sm := socketmode.New(api)

	return &SlackAdapter{
		configs:    configs,
		rdb:        rdb,
		appToken:   primary.SlackAppToken,
		channelMap: channelMap,
		defaultCfg: defaultCfg,
		client:     api,
		smClient:   sm,
		stopCh:     make(chan struct{}),
	}
}

// Start connects to Slack via Socket Mode and blocks until ctx is cancelled or
// an unrecoverable error occurs. It handles incoming message events by applying
// security checks, adding an hourglass reaction, and publishing to Redis.
func (a *SlackAdapter) Start(ctx context.Context) error {
	errCh := make(chan error, 1)

	go func() {
		errCh <- a.smClient.Run()
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-a.stopCh:
			return nil
		case err := <-errCh:
			return fmt.Errorf("slack socket mode: %w", err)
		case evt, ok := <-a.smClient.Events:
			if !ok {
				return nil
			}
			a.handleEvent(ctx, evt)
		}
	}
}

// Stop signals the adapter to shut down gracefully.
func (a *SlackAdapter) Stop() error {
	a.stopOnce.Do(func() {
		close(a.stopCh)
	})
	return nil
}

// AddReaction attaches an emoji reaction to a Slack message.
func (a *SlackAdapter) AddReaction(channelID, messageID, emoji string) error {
	ref := slack.ItemRef{Channel: channelID, Timestamp: messageID}
	if err := a.client.AddReaction(emoji, ref); err != nil {
		return fmt.Errorf("slack add reaction: %w", err)
	}
	return nil
}

// RemoveReaction detaches an emoji reaction from a Slack message.
func (a *SlackAdapter) RemoveReaction(channelID, messageID, emoji string) error {
	ref := slack.ItemRef{Channel: channelID, Timestamp: messageID}
	if err := a.client.RemoveReaction(emoji, ref); err != nil {
		return fmt.Errorf("slack remove reaction: %w", err)
	}
	return nil
}

// handleEvent processes a single Socket Mode event dispatched from the event loop.
func (a *SlackAdapter) handleEvent(ctx context.Context, evt socketmode.Event) {
	switch evt.Type {
	case socketmode.EventTypeEventsAPI:
		// Acknowledge the event immediately so Slack does not retry.
		a.smClient.Ack(*evt.Request)
		a.handleEventsAPI(ctx, evt)
	case socketmode.EventTypeConnected:
		tokenPrefix := a.appToken
		if len(tokenPrefix) > 8 {
			tokenPrefix = tokenPrefix[:8]
		}
		slog.Info("slack socket mode connected", "app_token_prefix", tokenPrefix)
	default:
		// Acknowledge and discard unhandled event types.
		if evt.Request != nil {
			a.smClient.Ack(*evt.Request)
		}
	}
}

// handleEventsAPI dispatches Events API callback payloads to the message handler.
func (a *SlackAdapter) handleEventsAPI(ctx context.Context, evt socketmode.Event) {
	apiEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
	if !ok {
		return
	}
	if apiEvent.Type != slackevents.CallbackEvent {
		return
	}

	msgEvent, ok := apiEvent.InnerEvent.Data.(*slackevents.MessageEvent)
	if !ok {
		return
	}

	a.handleMessage(ctx, msgEvent)
}

// handleMessage applies security checks and publishes a valid message event to
// the Redis Stream.
func (a *SlackAdapter) handleMessage(ctx context.Context, ev *slackevents.MessageEvent) {
	// Skip message subtypes (edits, deletions, thread broadcasts, etc.).
	if ev.SubType != "" {
		return
	}
	// Skip bot messages.
	if ev.BotID != "" {
		return
	}

	cfg := a.resolveConfig(ev.Channel)
	if cfg == nil {
		slog.Debug("slack message from unregistered channel, ignoring", "channel", ev.Channel)
		return
	}

	// Enforce allowed_users if configured.
	if len(cfg.Security.AllowedUsers) > 0 && !contains(cfg.Security.AllowedUsers, ev.User) {
		slog.Debug("slack message from disallowed user, ignoring",
			"channel", ev.Channel,
			"user", ev.User,
			"bot_id", cfg.ID,
		)
		return
	}

	// Add hourglass reaction to acknowledge receipt.
	ref := slack.ItemRef{Channel: ev.Channel, Timestamp: ev.TimeStamp}
	if err := a.client.AddReaction("hourglass_flowing_sand", ref); err != nil {
		// Non-fatal: log and continue publishing.
		slog.Warn("failed to add hourglass reaction",
			"channel", ev.Channel,
			"ts", ev.TimeStamp,
			"error", err,
		)
	}

	// Determine thread_ts: use ThreadTimeStamp if set (reply), else the
	// message's own timestamp (root message).
	threadTS := ev.ThreadTimeStamp
	if threadTS == "" {
		threadTS = ev.TimeStamp
	}

	fields := map[string]any{
		"bot_id":     cfg.ID,
		"channel_id": ev.Channel,
		"thread_ts":  threadTS,
		"user_id":    ev.User,
		"text":       ev.Text,
		"message_ts": ev.TimeStamp,
		"bot_token":  cfg.SlackBotToken,
		"platform":   "slack",
	}

	if err := a.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: slackStreamKey,
		Values: fields,
	}).Err(); err != nil {
		slog.Error("failed to publish slack message to redis stream",
			"channel", ev.Channel,
			"ts", ev.TimeStamp,
			"error", err,
		)
	}
}

// resolveConfig returns the most-specific BotConfig for the given channel.
// It checks the channelMap first, then falls back to the default config.
// Returns nil if no config matches and there is no default.
func (a *SlackAdapter) resolveConfig(channelID string) *config.BotConfig {
	if cfg, ok := a.channelMap[channelID]; ok {
		return cfg
	}
	return a.defaultCfg
}

// contains reports whether s is present in slice.
func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

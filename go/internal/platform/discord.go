package platform

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"
	"github.com/redis/go-redis/v9"

	"github.com/baekenough/customclaw/internal/config"
)

// discordStreamKey is the Redis Stream used for all incoming Discord messages.
// Intentionally the same key as Slack so the worker layer can consume from one
// stream regardless of platform.
const discordStreamKey = "customclaw:slack-messages"

// DiscordAdapter listens for Discord messages via the Gateway API and publishes
// them to the Redis Stream for downstream processing.
//
// One DiscordAdapter is created per unique bot token. Multiple BotConfigs that
// share the same token are multiplexed over a single Gateway connection.
type DiscordAdapter struct {
	configs    []*config.BotConfig
	rdb        *redis.Client
	session    *discordgo.Session
	channelMap map[string]*config.BotConfig // channel_id → config
	defaultCfg *config.BotConfig
	stopCh     chan struct{}
	stopped    sync.Once
}

// NewDiscordAdapter creates a DiscordAdapter for the given set of BotConfigs
// that share the same Discord token. rdb is used to publish incoming messages.
//
// configs must be non-empty. The first config's token is used for the Gateway
// connection; each config's security settings govern channel routing.
func NewDiscordAdapter(configs []*config.BotConfig, rdb *redis.Client) (*DiscordAdapter, error) {
	if len(configs) == 0 {
		return nil, fmt.Errorf("platform: NewDiscordAdapter requires at least one BotConfig")
	}

	primary := configs[0]

	channelMap := make(map[string]*config.BotConfig, len(configs)*4)
	var defaultCfg *config.BotConfig
	for _, cfg := range configs {
		if len(cfg.Security.AllowedChannels) == 0 {
			defaultCfg = cfg
			continue
		}
		for _, ch := range cfg.Security.AllowedChannels {
			channelMap[ch] = cfg
		}
	}

	session, err := discordgo.New("Bot " + primary.Discord.Token)
	if err != nil {
		return nil, fmt.Errorf("platform: create discord session: %w", err)
	}

	// Request the GuildMessages intent so the bot receives message events in
	// servers. MessageContent is required to read message text.
	session.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentMessageContent

	a := &DiscordAdapter{
		configs:    configs,
		rdb:        rdb,
		session:    session,
		channelMap: channelMap,
		defaultCfg: defaultCfg,
		stopCh:     make(chan struct{}),
	}

	session.AddHandler(a.onMessageCreate)
	session.AddHandler(a.onMessageDelete)
	session.AddHandler(a.onMessageUpdate)

	return a, nil
}

// Start connects to the Discord Gateway and blocks until ctx is cancelled or
// an unrecoverable error occurs.
func (a *DiscordAdapter) Start(ctx context.Context) error {
	if err := a.session.Open(); err != nil {
		return fmt.Errorf("discord gateway open: %w", err)
	}

	slog.Info("discord gateway connected", "bot_id", a.session.State.User.ID)

	select {
	case <-ctx.Done():
	case <-a.stopCh:
	}

	return nil
}

// Stop gracefully closes the Discord Gateway connection.
func (a *DiscordAdapter) Stop() error {
	a.stopped.Do(func() {
		close(a.stopCh)
	})
	return a.session.Close()
}

// AddReaction attaches an emoji reaction to a Discord message.
// emoji should be a Unicode emoji string (e.g. "⏳") or a custom emoji in the
// format "name:id".
func (a *DiscordAdapter) AddReaction(channelID, messageID, emoji string) error {
	if err := a.session.MessageReactionAdd(channelID, messageID, resolveEmoji(emoji)); err != nil {
		return fmt.Errorf("discord add reaction: %w", err)
	}
	return nil
}

// RemoveReaction detaches an emoji reaction from a Discord message.
func (a *DiscordAdapter) RemoveReaction(channelID, messageID, emoji string) error {
	if err := a.session.MessageReactionRemove(channelID, messageID, resolveEmoji(emoji), "@me"); err != nil {
		return fmt.Errorf("discord remove reaction: %w", err)
	}
	return nil
}

// onMessageCreate is the discordgo handler invoked for every MessageCreate event.
func (a *DiscordAdapter) onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Skip messages sent by any bot (including ourselves).
	if m.Author == nil || m.Author.Bot {
		return
	}

	cfg := a.resolveConfig(m.ChannelID)
	if cfg == nil {
		slog.Debug("discord message from unregistered channel, ignoring",
			"channel", m.ChannelID,
		)
		return
	}

	// Enforce allowed_users if configured.
	if len(cfg.Security.AllowedUsers) > 0 && !contains(cfg.Security.AllowedUsers, m.Author.ID) {
		slog.Debug("discord message from disallowed user, ignoring",
			"channel", m.ChannelID,
			"user", m.Author.ID,
			"bot_id", cfg.ID,
		)
		return
	}

	// mention_only: skip if the bot is not mentioned in this message.
	if cfg.Security.MentionOnly {
		mentioned := false
		for _, u := range m.Mentions {
			if u.ID == s.State.User.ID {
				mentioned = true
				break
			}
		}
		if !mentioned {
			return
		}
	}

	// Add hourglass reaction to acknowledge receipt.
	if err := s.MessageReactionAdd(m.ChannelID, m.ID, resolveEmoji("hourglass_flowing_sand")); err != nil {
		slog.Warn("failed to add hourglass reaction",
			"channel", m.ChannelID,
			"message_id", m.ID,
			"error", err,
		)
	}

	// Strip the bot mention from the message text.
	text := strings.ReplaceAll(m.Content, "<@"+s.State.User.ID+">", "")
	text = strings.TrimSpace(text)

	// thread_ts: use the referenced message ID if this is a reply.
	// Non-reply messages use an empty thread_ts so the worker groups them
	// under historyKey = "channel:{channelID}", matching Slack non-threaded
	// behaviour and allowing users in the same channel to share context.
	threadTS := ""
	if m.MessageReference != nil && m.MessageReference.MessageID != "" {
		threadTS = m.MessageReference.MessageID
	}

	ctx := context.Background()
	fields := map[string]any{
		"bot_id":              cfg.ID,
		"channel_id":          m.ChannelID,
		"thread_ts":           threadTS,
		"user_id":             m.Author.ID,
		"text":                text,
		"message_ts":          m.ID,
		"bot_token":           cfg.Discord.Token,
		"platform":            "discord",
		"event_type":          "create",
		"platform_message_id": m.ID,
	}

	if err := a.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: discordStreamKey,
		Values: fields,
	}).Err(); err != nil {
		slog.Error("failed to publish discord message to redis stream",
			"channel", m.ChannelID,
			"message_id", m.ID,
			"error", err,
		)
	}
}

// onMessageDelete is the discordgo handler invoked for every MessageDelete event.
// It publishes a delete event to the Redis Stream so the worker can mark the
// message as deleted in the database.
func (a *DiscordAdapter) onMessageDelete(s *discordgo.Session, m *discordgo.MessageDelete) {
	if m.Message == nil {
		return
	}

	cfg := a.resolveConfig(m.ChannelID)
	if cfg == nil {
		slog.Debug("discord message_delete from unregistered channel, ignoring",
			"channel", m.ChannelID,
		)
		return
	}

	ctx := context.Background()
	fields := map[string]any{
		"bot_id":              cfg.ID,
		"channel_id":          m.ChannelID,
		"thread_ts":           "",
		"user_id":             "",
		"text":                "",
		"message_ts":          m.ID,
		"bot_token":           cfg.Discord.Token,
		"platform":            "discord",
		"event_type":          "delete",
		"platform_message_id": m.ID,
	}

	if err := a.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: discordStreamKey,
		Values: fields,
	}).Err(); err != nil {
		slog.Error("failed to publish discord message_delete to redis stream",
			"channel", m.ChannelID,
			"message_id", m.ID,
			"error", err,
		)
	}
}

// onMessageUpdate is the discordgo handler invoked for every MessageUpdate event.
// It publishes an edit event to the Redis Stream only when the message content
// has actually changed (EditedTimestamp is non-nil).
func (a *DiscordAdapter) onMessageUpdate(s *discordgo.Session, m *discordgo.MessageUpdate) {
	if m.Message == nil {
		return
	}
	// Only process genuine user edits; Discord sends MessageUpdate for other
	// reasons (e.g. link-preview embeds). EditedTimestamp is non-nil only for
	// content edits.
	if m.EditedTimestamp == nil {
		return
	}
	// Skip updates from bots.
	if m.Author != nil && m.Author.Bot {
		return
	}

	cfg := a.resolveConfig(m.ChannelID)
	if cfg == nil {
		slog.Debug("discord message_update from unregistered channel, ignoring",
			"channel", m.ChannelID,
		)
		return
	}

	// Strip bot mention and trim whitespace from the updated content.
	text := strings.ReplaceAll(m.Content, "<@"+s.State.User.ID+">", "")
	text = strings.TrimSpace(text)

	userID := ""
	if m.Author != nil {
		userID = m.Author.ID
	}

	ctx := context.Background()
	fields := map[string]any{
		"bot_id":              cfg.ID,
		"channel_id":          m.ChannelID,
		"thread_ts":           "",
		"user_id":             userID,
		"text":                text,
		"message_ts":          m.ID,
		"bot_token":           cfg.Discord.Token,
		"platform":            "discord",
		"event_type":          "edit",
		"platform_message_id": m.ID,
	}

	if err := a.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: discordStreamKey,
		Values: fields,
	}).Err(); err != nil {
		slog.Error("failed to publish discord message_update to redis stream",
			"channel", m.ChannelID,
			"message_id", m.ID,
			"error", err,
		)
	}
}

// resolveConfig returns the most-specific BotConfig for the given channel.
// It checks the channelMap first, then falls back to the default config.
// Returns nil if no config matches and there is no default.
func (a *DiscordAdapter) resolveConfig(channelID string) *config.BotConfig {
	if cfg, ok := a.channelMap[channelID]; ok {
		return cfg
	}
	return a.defaultCfg
}

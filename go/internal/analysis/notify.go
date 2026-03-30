package analysis

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/jackc/pgx/v5"

	"github.com/baekenough/customclaw/internal/notify"
)

const driverBotID = "omcustomdriver"

// botInfo caches the notification bot token and default channel.
type botInfo struct {
	token   string
	channel string
}

var (
	cachedNotifier notify.Notifier
	notifierOnce   sync.Once
)

// initNotifier creates the notification backend from DB or env credentials.
func initNotifier(ctx context.Context) notify.Notifier {
	info := loadBotInfo(ctx)
	return notify.New(info.token, info.channel)
}

// loadBotInfo fetches the driver bot token + first channel from PostgreSQL.
// Falls back to env vars SLACK_BOT_TOKEN / SLACK_CHANNEL on any error.
func loadBotInfo(ctx context.Context) botInfo {
	databaseDSN := os.Getenv("DATABASE_DSN")
	botID := os.Getenv("OMCUSTOM_DRIVER_BOT_ID")
	if botID == "" {
		botID = driverBotID
	}

	if databaseDSN == "" {
		slog.Warn("notify: DATABASE_DSN not set, cannot fetch driver bot info")
		return envBotInfo()
	}

	conn, err := pgx.Connect(ctx, databaseDSN)
	if err != nil {
		slog.Warn("notify: db connect failed", "error", err)
		return envBotInfo()
	}
	defer func() { _ = conn.Close(ctx) }()

	var token string
	var channels []string
	// Query the platform-agnostic credentials column first; fall back to the
	// legacy slack_bot_token column so existing rows keep working.
	err = conn.QueryRow(ctx,
		`SELECT COALESCE(credentials->>'bot_token', slack_bot_token, ''), channels
		 FROM bots WHERE id = $1 AND is_active = true`,
		botID,
	).Scan(&token, &channels)
	if err != nil {
		slog.Warn("notify: driver bot not found or query error", "bot_id", botID, "error", err)
		return envBotInfo()
	}

	channel := ""
	if len(channels) > 0 {
		channel = channels[0]
	}
	return botInfo{token: token, channel: channel}
}

// envBotInfo returns bot info sourced from environment variables (fallback).
// Prefers ALERT_BOT_TOKEN / ALERT_CHANNEL; falls back to legacy Slack-specific names.
func envBotInfo() botInfo {
	token := os.Getenv("ALERT_BOT_TOKEN")
	if token == "" {
		token = os.Getenv("SLACK_BOT_TOKEN") // legacy fallback
	}
	channel := os.Getenv("ALERT_CHANNEL")
	if channel == "" {
		channel = os.Getenv("SLACK_CHANNEL") // legacy fallback
	}
	return botInfo{
		token:   token,
		channel: channel,
	}
}

// sendNotification sends a notification via the configured backend.
// Best-effort: errors are logged but not returned.
// Returns the thread_ts of the posted message, or "".
// channelOverride, when non-empty, overrides the channel loaded from the database.
func sendNotification(ctx context.Context, text, issueNumber, repo, threadTS, emoji, channelOverride string) string {
	notifierOnce.Do(func() {
		cachedNotifier = initNotifier(ctx)
	})

	channel := channelOverride // override takes precedence

	msgText := text
	if issueNumber != "" && repo != "" {
		msgText += fmt.Sprintf(
			" (<https://github.com/%s/issues/%s|#%s>)",
			repo, issueNumber, issueNumber,
		)
	}

	ts, err := cachedNotifier.SendMessage(ctx, channel, msgText, threadTS)
	if err != nil {
		slog.Warn("notify: send message failed (non-blocking)", "error", err)
		return ""
	}

	if emoji != "" && ts != "" {
		reactionTS := threadTS
		if reactionTS == "" {
			reactionTS = ts
		}
		_ = cachedNotifier.AddReaction(ctx, channel, reactionTS, emoji)
	}

	return ts
}

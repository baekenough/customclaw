package analysis

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/slack-go/slack"
)

// driverBotID is the bot whose token is used for Slack notifications.
const driverBotID = "omcustomdriver"

// botInfo caches the Slack bot token and channel fetched from the database.
type botInfo struct {
	token   string
	channel string
}

var (
	cachedBotInfo botInfo
	botInfoOnce   sync.Once
)

// loadBotInfo fetches the driver bot token + first channel from PostgreSQL.
// Falls back to env vars SLACK_BOT_TOKEN / SLACK_CHANNEL on any error.
func loadBotInfo(ctx context.Context) botInfo {
	databaseDSN := os.Getenv("DATABASE_DSN")
	botID := os.Getenv("OMCUSTOM_DRIVER_BOT_ID")
	if botID == "" {
		botID = driverBotID
	}

	if databaseDSN == "" {
		slog.Warn("slack: DATABASE_DSN not set, cannot fetch driver bot info")
		return envBotInfo()
	}

	conn, err := pgx.Connect(ctx, databaseDSN)
	if err != nil {
		slog.Warn("slack: db connect failed", "error", err)
		return envBotInfo()
	}
	defer conn.Close(ctx)

	var token string
	var channels []string
	err = conn.QueryRow(ctx,
		"SELECT slack_bot_token, channels FROM bots WHERE id = $1 AND is_active = true",
		botID,
	).Scan(&token, &channels)
	if err != nil {
		slog.Warn("slack: driver bot not found or query error", "bot_id", botID, "error", err)
		return envBotInfo()
	}

	channel := ""
	if len(channels) > 0 {
		channel = channels[0]
	}
	return botInfo{token: token, channel: channel}
}

// envBotInfo returns bot info sourced from environment variables (fallback).
func envBotInfo() botInfo {
	return botInfo{
		token:   os.Getenv("SLACK_BOT_TOKEN"),
		channel: os.Getenv("SLACK_CHANNEL"),
	}
}

// notifySlack sends a Slack notification. It is best-effort: errors are logged
// but not returned. Returns the thread_ts of the posted message, or "".
func notifySlack(ctx context.Context, text, issueNumber, repo, threadTS, emoji string) string {
	botInfoOnce.Do(func() {
		cachedBotInfo = loadBotInfo(ctx)
	})

	info := cachedBotInfo
	if info.token == "" || info.channel == "" {
		slog.Warn("slack: token/channel unavailable, skipping notification")
		return ""
	}

	msgText := text
	if issueNumber != "" && repo != "" {
		msgText += fmt.Sprintf(
			" (<https://github.com/%s/issues/%s|#%s>)",
			repo, issueNumber, issueNumber,
		)
	}

	client := slack.New(info.token)

	opts := []slack.MsgOption{
		slack.MsgOptionText(msgText, false),
		slack.MsgOptionDisableLinkUnfurl(),
	}
	if threadTS != "" {
		opts = append(opts, slack.MsgOptionTS(threadTS))
	}

	_, ts, err := client.PostMessageContext(ctx, info.channel, opts...)
	if err != nil {
		slog.Warn("slack: post message failed (non-blocking)", "error", err)
		return ""
	}

	// Add emoji reaction if specified.
	if emoji != "" && ts != "" {
		reactionTS := threadTS
		if reactionTS == "" {
			reactionTS = ts
		}
		if reactErr := client.AddReactionContext(ctx, emoji,
			slack.ItemRef{Channel: info.channel, Timestamp: reactionTS},
		); reactErr != nil {
			slog.Debug("slack: add reaction failed (non-blocking)", "error", reactErr)
		}
	}

	return ts
}

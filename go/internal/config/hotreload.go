package config

import (
	"context"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// ConfigChannel is the Redis Pub/Sub channel used to broadcast bot config changes.
const ConfigChannel = "customclaw:bot-config-changed"

// StartConfigSubscriber launches a goroutine that listens for config change events
// on ConfigChannel. When a message arrives, onReload is called with the bot ID.
// The goroutine reconnects automatically on connection loss.
// It stops when ctx is cancelled.
func StartConfigSubscriber(ctx context.Context, redisURL string, onReload func(botID string)) {
	go func() {
		for {
			if err := ctx.Err(); err != nil {
				return
			}
			if err := runSubscribeLoop(ctx, redisURL, onReload); err != nil {
				if ctx.Err() != nil {
					return
				}
				slog.Warn("config subscriber error, reconnecting", "error", err, "delay", "5s")
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}()
}

// runSubscribeLoop creates a dedicated Pub/Sub connection and processes messages
// until the context is cancelled or a connection error occurs.
func runSubscribeLoop(ctx context.Context, redisURL string, onReload func(botID string)) error {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return err
	}
	rdb := redis.NewClient(opts)
	defer func() { _ = rdb.Close() }()

	ps := rdb.Subscribe(ctx, ConfigChannel)
	defer func() { _ = ps.Close() }()

	slog.Info("config hot-reload subscriber started", "channel", ConfigChannel)

	ch := ps.Channel()
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-ch:
			if !ok {
				return nil
			}
			botID := msg.Payload
			slog.Info("config reload event received", "bot_id", botID)
			func() {
				defer func() {
					if r := recover(); r != nil {
						slog.Error("panic in config reload handler", "bot_id", botID, "panic", r)
					}
				}()
				onReload(botID)
			}()
		}
	}
}

// PublishConfigChange notifies all subscribers that a bot's configuration has changed.
// This is called by bot management tools after updating the database.
func PublishConfigChange(ctx context.Context, rdb *redis.Client, botID string) error {
	return rdb.Publish(ctx, ConfigChannel, botID).Err()
}

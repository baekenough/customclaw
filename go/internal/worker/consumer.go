package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// StreamKey is the Redis Stream key where platform adapters publish messages.
	StreamKey = "customclaw:platform-messages"
	// LegacyStreamKey is the old stream key retained for dual-read migration.
	// Once all producers have migrated to StreamKey, this can be removed.
	LegacyStreamKey = "customclaw:slack-messages"
	// claimIdleTimeout is the idle duration after which unacknowledged messages
	// are re-claimed by the current consumer.
	claimIdleTimeout = 5 * time.Minute
	// readBlockDuration is how long XREADGROUP blocks waiting for new messages.
	readBlockDuration = 2 * time.Second
	// readBatchSize controls how many messages are fetched per XREADGROUP call.
	readBatchSize = 10
)

// ConsumerLoop reads messages from the Redis Stream using a consumer group
// and feeds each entry to the dispatcher. Acknowledgement (XACK) is the
// responsibility of the processFn supplied to the Dispatcher — messages are
// ACKed only after successful processing, providing at-least-once delivery.
//
// It also periodically re-claims idle pending messages (XAUTOCLAIM) so that
// messages from crashed consumers are eventually reprocessed.
func ConsumerLoop(
	ctx context.Context,
	rdb *redis.Client,
	group, consumer string,
	dispatcher *Dispatcher,
) {
	claimTicker := time.NewTicker(claimIdleTimeout / 2)
	defer claimTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-claimTicker.C:
			reclaimIdle(ctx, rdb, group, consumer, dispatcher)
		default:
		}

		entries, err := rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    group,
			Consumer: consumer,
			Streams:  []string{StreamKey, ">"},
			Count:    readBatchSize,
			Block:    readBlockDuration,
			NoAck:    false,
		}).Result()

		if err != nil {
			if err == redis.Nil || err == context.DeadlineExceeded {
				continue
			}
			if ctx.Err() != nil {
				return
			}
			slog.Error("xreadgroup error", "error", err)
			time.Sleep(time.Second)
			continue
		}

		for _, stream := range entries {
			for _, msg := range stream.Messages {
				incoming := decodeStreamMessage(msg)
				// Do not XACK here. The processFn is responsible for ACKing
				// after successful processing (at-least-once delivery).
				dispatcher.Dispatch(ctx, incoming)
			}
		}
	}
}

// reclaimIdle uses XAUTOCLAIM to take ownership of pending messages that have
// been idle longer than claimIdleTimeout.
func reclaimIdle(
	ctx context.Context,
	rdb *redis.Client,
	group, consumer string,
	dispatcher *Dispatcher,
) {
	messages, _, err := rdb.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   StreamKey,
		Group:    group,
		Consumer: consumer,
		MinIdle:  claimIdleTimeout,
		Start:    "0-0",
		Count:    readBatchSize,
	}).Result()
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("xautoclaim error", "error", err)
		}
		return
	}

	for _, msg := range messages {
		slog.Info("reclaiming idle message", "id", msg.ID)
		incoming := decodeStreamMessage(msg)
		// Do not XACK here. Reclaimed messages follow the same at-least-once
		// path: the processFn ACKs them after successful processing.
		dispatcher.Dispatch(ctx, incoming)
	}
}

// DrainLegacyStream reads remaining messages from the old "customclaw:slack-messages"
// stream key during the migration period. It joins the same consumer group so
// at-least-once semantics are preserved. Once the pending count reaches zero the
// goroutine exits — callers should start it with go and ignore the return.
//
// Safe to call even when the legacy stream does not exist: XGroupCreateMkStream
// creates it empty if absent, and the drain loop exits immediately.
func DrainLegacyStream(
	ctx context.Context,
	rdb *redis.Client,
	group, consumer string,
	dispatcher *Dispatcher,
) {
	// Ensure consumer group exists on the legacy stream.
	_ = rdb.XGroupCreateMkStream(ctx, LegacyStreamKey, group, "0").Err()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		entries, err := rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    group,
			Consumer: consumer,
			Streams:  []string{LegacyStreamKey, ">"},
			Count:    readBatchSize,
			Block:    5 * time.Second,
			NoAck:    false,
		}).Result()

		if err != nil {
			if err == redis.Nil || err == context.DeadlineExceeded {
				// No new messages; check pending count before exiting.
				pending, pendErr := rdb.XPending(ctx, LegacyStreamKey, group).Result()
				if pendErr != nil || pending.Count == 0 {
					slog.Info("legacy stream drain complete",
						"stream", LegacyStreamKey)
					return
				}
				continue
			}
			if ctx.Err() != nil {
				return
			}
			slog.Warn("legacy stream drain error", "stream", LegacyStreamKey, "error", err)
			time.Sleep(time.Second)
			continue
		}

		for _, stream := range entries {
			for _, msg := range stream.Messages {
				incoming := decodeStreamMessage(msg)
				dispatcher.Dispatch(ctx, incoming)
				// ACK immediately — legacy drain is best-effort catch-up.
				if ackErr := rdb.XAck(ctx, LegacyStreamKey, group, msg.ID).Err(); ackErr != nil {
					slog.Warn("legacy stream: xack failed", "id", msg.ID, "error", ackErr)
				}
			}
		}
	}
}

// decodeStreamMessage converts a raw Redis Stream entry to IncomingMessage.
// All values are stored as strings in the stream.
func decodeStreamMessage(msg redis.XMessage) IncomingMessage {
	get := func(key string) string {
		v, _ := msg.Values[key].(string)
		return v
	}
	return IncomingMessage{
		StreamID:      msg.ID,
		BotID:         get("bot_id"),
		ChannelID:     get("channel_id"),
		UserID:        get("user_id"),
		ThreadID:      get("thread_ts"),     // Python adapters write "thread_ts" (not "thread_id")
		MessageID:     get("message_ts"),    // Python adapters write "message_ts" (not "message_id")
		Text:          get("text"),
		Platform:      get("platform"),
		BotToken:      get("bot_token"),
		EventType:     get("event_type"),          // "create" | "delete" | "edit"; empty == "create"
		PlatformMsgID: get("platform_message_id"), // platform-native message ID for delete/edit
	}
}

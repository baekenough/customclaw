package analysis

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/baekenough/customclaw/internal/llm"
	rediswrapper "github.com/baekenough/customclaw/internal/redis"
)

const (
	// AnalysisStream is the Redis Stream key for analysis requests.
	AnalysisStream = "customclaw:analysis-requests"
	// AnalysisGroup is the consumer group name for analysis workers.
	AnalysisGroup = "analysis-workers"

	analysisReadBatch     = 5
	analysisBlockDuration = 5 * time.Second
	analysisPendingIdle   = time.Minute
	analysisClaimInterval = analysisPendingIdle / 2
	analysisMaxConcurrent = 4
)

// StartAnalysisConsumer starts a background goroutine that consumes analysis
// requests from the Redis Stream and dispatches them to the pipeline.
// The goroutine exits when ctx is cancelled.
func StartAnalysisConsumer(ctx context.Context, rdb *redis.Client) {
	consumerName := consumerName()
	if err := rediswrapper.EnsureStreamGroup(ctx, rdb, AnalysisStream, AnalysisGroup); err != nil {
		slog.Warn("analysis: ensure stream group failed", "error", err)
		// Non-fatal: the group may already exist in a restarted scenario.
	}

	provider := llm.NewClaudeCLIProvider()

	go consumeLoop(ctx, rdb, consumerName, provider)
	slog.Info("analysis consumer started", "stream", AnalysisStream)
}

// consumeLoop is the main read loop. It processes messages from the stream and
// reclaims idle pending messages on a ticker.
func consumeLoop(ctx context.Context, rdb *redis.Client, consumer string, provider llm.Provider) {
	claimTicker := time.NewTicker(analysisClaimInterval)
	defer claimTicker.Stop()

	// Bounded semaphore limits concurrent processAndAck goroutines.
	sem := make(chan struct{}, analysisMaxConcurrent)

	for {
		select {
		case <-ctx.Done():
			return
		case <-claimTicker.C:
			reclaimIdleAnalysis(ctx, rdb, consumer, provider, sem)
		default:
		}

		entries, err := rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    AnalysisGroup,
			Consumer: consumer,
			Streams:  []string{AnalysisStream, ">"},
			Count:    analysisReadBatch,
			Block:    analysisBlockDuration,
			NoAck:    false,
		}).Result()

		if err != nil {
			if err == redis.Nil || err == context.DeadlineExceeded {
				continue
			}
			if ctx.Err() != nil {
				return
			}
			slog.Error("analysis: xreadgroup error", "error", err)
			time.Sleep(time.Second)
			continue
		}

		for _, stream := range entries {
			for _, msg := range stream.Messages {
				req := decodeAnalysisMessage(msg)
				sem <- struct{}{}
				go func(id string, r AnalysisRequest) {
					defer func() { <-sem }()
					processAndAck(ctx, rdb, id, r, provider)
				}(msg.ID, req)
			}
		}
	}
}

// reclaimIdleAnalysis uses XAUTOCLAIM to recover pending messages from
// previously crashed consumers.
func reclaimIdleAnalysis(ctx context.Context, rdb *redis.Client, consumer string, provider llm.Provider, sem chan struct{}) {
	messages, _, err := rdb.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   AnalysisStream,
		Group:    AnalysisGroup,
		Consumer: consumer,
		MinIdle:  analysisPendingIdle,
		Start:    "0-0",
		Count:    analysisReadBatch,
	}).Result()
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("analysis: xautoclaim error", "error", err)
		}
		return
	}
	for _, msg := range messages {
		slog.Info("analysis: reclaiming idle message", "id", msg.ID)
		req := decodeAnalysisMessage(msg)
		sem <- struct{}{}
		go func(id string, r AnalysisRequest) {
			defer func() { <-sem }()
			processAndAck(ctx, rdb, id, r, provider)
		}(msg.ID, req)
	}
}

// processAndAck processes the request and ACKs the message on completion.
// Errors are logged; the message is still ACKed to avoid infinite redelivery of
// poison messages.
func processAndAck(ctx context.Context, rdb *redis.Client, msgID string, req AnalysisRequest, provider llm.Provider) {
	var err error
	switch req.Type {
	case "pr_analysis":
		err = processPRAnalysis(ctx, req, provider)
	default:
		err = processAnalysis(ctx, req, provider)
	}
	if err != nil {
		slog.Error("analysis: processing failed", "msg_id", msgID, "type", req.Type, "error", err)
	}

	if ackErr := rdb.XAck(ctx, AnalysisStream, AnalysisGroup, msgID).Err(); ackErr != nil {
		slog.Warn("analysis: xack failed", "id", msgID, "error", ackErr)
	} else {
		slog.Info("analysis: acked", "id", msgID)
	}
}

// decodeAnalysisMessage converts a raw Redis Stream entry to an AnalysisRequest.
func decodeAnalysisMessage(msg redis.XMessage) AnalysisRequest {
	get := func(key string) string {
		v, _ := msg.Values[key].(string)
		return v
	}
	return AnalysisRequest{
		Type:         get("type"),
		IssueNumber:  get("issue_number"),
		PRNumber:     get("pr_number"),
		Repo:         get("repo"),
		RepoPath:     get("repo_path"),
		IssueTitle:   get("issue_title"),
		IssueBody:    get("issue_body"),
		IssueLabels:  get("issue_labels"),
		PRTitle:      get("pr_title"),
		PRBody:       get("pr_body"),
		SlackChannel: get("slack_channel"),
	}
}

// consumerName returns a unique consumer identifier based on the hostname.
func consumerName() string {
	h, err := os.Hostname()
	if err != nil {
		h = "unknown"
	}
	return "analyzer-" + h
}

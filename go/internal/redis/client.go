// Package redis wraps go-redis/v9 with project-specific helpers.
package redis

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// NewClient parses redisURL and returns a connected *redis.Client.
// It performs a PING to verify connectivity before returning.
func NewClient(ctx context.Context, redisURL string) (*redis.Client, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis URL: %w", err)
	}

	rdb := redis.NewClient(opts)
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	return rdb, nil
}

// EnsureStreamGroup creates the consumer group for streamKey if it does not
// already exist. The group starts reading from the beginning of the stream ("0").
// The MKSTREAM option creates the stream if it does not yet exist.
func EnsureStreamGroup(ctx context.Context, rdb *redis.Client, streamKey, group string) error {
	err := rdb.XGroupCreateMkStream(ctx, streamKey, group, "0").Err()
	if err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		return fmt.Errorf("xgroup create %s/%s: %w", streamKey, group, err)
	}
	return nil
}

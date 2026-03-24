// Package runtime provides supervisor and restart-control primitives.
package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// RestartKeyPrefix is the Redis key prefix for restart request entries.
	// Full key: RestartKeyPrefix + ":" + target.
	RestartKeyPrefix = "customclaw:control:restart"

	// SupervisedEnv is the environment variable set to "1" when the process
	// is running under RunSupervisor.
	SupervisedEnv = "CUSTOMCLAW_SUPERVISED"

	// RuntimeTargetEnv holds the name of the target runtime (e.g. "worker").
	RuntimeTargetEnv = "CUSTOMCLAW_RUNTIME_TARGET"

	// RestartExitCode is the exit code a supervised child uses to request a
	// clean respawn from the supervisor without going through Redis.
	RestartExitCode = 75
)

// RestartRequest represents a pending restart stored in Redis.
type RestartRequest struct {
	Target      string `json:"target"`
	RequestedBy string `json:"requested_by"`
	Reason      string `json:"reason"`
	RequestedAt int64  `json:"requested_at"`
}

// restartKey returns the Redis key for a given target.
func restartKey(target string) string {
	return RestartKeyPrefix + ":" + target
}

// RequestRestart stores a restart request in Redis for the given target.
// Any existing request for the same target is overwritten. The key is set
// with a 5-minute TTL so stale requests do not linger forever.
func RequestRestart(ctx context.Context, rdb *redis.Client, target, requestedBy, reason string) error {
	req := RestartRequest{
		Target:      target,
		RequestedBy: requestedBy,
		Reason:      reason,
		RequestedAt: time.Now().Unix(),
	}
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal restart request: %w", err)
	}
	if err := rdb.Set(ctx, restartKey(target), data, 5*time.Minute).Err(); err != nil {
		return fmt.Errorf("redis set restart key: %w", err)
	}
	return nil
}

// ConsumeRestartRequest atomically reads and deletes the restart request for
// the given target. Returns (nil, nil) when no request is pending.
//
// GETDEL is available in Redis 6.2+. go-redis exposes it via GetDel.
func ConsumeRestartRequest(ctx context.Context, rdb *redis.Client, target string) (*RestartRequest, error) {
	data, err := rdb.GetDel(ctx, restartKey(target)).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("redis getdel restart key: %w", err)
	}

	var req RestartRequest
	if err := json.Unmarshal([]byte(data), &req); err != nil {
		return nil, fmt.Errorf("unmarshal restart request: %w", err)
	}
	return &req, nil
}

// IsSupervisedRuntime reports whether the current process was spawned by
// RunSupervisor (i.e. CUSTOMCLAW_SUPERVISED=1 is set).
func IsSupervisedRuntime() bool {
	return os.Getenv(SupervisedEnv) == "1"
}

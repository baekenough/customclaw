// Command supervisor manages a supervised child process with Redis-based
// restart control.
//
// Usage:
//
//	supervisor [target]
//
// target defaults to "worker" or the value of CUSTOMCLAW_RUNTIME_TARGET.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	rediswrapper "github.com/baekenough/customclaw/internal/redis"
	"github.com/baekenough/customclaw/internal/runtime"
)

func main() {
	if err := run(); err != nil {
		slog.Error("supervisor exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	target := resolveTarget()

	redisURL := envOrDefault("REDIS_URL", "redis://localhost:6379")

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	slog.Info("supervisor starting", "target", target, "redis_url", redisURL)

	rdb, err := rediswrapper.NewClient(ctx, redisURL)
	if err != nil {
		return err
	}
	defer func() { _ = rdb.Close() }()

	return runtime.RunSupervisor(ctx, rdb, target)
}

// resolveTarget determines the runtime target from command-line arguments or
// the CUSTOMCLAW_RUNTIME_TARGET environment variable, defaulting to "worker".
func resolveTarget() string {
	if len(os.Args) > 1 {
		return os.Args[1]
	}
	if t := os.Getenv(runtime.RuntimeTargetEnv); t != "" {
		return t
	}
	return "worker"
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

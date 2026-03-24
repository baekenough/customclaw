// Command app manages platform adapters (BotManager) and routes incoming
// messages from each platform to the shared Redis Stream.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/baekenough/customclaw/internal/config"
	"github.com/baekenough/customclaw/internal/platform"
	rediswrapper "github.com/baekenough/customclaw/internal/redis"
)

func main() {
	if err := run(); err != nil {
		slog.Error("app exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	redisURL := envOrDefault("REDIS_URL", "redis://localhost:6379")
	botsDir := envOrDefault("BOTS_DIR", "/app/bots")
	databaseDSN := os.Getenv("DATABASE_DSN")

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	slog.Info("starting app", "bots_dir", botsDir)

	rdb, err := rediswrapper.NewClient(ctx, redisURL)
	if err != nil {
		slog.Error("redis connect failed", "error", err)
		return err
	}
	defer func() { _ = rdb.Close() }()

	configs, err := config.LoadAllBots(ctx, botsDir, databaseDSN)
	if err != nil {
		slog.Error("load bots failed", "error", err)
		return err
	}
	slog.Info("loaded bot configurations", "count", len(configs))

	// Group Slack configs by app_token so that bots sharing a Socket Mode
	// connection are multiplexed onto a single SlackAdapter.
	slackByAppToken := make(map[string][]*config.BotConfig)
	for _, cfg := range configs {
		if cfg.Platform != "slack" {
			continue
		}
		if cfg.SlackAppToken == "" || cfg.SlackBotToken == "" {
			slog.Warn("slack bot missing tokens, skipping", "bot_id", cfg.ID)
			continue
		}
		slackByAppToken[cfg.SlackAppToken] = append(slackByAppToken[cfg.SlackAppToken], cfg)
	}

	// Group Discord configs by token so that bots sharing the same token
	// are multiplexed onto a single DiscordAdapter.
	discordByToken := make(map[string][]*config.BotConfig)
	for _, cfg := range configs {
		if cfg.Platform != "discord" {
			continue
		}
		if cfg.Discord.Token == "" {
			slog.Warn("discord bot missing token, skipping", "bot_id", cfg.ID)
			continue
		}
		discordByToken[cfg.Discord.Token] = append(discordByToken[cfg.Discord.Token], cfg)
	}

	// Log platforms that have no adapter implementation.
	for _, cfg := range configs {
		if cfg.Platform != "slack" && cfg.Platform != "discord" {
			slog.Warn("no adapter implemented for platform, skipping",
				"platform", cfg.Platform,
				"bot_id", cfg.ID,
			)
		}
	}

	// Start one SlackAdapter per unique app_token.
	var wg sync.WaitGroup
	adapters := make([]platform.PlatformAdapter, 0, len(slackByAppToken)+len(discordByToken))

	for appToken, bots := range slackByAppToken {
		adapter := platform.NewSlackAdapter(bots, rdb)
		adapters = append(adapters, adapter)

		botIDs := make([]string, len(bots))
		for i, b := range bots {
			botIDs[i] = b.ID
		}

		wg.Add(1)
		go func(a platform.PlatformAdapter, token string, ids []string) {
			defer wg.Done()
			slog.Info("starting slack adapter",
				"app_token_prefix", token[:min(8, len(token))],
				"bot_ids", ids,
			)
			if err := a.Start(ctx); err != nil && ctx.Err() == nil {
				slog.Error("slack adapter error",
					"app_token_prefix", token[:min(8, len(token))],
					"error", err,
				)
			}
		}(adapter, appToken, botIDs)
	}

	// Start one DiscordAdapter per unique token.
	for token, bots := range discordByToken {
		adapter, err := platform.NewDiscordAdapter(bots, rdb)
		if err != nil {
			slog.Error("failed to create discord adapter",
				"token_prefix", token[:min(8, len(token))],
				"error", err,
			)
			continue
		}
		adapters = append(adapters, adapter)

		botIDs := make([]string, len(bots))
		for i, b := range bots {
			botIDs[i] = b.ID
		}

		wg.Add(1)
		go func(a platform.PlatformAdapter, tok string, ids []string) {
			defer wg.Done()
			slog.Info("starting discord adapter",
				"token_prefix", tok[:min(8, len(tok))],
				"bot_ids", ids,
			)
			if err := a.Start(ctx); err != nil && ctx.Err() == nil {
				slog.Error("discord adapter error",
					"token_prefix", tok[:min(8, len(tok))],
					"error", err,
				)
			}
		}(adapter, token, botIDs)
	}

	// Wait for signal, then stop all adapters.
	<-ctx.Done()
	slog.Info("shutdown signal received, stopping adapters")

	for _, a := range adapters {
		if err := a.Stop(); err != nil {
			slog.Warn("adapter stop error", "error", err)
		}
	}
	wg.Wait()

	slog.Info("app shutdown complete")
	return nil
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

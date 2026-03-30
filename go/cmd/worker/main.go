// Command worker is the Redis Stream consumer that processes bot messages via LLM.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/baekenough/customclaw/internal/analysis"
	"github.com/baekenough/customclaw/internal/config"
	"github.com/baekenough/customclaw/internal/credprobe"
	"github.com/baekenough/customclaw/internal/llm"
	"github.com/baekenough/customclaw/internal/memory"
	"github.com/baekenough/customclaw/internal/notify"
	"github.com/baekenough/customclaw/internal/platform"
	rediswrapper "github.com/baekenough/customclaw/internal/redis"
	"github.com/baekenough/customclaw/internal/tools"
	"github.com/baekenough/customclaw/internal/usage"
	"github.com/baekenough/customclaw/internal/worker"
)

func main() {
	if err := run(); err != nil {
		slog.Error("worker exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	redisURL := envOrDefault("REDIS_URL", "redis://localhost:6379")
	botsDir := envOrDefault("BOTS_DIR", "/app/bots")
	consumerGroup := envOrDefault("REDIS_CONSUMER_GROUP", "customclaw-workers")
	consumerName := envOrDefault("REDIS_CONSUMER_NAME", mustHostname())
	databaseDSN := os.Getenv("DATABASE_DSN")

	maxConcurrent, err := strconv.Atoi(envOrDefault("MAX_CONCURRENT_WORKERS", "10"))
	if err != nil {
		return fmt.Errorf("parse MAX_CONCURRENT_WORKERS: %w", err)
	}

	mergeWindowSec, err := strconv.Atoi(envOrDefault("MERGE_WINDOW_SECONDS", "30"))
	if err != nil {
		return fmt.Errorf("parse MERGE_WINDOW_SECONDS: %w", err)
	}
	mergeWindow := time.Duration(mergeWindowSec) * time.Second

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	slog.Info("starting worker",
		"consumer_group", consumerGroup,
		"consumer_name", consumerName,
		"max_concurrent", maxConcurrent,
		"merge_window", mergeWindow,
	)

	// Redis client
	rdb, err := rediswrapper.NewClient(ctx, redisURL)
	if err != nil {
		return fmt.Errorf("redis connect: %w", err)
	}
	defer func() { _ = rdb.Close() }()

	// Create consumer group (idempotent)
	if err := rediswrapper.EnsureStreamGroup(ctx, rdb, worker.StreamKey, consumerGroup); err != nil {
		return fmt.Errorf("ensure stream group: %w", err)
	}

	// Load bot configs (DB-first, YAML fallback)
	configs, err := config.LoadAllBots(ctx, botsDir, databaseDSN)
	if err != nil {
		return fmt.Errorf("load bots: %w", err)
	}
	slog.Info("loaded bot configurations", "count", len(configs))

	botsMap := make(map[string]*config.BotConfig, len(configs))
	for _, cfg := range configs {
		botsMap[cfg.ID] = cfg
	}

	// LLM providers.
	// Claude uses the CLI binary (OAuth-based, independent of API key quota).
	// Codex and Gemini use direct API SDK calls.
	claudeCLIProvider := llm.NewClaudeCLIProvider()
	codexProvider := llm.NewCodexProvider()
	geminiProvider := llm.NewGeminiProvider()

	// Provider registry: keyed by canonical provider name.
	providers := map[string]llm.Provider{
		"claude":     claudeCLIProvider,
		"claude-cli": claudeCLIProvider,
		"anthropic":  claudeCLIProvider,
		"codex":      codexProvider,
		"openai":     codexProvider,
		"gemini":     geminiProvider,
	}

	// Keep a reference to the default provider (Claude CLI) for backwards-compatible
	// code paths that still hold a single llm.Provider reference.
	provider := claudeCLIProvider

	// Memory subsystem.
	store, err := memory.NewMessageStore(ctx, databaseDSN)
	if err != nil {
		return fmt.Errorf("memory store: %w", err)
	}

	opensearchURL := os.Getenv("OPENSEARCH_URL")
	search := memory.NewHybridSearch(store, opensearchURL, rdb)

	var osClient *memory.OpenSearchClient
	if opensearchURL != "" {
		osClient, err = memory.NewOpenSearchClient(opensearchURL)
		if err != nil {
			slog.Warn("opensearch unavailable; extractor will skip indexing", "error", err)
			osClient = nil
		}
	}
	extractor := memory.NewMemoryExtractor(provider, store, osClient, databaseDSN)

	// Tool registry — register all available tools.
	registry := buildToolRegistry()

	// Usage logger (no-op when DATABASE_DSN is empty).
	usageLogger := usage.NewLogger(databaseDSN)

	// PublisherFactory creates and caches ResponsePublisher instances per
	// platform and bot token.
	pubFactory := platform.NewPublisherFactory()
	publisherFactory := func(plt, _ string, token string) platform.ResponsePublisher {
		return pubFactory.Get(plt, token)
	}

	// Processor — uses the provider registry to select the right LLM per bot.
	proc := worker.NewProcessorWithProviders(
		botsMap,
		providers,
		provider, // default: Claude CLI
		store,
		search,
		registry,
		extractor,
		usageLogger,
		publisherFactory,
	)

	// Dispatcher.
	dispatcher := worker.NewDispatcher(mergeWindow, maxConcurrent,
		func(ctx context.Context, msg worker.IncomingMessage, msgIDs []string) {
			resp, err := proc.ProcessMessage(ctx, msg, msgIDs)

			// ACK after processing (at-least-once delivery).
			// ACK even on error to avoid infinite redelivery of poison messages.
			if len(msgIDs) > 0 {
				if ackErr := rdb.XAck(ctx, worker.StreamKey, consumerGroup, msgIDs...).Err(); ackErr != nil {
					slog.Warn("xack failed", "ids", msgIDs, "error", ackErr)
				}
			}

			if err != nil {
				slog.Error("process message failed",
					"bot", msg.BotID,
					"channel", msg.ChannelID,
					"error", err,
				)
				return
			}
			slog.Info("message processed", "bot", msg.BotID, "response_len", len(resp))
		},
	)

	// Credential probe — periodically checks LLM provider credentials and
	// stores results in the credential_status table. Fixes issue #32.
	// Env var precedence: ALERT_BOT_TOKEN > CUSTOMCLAW_SLACK_BOT_TOKEN (legacy)
	alertToken := os.Getenv("ALERT_BOT_TOKEN")
	if alertToken == "" {
		alertToken = os.Getenv("CUSTOMCLAW_SLACK_BOT_TOKEN") // legacy fallback
	}
	// Env var precedence: ALERT_CHANNEL > CREDENTIAL_ALERT_CHANNEL (legacy)
	alertChannel := os.Getenv("ALERT_CHANNEL")
	if alertChannel == "" {
		alertChannel = os.Getenv("CREDENTIAL_ALERT_CHANNEL") // legacy fallback
	}
	credprobe.SetAlertNotifier(notify.New(alertToken, alertChannel))
	credprobe.Start(ctx, store.Pool())

	// Analysis consumer — processes GitHub issue/PR analysis requests.
	if err := rediswrapper.EnsureStreamGroup(ctx, rdb, analysis.AnalysisStream, analysis.AnalysisGroup); err != nil {
		slog.Warn("ensure analysis stream group failed (non-fatal)", "error", err)
	}
	analysis.StartAnalysisConsumer(ctx, rdb)

	// Hot-reload subscriber
	config.StartConfigSubscriber(ctx, redisURL, func(botID string) {
		newConfigs, err := config.LoadAllBots(ctx, botsDir, databaseDSN)
		if err != nil {
			slog.Error("hot-reload: failed to reload configs", "error", err)
			return
		}
		// Clear cached publishers so any updated tokens take effect.
		pubFactory.Clear()
		for _, cfg := range newConfigs {
			if cfg.ID == botID {
				proc.UpdateBot(cfg)
				slog.Info("hot-reload: updated bot config", "bot_id", botID)
				return
			}
		}
		slog.Warn("hot-reload: bot not found in reloaded configs", "bot_id", botID)
	})

	// Drain legacy stream — catches any messages still in the old stream key
	// from before the platform-messages migration. Exits automatically once empty.
	go worker.DrainLegacyStream(ctx, rdb, consumerGroup, consumerName, dispatcher)

	// Main consumer loop
	slog.Info("consumer loop started")
	worker.ConsumerLoop(ctx, rdb, consumerGroup, consumerName, dispatcher)

	slog.Info("worker shutdown complete")
	return nil
}

// buildToolRegistry registers all available tools and returns the registry.
func buildToolRegistry() *tools.Registry {
	registry := tools.NewRegistry()

	// GitHub tools.
	registry.Register(&tools.CreateIssueTool{})
	registry.Register(&tools.QueryIssuesTool{})

	// Airflow tools.
	registry.Register(&tools.GetDagStatusTool{})
	registry.Register(&tools.ListDagRunsTool{})
	registry.Register(&tools.ListDagsTool{})
	registry.Register(&tools.TriggerDagTool{})

	// Code search (uses Claude CLI subprocess via llm.Provider).
	registry.Register(tools.NewSearchCodeTool())

	// Bot management tools.
	registry.Register(&tools.CreateBotTool{})
	registry.Register(&tools.ListBotsTool{})
	registry.Register(&tools.UpdateBotTool{})
	registry.Register(&tools.DeleteBotTool{})
	registry.Register(&tools.RestartRuntimeTool{})

	slog.Info("tool registry built", "tools", registry.ListNames())
	return registry
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func mustHostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "worker-unknown"
	}
	return "worker-" + h
}

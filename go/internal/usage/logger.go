// Package usage provides fire-and-forget LLM API usage logging to PostgreSQL.
package usage

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

const insertSQL = `
INSERT INTO api_usage_logs
	(bot_id, user_id, model, input_tokens, output_tokens,
	 cost_usd, tool_name, cache_read_tokens, cache_creation_tokens)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

// Entry holds the data for a single LLM usage record.
type Entry struct {
	BotID               string
	UserID              string
	Model               string
	InputTokens         int
	OutputTokens        int
	CostUSD             *float64
	ToolName            string
	CacheReadTokens     int
	CacheCreationTokens int
}

// Logger records LLM API usage data to the api_usage_logs PostgreSQL table.
// All operations are no-ops when constructed with an empty DSN.
type Logger struct {
	pool *pgxpool.Pool
}

// NewLogger creates a Logger. If dsn is empty, all operations are no-ops.
// A shared connection pool is initialized once to avoid connection storms
// under concurrent workers.
func NewLogger(dsn string) *Logger {
	if dsn == "" {
		return &Logger{}
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		slog.Warn("usage logger: failed to create connection pool", "error", err)
		return &Logger{}
	}
	return &Logger{pool: pool}
}

// Close releases the underlying connection pool. Should be called when the
// Logger is no longer needed.
func (l *Logger) Close() {
	if l.pool != nil {
		l.pool.Close()
	}
}

// LogUsage inserts a usage record into api_usage_logs.
// Errors are logged at Warn level and never returned — usage logging must
// not block or fail message processing.
func (l *Logger) LogUsage(ctx context.Context, entry Entry) {
	if l.pool == nil {
		return
	}

	_, err := l.pool.Exec(ctx, insertSQL,
		entry.BotID,
		entry.UserID,
		entry.Model,
		entry.InputTokens,
		entry.OutputTokens,
		entry.CostUSD,
		entry.ToolName,
		entry.CacheReadTokens,
		entry.CacheCreationTokens,
	)
	if err != nil {
		slog.Warn("usage logger: insert failed",
			"bot_id", entry.BotID,
			"model", entry.Model,
			"error", err,
		)
	}
}

package usage

import (
	"context"
	"testing"
)

func TestNewLogger_EmptyDSN(t *testing.T) {
	t.Parallel()

	l := NewLogger("")
	if l == nil {
		t.Fatal("NewLogger(\"\") returned nil")
	}
	// When DSN is empty the pool must be nil (no-op mode).
	if l.pool != nil {
		t.Error("expected pool to be nil for empty DSN")
	}
}

func TestNewLogger_WithDSN(t *testing.T) {
	t.Parallel()

	// An unreachable DSN must not panic; the pool creation will fail gracefully
	// and return a Logger with pool == nil.
	l := NewLogger("postgres://invalid-host-xyz:5432/db?connect_timeout=1")
	if l == nil {
		t.Fatal("NewLogger returned nil even for invalid DSN")
	}
}

// TestLogUsage_EmptyDSN verifies that LogUsage is a no-op and does not panic
// when the Logger was constructed with an empty DSN.
func TestLogUsage_EmptyDSN(t *testing.T) {
	t.Parallel()

	l := NewLogger("")

	// Must not panic.
	l.LogUsage(context.Background(), Entry{
		BotID:        "bot1",
		UserID:       "user1",
		Model:        "claude-sonnet",
		InputTokens:  100,
		OutputTokens: 50,
	})
}

// TestLogUsage_EmptyDSN_NilCostUSD verifies nil CostUSD is safe.
func TestLogUsage_EmptyDSN_NilCostUSD(t *testing.T) {
	t.Parallel()

	l := NewLogger("")
	l.LogUsage(context.Background(), Entry{
		BotID:   "bot1",
		CostUSD: nil, // explicitly nil
	})
}

// TestLogUsage_InvalidDSN verifies that a non-empty but invalid DSN does not
// panic — it should log a warning and return silently.
func TestLogUsage_InvalidDSN_NoConnect(t *testing.T) {
	t.Parallel()

	// Use an obviously invalid DSN that will fail to connect quickly.
	// The Logger must not panic, and must not block (postgres connect timeout
	// applies; we use a cancelled context to avoid waiting).
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediately cancelled

	l := NewLogger("postgres://invalid-host-xyz:5432/db?connect_timeout=1")

	// Should return without panicking despite connection failure.
	l.LogUsage(ctx, Entry{
		BotID: "bot1",
		Model: "sonnet",
	})
}

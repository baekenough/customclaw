package redis

import (
	"context"
	"testing"
)

// TestNewClient_InvalidURL verifies that a syntactically invalid URL returns
// a parse error before any network connection is attempted.
func TestNewClient_InvalidURL(t *testing.T) {
	t.Parallel()

	_, err := NewClient(context.Background(), "not-a-valid-redis-url://??")
	if err == nil {
		t.Fatal("expected error for invalid URL, got nil")
	}
}

// TestNewClient_UnreachableHost verifies that an unreachable host returns an
// error (ping failure) rather than panicking.
// This uses a cancelled context to avoid blocking in CI.
func TestNewClient_UnreachableHost(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediately cancelled — PING will fail fast

	_, err := NewClient(ctx, "redis://localhost:19999") // port unlikely to be in use
	if err == nil {
		t.Fatal("expected error for unreachable host, got nil")
	}
}

// TestNewClient_EmptyURL verifies behavior for an empty URL string.
func TestNewClient_EmptyURL(t *testing.T) {
	t.Parallel()

	_, err := NewClient(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty URL, got nil")
	}
}

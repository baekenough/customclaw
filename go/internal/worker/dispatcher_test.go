package worker

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// routingKey
// ---------------------------------------------------------------------------

func TestRoutingKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		msg  IncomingMessage
		want string
	}{
		{
			name: "thread message",
			msg:  IncomingMessage{ThreadID: "T123", ChannelID: "C001", UserID: "U001"},
			want: "thread:T123",
		},
		{
			name: "top-level message",
			msg:  IncomingMessage{ThreadID: "", ChannelID: "C001", UserID: "U001"},
			want: "channel:C001:U001",
		},
		{
			name: "empty thread treated as top-level",
			msg:  IncomingMessage{ChannelID: "C002", UserID: "U002"},
			want: "channel:C002:U002",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := routingKey(tc.msg); got != tc.want {
				t.Errorf("routingKey() = %q, want %q", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Dispatch: merge window behaviour
// ---------------------------------------------------------------------------

// newTestDispatcher creates a Dispatcher with a short merge window suitable for tests.
func newTestDispatcher(
	mergeWindow time.Duration,
	fn func(ctx context.Context, msg IncomingMessage, msgIDs []string),
) *Dispatcher {
	return NewDispatcher(mergeWindow, 4, fn)
}

func TestDispatch_SingleMessage(t *testing.T) {
	t.Parallel()

	const window = 20 * time.Millisecond

	var (
		mu      sync.Mutex
		got     []IncomingMessage
		gotIDs  [][]string
	)

	d := newTestDispatcher(window, func(_ context.Context, msg IncomingMessage, ids []string) {
		mu.Lock()
		got = append(got, msg)
		gotIDs = append(gotIDs, ids)
		mu.Unlock()
	})

	ctx := context.Background()
	msg := IncomingMessage{
		StreamID:  "1-0",
		BotID:     "bot1",
		ChannelID: "C001",
		UserID:    "U001",
		Text:      "hello",
	}
	d.Dispatch(ctx, msg)

	// Wait for merge window + buffer.
	time.Sleep(window * 3)

	mu.Lock()
	defer mu.Unlock()

	if len(got) != 1 {
		t.Fatalf("processFn called %d times, want 1", len(got))
	}
	if got[0].Text != "hello" {
		t.Errorf("Text = %q, want %q", got[0].Text, "hello")
	}
	if len(gotIDs[0]) != 1 || gotIDs[0][0] != "1-0" {
		t.Errorf("msgIDs = %v, want [1-0]", gotIDs[0])
	}
}

func TestDispatch_MergeWindow_SameKey(t *testing.T) {
	t.Parallel()

	const window = 40 * time.Millisecond

	type result struct {
		msg IncomingMessage
		ids []string
	}
	var (
		mu      sync.Mutex
		results []result
	)

	d := newTestDispatcher(window, func(_ context.Context, msg IncomingMessage, ids []string) {
		mu.Lock()
		results = append(results, result{msg, ids})
		mu.Unlock()
	})

	ctx := context.Background()
	base := IncomingMessage{
		BotID:     "bot1",
		ChannelID: "C001",
		UserID:    "U001",
	}

	msg1 := base
	msg1.StreamID = "1-0"
	msg1.Text = "first"

	msg2 := base
	msg2.StreamID = "1-1"
	msg2.Text = "second"

	// Dispatch both within the merge window.
	d.Dispatch(ctx, msg1)
	d.Dispatch(ctx, msg2)

	// Wait for the merge window to fire.
	time.Sleep(window * 3)

	mu.Lock()
	defer mu.Unlock()

	// Only one batch should have been processed.
	if len(results) != 1 {
		t.Fatalf("processFn called %d times, want 1 (merge should have occurred)", len(results))
	}

	merged := results[0]
	// Text should be newline-joined.
	if !strings.Contains(merged.msg.Text, "first") || !strings.Contains(merged.msg.Text, "second") {
		t.Errorf("merged text %q should contain both messages", merged.msg.Text)
	}
	if merged.msg.Text != "first\nsecond" {
		t.Errorf("merged text = %q, want %q", merged.msg.Text, "first\nsecond")
	}
	// Both stream IDs should be present.
	if len(merged.ids) != 2 {
		t.Errorf("msgIDs len = %d, want 2; got %v", len(merged.ids), merged.ids)
	}
}

func TestDispatch_DifferentKeysProcessedIndependently(t *testing.T) {
	t.Parallel()

	const window = 20 * time.Millisecond

	type result struct {
		key string
		msg IncomingMessage
	}
	var (
		mu      sync.Mutex
		results []result
	)

	d := newTestDispatcher(window, func(_ context.Context, msg IncomingMessage, _ []string) {
		mu.Lock()
		results = append(results, result{key: routingKey(msg), msg: msg})
		mu.Unlock()
	})

	ctx := context.Background()

	d.Dispatch(ctx, IncomingMessage{StreamID: "1-0", ChannelID: "C001", UserID: "U001", Text: "alpha"})
	d.Dispatch(ctx, IncomingMessage{StreamID: "2-0", ChannelID: "C002", UserID: "U002", Text: "beta"})

	time.Sleep(window * 4)

	mu.Lock()
	defer mu.Unlock()

	if len(results) != 2 {
		t.Fatalf("processFn called %d times, want 2", len(results))
	}

	keys := map[string]bool{}
	for _, r := range results {
		keys[r.key] = true
	}
	if !keys["channel:C001:U001"] {
		t.Error("missing result for channel:C001:U001")
	}
	if !keys["channel:C002:U002"] {
		t.Error("missing result for channel:C002:U002")
	}
}

// TestDispatch_FIFOOrdering verifies that successive dispatches for the same
// routing key are processed in order.
func TestDispatch_FIFOOrdering(t *testing.T) {
	t.Parallel()

	// Use a very short window to force each message to fire individually.
	// We dispatch the second message only after the first window would have expired.
	const window = 15 * time.Millisecond

	var (
		mu    sync.Mutex
		order []string
	)

	d := newTestDispatcher(window, func(_ context.Context, msg IncomingMessage, _ []string) {
		mu.Lock()
		order = append(order, msg.Text)
		mu.Unlock()
	})

	ctx := context.Background()
	base := IncomingMessage{BotID: "bot1", ChannelID: "C001", UserID: "U001"}

	// First message.
	m1 := base
	m1.StreamID = "1-0"
	m1.Text = "first"
	d.Dispatch(ctx, m1)

	// Wait for the first window to expire before dispatching the second.
	time.Sleep(window * 2)

	m2 := base
	m2.StreamID = "1-1"
	m2.Text = "second"
	d.Dispatch(ctx, m2)

	time.Sleep(window * 4)

	mu.Lock()
	defer mu.Unlock()

	if len(order) != 2 {
		t.Fatalf("got %d calls, want 2; order=%v", len(order), order)
	}
	if order[0] != "first" || order[1] != "second" {
		t.Errorf("order = %v, want [first second]", order)
	}
}

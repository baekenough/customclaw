// Package worker implements the Redis Stream consumer and message dispatch logic.
package worker

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// IncomingMessage is the normalised form of a Redis Stream entry passed to the dispatcher.
type IncomingMessage struct {
	// StreamID is the Redis Stream entry ID (e.g. "1700000000000-0").
	StreamID  string
	BotID     string
	ChannelID string
	UserID    string
	ThreadID  string
	MessageID string
	Text      string
	Platform  string
	BotToken  string
}

// pendingItem accumulates messages for a single routing key during the merge window.
type pendingItem struct {
	msgIDs    []string
	mergedMsg IncomingMessage
	timer     *time.Timer
}

// Dispatcher merges messages within a configurable window and then processes each
// routing key serially, while running multiple keys concurrently.
//
// Routing key assignment:
//   - Messages in a thread share key "thread:<thread_id>"
//   - Top-level messages share key "channel:<channel_id>:<user_id>"
//
// This mirrors the Python MessageDispatcher behaviour.
type Dispatcher struct {
	mergeWindow time.Duration
	semaphore   chan struct{}
	processFn   func(ctx context.Context, msg IncomingMessage, msgIDs []string)

	mu      sync.Mutex
	pending map[string]*pendingItem
	// per-key FIFO queues ensure messages for the same key are processed in order
	// even after the merge window fires.
	queues map[string]chan dispatchWork
}

type dispatchWork struct {
	msg    IncomingMessage
	msgIDs []string
}

// NewDispatcher creates a Dispatcher.
//
//   - mergeWindow: how long to wait for additional messages before processing.
//   - maxConcurrent: maximum number of concurrent routing-key processors.
//   - processFn: called once per merged message batch.
func NewDispatcher(
	mergeWindow time.Duration,
	maxConcurrent int,
	processFn func(ctx context.Context, msg IncomingMessage, msgIDs []string),
) *Dispatcher {
	return &Dispatcher{
		mergeWindow: mergeWindow,
		semaphore:   make(chan struct{}, maxConcurrent),
		processFn:   processFn,
		pending:     make(map[string]*pendingItem),
		queues:      make(map[string]chan dispatchWork),
	}
}

// routingKey derives the dispatch key for a message.
func routingKey(msg IncomingMessage) string {
	if msg.ThreadID != "" {
		return "thread:" + msg.ThreadID
	}
	return "channel:" + msg.ChannelID + ":" + msg.UserID
}

// Dispatch enqueues msg for processing, merging it with any pending messages
// that share the same routing key within the merge window.
func (d *Dispatcher) Dispatch(ctx context.Context, msg IncomingMessage) {
	key := routingKey(msg)

	d.mu.Lock()
	item, exists := d.pending[key]
	if !exists {
		item = &pendingItem{
			mergedMsg: msg,
		}
		d.pending[key] = item
	} else {
		// Merge: append text and accumulate stream IDs.
		if item.mergedMsg.Text != "" && msg.Text != "" {
			item.mergedMsg.Text = strings.Join([]string{item.mergedMsg.Text, msg.Text}, "\n")
		} else if msg.Text != "" {
			item.mergedMsg.Text = msg.Text
		}
		item.timer.Stop()
	}
	item.msgIDs = append(item.msgIDs, msg.StreamID)

	item.timer = time.AfterFunc(d.mergeWindow, func() {
		d.fire(ctx, key)
	})
	d.mu.Unlock()
}

// fire removes the pending item for key and sends it to the per-key worker.
func (d *Dispatcher) fire(ctx context.Context, key string) {
	d.mu.Lock()
	item, ok := d.pending[key]
	if !ok {
		d.mu.Unlock()
		return
	}
	delete(d.pending, key)

	q, exists := d.queues[key]
	if !exists {
		q = make(chan dispatchWork, 64)
		d.queues[key] = q
		d.mu.Unlock()
		// Start a dedicated goroutine that serialises work for this key.
		go d.runQueue(ctx, key, q)
	} else {
		d.mu.Unlock()
	}

	work := dispatchWork{msg: item.mergedMsg, msgIDs: item.msgIDs}
	select {
	case q <- work:
	case <-ctx.Done():
	}
}

// queueIdleTimeout is how long a per-key goroutine waits for new work before
// exiting. On the next message for that key a fresh goroutine will be started.
const queueIdleTimeout = 60 * time.Second

// runQueue drains the per-key work queue, bounded by the global semaphore.
// It exits when either the context is done or no work arrives within
// queueIdleTimeout, removing the queue entry from the map so the next
// dispatch for this key starts a fresh goroutine.
func (d *Dispatcher) runQueue(ctx context.Context, key string, q chan dispatchWork) {
	idleTimer := time.NewTimer(queueIdleTimeout)
	defer idleTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-idleTimer.C:
			// No work arrived within the idle window. Remove the queue entry
			// and let this goroutine exit cleanly.
			d.mu.Lock()
			delete(d.queues, key)
			d.mu.Unlock()
			return
		case work, ok := <-q:
			if !ok {
				return
			}
			// Reset the idle timer each time real work arrives.
			if !idleTimer.Stop() {
				select {
				case <-idleTimer.C:
				default:
				}
			}
			idleTimer.Reset(queueIdleTimeout)

			// Acquire semaphore slot.
			select {
			case d.semaphore <- struct{}{}:
			case <-ctx.Done():
				return
			}
			func() {
				defer func() { <-d.semaphore }()
				defer func() {
					if r := recover(); r != nil {
						slog.Error("panic in message processor", "key", key, "panic", r)
					}
				}()
				d.processFn(ctx, work.msg, work.msgIDs)
			}()
		}
	}
}

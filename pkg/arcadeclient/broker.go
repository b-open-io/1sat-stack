package arcadeclient

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// EventHandler is invoked for every event arriving on the SSE stream.
// Handlers run sequentially after per-txid waiters are notified; a panicking
// handler is logged and recovered so it does not stop the broker.
type EventHandler func(context.Context, *SSEEvent)

// CheckpointStore is the minimal key/value surface the broker uses to persist
// its lastEventID cursor across restarts. The caller picks the storage key
// via SetCheckpointStore. Get should return ("", nil) when the key does not
// exist (caller wraps any "not found" error into the nil-value form).
type CheckpointStore interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value string) error
}

// EventBroker wraps a Client's Subscribe stream and fans events out to
// per-txid waiters and always-on handlers. One broker per 1sat-stack instance.
type EventBroker struct {
	client *Client
	logger *slog.Logger

	mu              sync.Mutex
	waiters         map[string][]chan *SSEEvent
	handlers        []EventHandler
	lastEventID     string
	checkpointStore CheckpointStore
	checkpointKey   string
}

// NewEventBroker creates a broker. Call Run to start consuming the SSE stream.
func NewEventBroker(client *Client, logger *slog.Logger) *EventBroker {
	if logger == nil {
		logger = slog.Default()
	}
	return &EventBroker{
		client:  client,
		logger:  logger,
		waiters: make(map[string][]chan *SSEEvent),
	}
}

// SetLastEventID seeds the broker with a resume checkpoint. Call before Run.
func (b *EventBroker) SetLastEventID(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lastEventID = id
}

// SetCheckpointStore wires a CheckpointStore (and its key) into the broker.
// Must be called before Run. Once set, Run loads the prior cursor from cs
// before subscribing, and every dispatched event triggers a Set. Save
// failures log a warning and are otherwise ignored — the next event will
// overwrite the stored value.
func (b *EventBroker) SetCheckpointStore(cs CheckpointStore, key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.checkpointStore = cs
	b.checkpointKey = key
}

// LastEventID returns the most recently dispatched event's ID. Safe to call
// concurrently with Run; useful for periodically persisting the checkpoint
// so we can resume across restarts within arcade's SSE retention window.
func (b *EventBroker) LastEventID() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lastEventID
}

// AddHandler registers an always-on handler. Handlers receive every event
// regardless of whether a per-txid waiter is registered. Safe to call concurrently.
func (b *EventBroker) AddHandler(h EventHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers = append(b.handlers, h)
}

// Wait registers a per-txid waiter. Returns a buffered channel that receives
// every event for that txid, plus a cleanup func the caller MUST invoke when
// done (typically deferred). The channel is closed by cleanup.
func (b *EventBroker) Wait(txid string) (<-chan *SSEEvent, func()) {
	ch := make(chan *SSEEvent, 8)
	b.mu.Lock()
	b.waiters[txid] = append(b.waiters[txid], ch)
	b.mu.Unlock()

	cleanup := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		list := b.waiters[txid]
		for i, c := range list {
			if c == ch {
				b.waiters[txid] = append(list[:i], list[i+1:]...)
				if len(b.waiters[txid]) == 0 {
					delete(b.waiters, txid)
				}
				close(ch)
				return
			}
		}
	}
	return ch, cleanup
}

// Run consumes events from arcade's SSE stream and fans each event out to
// matching per-txid waiters and to all always-on handlers. Blocks until ctx
// is cancelled or the underlying SSE stream cannot be re-established.
func (b *EventBroker) Run(ctx context.Context) {
	b.loadCheckpoint(ctx)
	startID := b.LastEventID()
	for evt := range b.client.Subscribe(ctx, startID) {
		b.dispatch(ctx, evt)
	}
}

// loadCheckpoint pulls the last persisted cursor from the configured
// CheckpointStore (if any) and seeds the in-memory lastEventID before
// Subscribe runs. Errors are logged and tolerated — broker simply starts
// from "now".
func (b *EventBroker) loadCheckpoint(ctx context.Context) {
	b.mu.Lock()
	cs := b.checkpointStore
	key := b.checkpointKey
	b.mu.Unlock()
	if cs == nil || key == "" {
		return
	}
	id, err := cs.Get(ctx, key)
	if err != nil {
		b.logger.Warn("arcade broker: failed to load checkpoint, starting from now",
			"key", key, "err", err)
		return
	}
	if id == "" {
		return
	}
	b.SetLastEventID(id)
	b.logger.Info("arcade broker: resumed from checkpoint",
		"key", key, "last_event_id", id)
}

func (b *EventBroker) dispatch(ctx context.Context, evt *SSEEvent) {
	b.mu.Lock()
	if evt.ID != "" {
		b.lastEventID = evt.ID
	}
	waiters := append([]chan *SSEEvent(nil), b.waiters[evt.Txid]...)
	handlers := append([]EventHandler(nil), b.handlers...)
	cs := b.checkpointStore
	key := b.checkpointKey
	b.mu.Unlock()

	if cs != nil && key != "" && evt.ID != "" {
		if err := cs.Set(ctx, key, evt.ID); err != nil {
			b.logger.Warn("arcade broker: failed to save checkpoint",
				"key", key, "id", evt.ID, "err", err)
		}
	}

	b.logger.Info("arcade event dispatching",
		"txid", evt.Txid, "tx_status", evt.TxStatus,
		"waiters", len(waiters), "handlers", len(handlers))

	for _, ch := range waiters {
		select {
		case ch <- evt:
		default:
			b.logger.Warn("arcade event broker: waiter buffer full, event dropped",
				"txid", evt.Txid, "tx_status", evt.TxStatus)
		}
	}

	for _, h := range handlers {
		b.invokeHandler(ctx, h, evt)
	}
}

func (b *EventBroker) invokeHandler(ctx context.Context, h EventHandler, evt *SSEEvent) {
	defer func() {
		if r := recover(); r != nil {
			b.logger.Error("arcade event broker: handler panicked",
				"panic", r, "txid", evt.Txid, "tx_status", evt.TxStatus)
		}
	}()
	h(ctx, evt)
}

// StopCondition selects when SubmitAndWait considers the wait satisfied.
type StopCondition int

const (
	// StopOnAccepted breaks the wait once the tx reaches an "accepted" tier
	// (SENT_TO_NETWORK / ACCEPTED_BY_NETWORK / SEEN_ON_NETWORK / SEEN_MULTIPLE_NODES)
	// or any terminal state. This is the default; matches what /1sat/tx and paymail want.
	StopOnAccepted StopCondition = iota

	// StopOnTerminal breaks the wait only on a terminal state
	// (MINED / IMMUTABLE / REJECTED / DOUBLE_SPEND_ATTEMPTED).
	StopOnTerminal
)

// WaitOptions configures SubmitAndWait.
type WaitOptions struct {
	// Timeout is the overall wait window. Zero means rely on ctx alone.
	Timeout time.Duration
	// StopOn selects the stop condition. Default StopOnAccepted.
	StopOn StopCondition
}

// SubmitAndWait submits a raw transaction and blocks until the wait condition
// is satisfied or the timeout fires, then returns the most up-to-date
// TransactionStatus available via GetStatus.
//
// On stop-condition reached: returns (status, nil).
// On timeout (ctx.Done() or wait.Timeout elapses): returns (best-effort
// status, ctx.Err()) — caller checks err for context.DeadlineExceeded and
// decides whether the partial status is still useful (usually yes).
// On submit failure: returns (nil, error).
//
// The returned TransactionStatus is fetched via GetStatus after the wait
// resolves, since SSE events are slim and don't include merkle path/extraInfo.
func (b *EventBroker) SubmitAndWait(
	ctx context.Context,
	rawTx []byte,
	submit SubmitOptions,
	wait WaitOptions,
) (*TransactionStatus, error) {
	if wait.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, wait.Timeout)
		defer cancel()
	}

	txid, err := ComputeTxid(rawTx)
	if err != nil {
		return nil, fmt.Errorf("compute txid: %w", err)
	}

	// Register waiter before submit so we don't race the first event.
	eventCh, cleanup := b.Wait(txid)
	defer cleanup()

	if _, err := b.client.Submit(ctx, rawTx, submit); err != nil {
		return nil, fmt.Errorf("submit: %w", err)
	}

	var lastEvent *SSEEvent
	for {
		select {
		case evt, ok := <-eventCh:
			if !ok {
				b.logger.Warn("arcade SubmitAndWait: waiter channel closed", "txid", txid)
				return b.finalStatus(txid, lastEvent), nil
			}
			lastEvent = evt
			if shouldStop(evt.TxStatus, wait.StopOn) {
				b.logger.Info("arcade SubmitAndWait stop condition reached",
					"txid", txid, "tx_status", evt.TxStatus)
				return b.finalStatus(txid, evt), nil
			}
		case <-ctx.Done():
			lastStatus := ""
			if lastEvent != nil {
				lastStatus = lastEvent.TxStatus
			}
			b.logger.Warn("arcade SubmitAndWait timed out",
				"txid", txid, "last_status", lastStatus)
			return b.finalStatus(txid, lastEvent), ctx.Err()
		}
	}
}

func shouldStop(status string, cond StopCondition) bool {
	if IsTerminal(status) {
		return true
	}
	if cond == StopOnAccepted && IsAccepted(status) {
		return true
	}
	return false
}

// finalStatus best-effort fetches the full TransactionStatus from arcade.
// Uses an independent 5s context so it works even if the caller's ctx is
// cancelled (timeout path). On fetch failure, falls back to the slim event
// payload, then to a placeholder Unknown status.
func (b *EventBroker) finalStatus(txid string, fallback *SSEEvent) *TransactionStatus {
	fetchCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if status, err := b.client.GetStatus(fetchCtx, txid); err == nil && status != nil {
		return status
	}
	if fallback != nil {
		return &TransactionStatus{
			Txid:      fallback.Txid,
			TxStatus:  fallback.TxStatus,
			Timestamp: fallback.Timestamp,
		}
	}
	return &TransactionStatus{
		Txid:     txid,
		TxStatus: StatusUnknown,
	}
}

package arcadeclient

import (
	"context"
	"log/slog"
	"sync"
	"testing"
)

// A waiter that gives up (cleanup closes its channel) while an event for the
// same txid is being dispatched must not crash the broker. Before the fix,
// dispatch copied the waiter list, released the lock, and sent to channels
// that cleanup had since closed: "panic: send on closed channel", which took
// the whole server down in production. Run with -race.
func TestDispatchSurvivesWaiterCleanupRace(t *testing.T) {
	b := NewEventBroker(nil, slog.Default())
	const txid = "4b4aa04479668f9ea3bf50b16c7b918a6d61b93d8b503faa292e27364c46f9d0"
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		ch, cleanup := b.Wait(txid)
		wg.Add(2)
		go func() {
			defer wg.Done()
			cleanup()
		}()
		go func() {
			defer wg.Done()
			b.dispatch(ctx, &SSEEvent{Txid: txid, TxStatus: "REJECTED"})
		}()
		// Drain whatever was delivered before the close so the buffer never fills.
		go func() {
			for range ch {
			}
		}()
	}
	wg.Wait()

	b.mu.Lock()
	remaining := len(b.waiters[txid])
	b.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("waiters left registered after cleanup: %d", remaining)
	}
}

// Events still reach a live waiter, and a full buffer drops rather than blocks.
func TestDispatchDeliversToLiveWaiter(t *testing.T) {
	b := NewEventBroker(nil, slog.Default())
	const txid = "c705a20c429ca8b7dc0ea5048a3b0845c69fa13d1471f48740e050a8bd38d263"
	ch, cleanup := b.Wait(txid)
	defer cleanup()

	for i := 0; i < 10; i++ { // buffer is 8: the last two must be dropped, not block
		b.dispatch(context.Background(), &SSEEvent{Txid: txid, TxStatus: "SEEN_ON_NETWORK"})
	}
	if got := len(ch); got != 8 {
		t.Fatalf("buffered events = %d, want 8", got)
	}
	if evt := <-ch; evt.TxStatus != "SEEN_ON_NETWORK" {
		t.Fatalf("unexpected event %+v", evt)
	}
}

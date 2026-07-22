package indexer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/pubsub"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/chaintracker"
)

// capturePubSub records Publish calls for assertions.
type capturePubSub struct {
	mu        sync.Mutex
	publishes []capturedPublish
}

type capturedPublish struct {
	topic string
	data  string
}

func (c *capturePubSub) Publish(ctx context.Context, topic string, data string, score ...float64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.publishes = append(c.publishes, capturedPublish{topic: topic, data: data})
	return nil
}

func (c *capturePubSub) Subscribe(ctx context.Context, topics []string) (<-chan pubsub.Event, error) {
	return nil, nil
}
func (c *capturePubSub) Unsubscribe(topics []string) error { return nil }
func (c *capturePubSub) Stop() error                       { return nil }
func (c *capturePubSub) Close() error                      { return nil }

// notFoundBeef surfaces a miss as beef.ErrNotFound. This exercises the
// auditor's rollback+publish logic in isolation, but note the production
// aggregator (beef.Storage.UpdateMerklePath) never returns ErrNotFound — see
// fetchErrorBeef for the production-semantics case.
type notFoundBeef struct{}

func (notFoundBeef) LoadTx(ctx context.Context, txid *chainhash.Hash) (*transaction.Transaction, error) {
	return nil, beef.ErrNotFound
}

func (notFoundBeef) UpdateMerklePath(ctx context.Context, txid *chainhash.Hash, ct chaintracker.ChainTracker) ([]byte, error) {
	return nil, beef.ErrNotFound
}

func (notFoundBeef) SaveBeef(ctx context.Context, txid *chainhash.Hash, b *transaction.Beef) error {
	return nil
}

// fetchErrorBeef mirrors the production aggregator: a genuine miss surfaces as
// a plain error, not beef.ErrNotFound. processUnconfirmed classifies this as a
// transient error and retries, so the rollback branch is not reached.
type fetchErrorBeef struct{}

func (fetchErrorBeef) LoadTx(ctx context.Context, txid *chainhash.Hash) (*transaction.Transaction, error) {
	return nil, errors.New("not found")
}

func (fetchErrorBeef) UpdateMerklePath(ctx context.Context, txid *chainhash.Hash, ct chaintracker.ChainTracker) ([]byte, error) {
	return nil, errors.New("unable to fetch updated merkle proof for " + txid.String())
}

func (fetchErrorBeef) SaveBeef(ctx context.Context, txid *chainhash.Hash, b *transaction.Beef) error {
	return nil
}

func newAuditorTestStore(t *testing.T) store.Store {
	t.Helper()
	s, err := store.NewBadgerStoreFromConfig(
		&store.BadgerConfig{Path: t.TempDir(), LogLevel: "error"},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatalf("open badger store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestProcessUnconfirmedPublishesStaleRollback(t *testing.T) {
	st := newAuditorTestStore(t)
	outputStore := txo.NewOutputStore(st, nil, beef.NewStorageFromProviders(nil, nil))
	ps := &capturePubSub{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	a := NewPendingAuditor(outputStore, notFoundBeef{}, nil, nil, nil, ps, logger)

	txid := chainhash.HashH([]byte("stale-tx"))
	staleScore := float64(time.Now().Add(-4*time.Hour).UnixNano()) / 1e9
	members := []store.ScoredMember{{Member: txid[:], Score: staleScore}}

	proofsFound, rolledBack, stillPending := a.processUnconfirmed(context.Background(), members)
	if rolledBack != 1 {
		t.Fatalf("rolledBack = %d, want 1 (proofsFound=%d stillPending=%d)", rolledBack, proofsFound, stillPending)
	}

	ps.mu.Lock()
	defer ps.mu.Unlock()
	if len(ps.publishes) != 1 {
		t.Fatalf("publishes = %d, want 1", len(ps.publishes))
	}
	pub := ps.publishes[0]
	if pub.topic != "arc" {
		t.Fatalf("topic = %q, want arc", pub.topic)
	}
	var evt ArcEvent
	if err := json.Unmarshal([]byte(pub.data), &evt); err != nil {
		t.Fatalf("unmarshal arc event: %v", err)
	}
	if evt.TxID != txid.String() {
		t.Fatalf("evt.TxID = %q, want %q", evt.TxID, txid.String())
	}
	if evt.Status != "REJECTED" {
		t.Fatalf("evt.Status = %q, want REJECTED", evt.Status)
	}
	if !strings.Contains(evt.ExtraInfo, "stale") {
		t.Fatalf("evt.ExtraInfo = %q, want to contain 'stale'", evt.ExtraInfo)
	}
}

// TestProcessUnconfirmedFetchErrorDoesNotRollback pins the intentionally-dormant
// behavior: because beef.Storage.UpdateMerklePath returns a plain error on a
// genuine miss, the stale-rollback branch is not reached and no arc event is
// published. If automatic stale-rollback is later enabled, this test should
// flip to expect a rollback.
func TestProcessUnconfirmedFetchErrorDoesNotRollback(t *testing.T) {
	st := newAuditorTestStore(t)
	outputStore := txo.NewOutputStore(st, nil, beef.NewStorageFromProviders(nil, nil))
	ps := &capturePubSub{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	a := NewPendingAuditor(outputStore, fetchErrorBeef{}, nil, nil, nil, ps, logger)

	txid := chainhash.HashH([]byte("stale-tx"))
	staleScore := float64(time.Now().Add(-4*time.Hour).UnixNano()) / 1e9
	members := []store.ScoredMember{{Member: txid[:], Score: staleScore}}

	_, rolledBack, stillPending := a.processUnconfirmed(context.Background(), members)
	if rolledBack != 0 {
		t.Fatalf("rolledBack = %d, want 0 (branch is unreachable under production error semantics)", rolledBack)
	}
	if stillPending != 1 {
		t.Fatalf("stillPending = %d, want 1", stillPending)
	}

	ps.mu.Lock()
	defer ps.mu.Unlock()
	if len(ps.publishes) != 0 {
		t.Fatalf("publishes = %d, want 0 (no rollback event should fire in production)", len(ps.publishes))
	}
}

package indexer

import (
	"context"
	"encoding/json"
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

// notFoundBeef reports every tx as having no proof available, driving the
// auditor's stale-rollback path.
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

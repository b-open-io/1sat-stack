package overlay

import (
	"context"
	"testing"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/pubsub"
	"github.com/b-open-io/1sat-stack/pkg/queue"
	"github.com/bsv-blockchain/go-sdk/chainhash"
)

type mockQueue struct {
	added map[string][]queue.ScoredItem
}

func newMockQueue() *mockQueue {
	return &mockQueue{added: make(map[string][]queue.ScoredItem)}
}

func (m *mockQueue) Enqueue(ctx context.Context, key []byte, items ...queue.ScoredItem) error {
	k := string(key)
	m.added[k] = append(m.added[k], items...)
	return nil
}

func (m *mockQueue) Read(ctx context.Context, key []byte, cfg queue.ReadCfg) ([]queue.ScoredItem, error) {
	return nil, nil
}

func (m *mockQueue) Ack(ctx context.Context, key []byte, members ...[]byte) error { return nil }

func (m *mockQueue) Requeue(ctx context.Context, key []byte, item queue.ScoredItem) error { return nil }

func (m *mockQueue) Depth(ctx context.Context, key []byte) (uint64, error) { return 0, nil }

func (m *mockQueue) Close() error { return nil }

func TestEventBridge_RoutesToQueue(t *testing.T) {
	ps := pubsub.NewChannelPubSub(nil)
	defer ps.Close()
	ms := newMockQueue()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bridge := NewEventBridge(&EventBridgeConfig{
		PubSub:   ps,
		Queue:    ms,
		Patterns: []string{"ordlock", "spend:ordlock"},
		QueueFunc: func(ev pubsub.Event) string {
			return "q:ordlock"
		},
	})

	if err := bridge.Start(ctx); err != nil {
		t.Fatal(err)
	}

	testTxid := "a0b1c2d3e4f5a0b1c2d3e4f5a0b1c2d3e4f5a0b1c2d3e4f5a0b1c2d3e4f5a0b1"
	if err := ps.Publish(ctx, "ordlock", testTxid); err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)

	members, ok := ms.added["q:ordlock"]
	if !ok || len(members) == 0 {
		t.Fatal("expected txid to be enqueued")
	}
	expectedHash, _ := chainhash.NewHashFromHex(testTxid)
	if string(members[0].Member) != string(expectedHash[:]) {
		t.Fatalf("unexpected member: got %x, want %x", members[0].Member, expectedHash[:])
	}
	if members[0].Score <= 0 {
		t.Fatal("expected positive timestamp score")
	}
}

func TestEventBridge_SkipsOnEmptyQueueKey(t *testing.T) {
	ps := pubsub.NewChannelPubSub(nil)
	defer ps.Close()
	ms := newMockQueue()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bridge := NewEventBridge(&EventBridgeConfig{
		PubSub:   ps,
		Queue:    ms,
		Patterns: []string{"map:type:post"},
		QueueFunc: func(ev pubsub.Event) string {
			return ""
		},
	})

	if err := bridge.Start(ctx); err != nil {
		t.Fatal(err)
	}

	if err := ps.Publish(ctx, "map:type:post", "txid1"); err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)

	if len(ms.added) > 0 {
		t.Fatal("should not have enqueued anything")
	}
}

package overlay

import (
	"context"
	"testing"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/pubsub"
	"github.com/b-open-io/1sat-stack/pkg/store"
)

type mockStore struct {
	store.Store
	added map[string][]store.ScoredMember
}

func newMockStore() *mockStore {
	return &mockStore{added: make(map[string][]store.ScoredMember)}
}

func (m *mockStore) ZAdd(ctx context.Context, key []byte, members ...store.ScoredMember) error {
	k := string(key)
	m.added[k] = append(m.added[k], members...)
	return nil
}

func TestEventBridge_RoutesToQueue(t *testing.T) {
	ps := pubsub.NewChannelPubSub(nil)
	defer ps.Close()
	ms := newMockStore()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bridge := NewEventBridge(&EventBridgeConfig{
		PubSub:   ps,
		Store:    ms,
		Patterns: []string{"ordlock", "spend:ordlock"},
		QueueFunc: func(ev pubsub.Event) string {
			return "q:ordlock"
		},
	})

	if err := bridge.Start(ctx); err != nil {
		t.Fatal(err)
	}

	if err := ps.Publish(ctx, "ordlock", "abcd1234"); err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)

	members, ok := ms.added["q:ordlock"]
	if !ok || len(members) == 0 {
		t.Fatal("expected txid to be enqueued")
	}
	if string(members[0].Member) != "abcd1234" {
		t.Fatalf("unexpected member: %s", string(members[0].Member))
	}
	if members[0].Score <= 0 {
		t.Fatal("expected positive timestamp score")
	}
}

func TestEventBridge_SkipsOnEmptyQueueKey(t *testing.T) {
	ps := pubsub.NewChannelPubSub(nil)
	defer ps.Close()
	ms := newMockStore()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bridge := NewEventBridge(&EventBridgeConfig{
		PubSub:   ps,
		Store:    ms,
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

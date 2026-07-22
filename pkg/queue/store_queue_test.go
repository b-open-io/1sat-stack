package queue_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/b-open-io/1sat-stack/pkg/queue"
	"github.com/b-open-io/1sat-stack/pkg/store"
)

// newTestBadgerStore opens a disk-backed Badger store in a temp dir.
func newTestBadgerStore(t *testing.T) store.Store {
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

func TestStoreQueueRoundTrip(t *testing.T) {
	s := newTestBadgerStore(t)
	q := queue.NewStoreQueue(s)
	key := []byte("q:test")
	ctx := context.Background()

	if err := q.Enqueue(ctx, key, queue.ScoredItem{Member: []byte("a"), Score: 1}, queue.ScoredItem{Member: []byte("b"), Score: 2}); err != nil {
		t.Fatal(err)
	}
	if n, _ := q.Depth(ctx, key); n != 2 {
		t.Fatalf("depth = %d, want 2", n)
	}
	items, err := q.Read(ctx, key, queue.ReadCfg{Limit: 10})
	if err != nil || len(items) != 2 || string(items[0].Member) != "a" {
		t.Fatalf("read = %v, %v", items, err) // score order
	}
	if err := q.Ack(ctx, key, []byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := q.Requeue(ctx, key, queue.ScoredItem{Member: []byte("b"), Score: 99}); err != nil {
		t.Fatal(err)
	}
	items, _ = q.Read(ctx, key, queue.ReadCfg{Limit: 10})
	if len(items) != 1 || items[0].Score != 99 {
		t.Fatalf("after ack+requeue: %v", items)
	}
}

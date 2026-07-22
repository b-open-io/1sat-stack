package queue

import (
	"context"

	"github.com/b-open-io/1sat-stack/pkg/store"
)

// StoreQueue implements Queue over a store.Store's sorted-set operations,
// preserving the existing q:* keyspace.
type StoreQueue struct {
	store  store.Store
	closer func() error // closes an owned dedicated store; nil for an inherited store
}

// NewStoreQueue wraps an existing store. The store's lifecycle belongs to its
// owner, so Close is a no-op.
func NewStoreQueue(s store.Store) *StoreQueue {
	return &StoreQueue{store: s}
}

func (q *StoreQueue) Enqueue(ctx context.Context, key []byte, items ...ScoredItem) error {
	if len(items) == 0 {
		return nil
	}
	members := make([]store.ScoredMember, len(items))
	for i, item := range items {
		members[i] = store.ScoredMember{Member: item.Member, Score: item.Score}
	}
	return q.store.ZAdd(ctx, key, members...)
}

func (q *StoreQueue) Read(ctx context.Context, key []byte, cfg ReadCfg) ([]ScoredItem, error) {
	results, err := q.store.Search(ctx, &store.SearchCfg{
		Keys:  [][]byte{key},
		From:  cfg.From,
		To:    cfg.To,
		Limit: uint32(cfg.Limit),
	})
	if err != nil {
		return nil, err
	}
	items := make([]ScoredItem, len(results))
	for i, r := range results {
		items[i] = ScoredItem{Member: r.Member, Score: r.Score}
	}
	return items, nil
}

func (q *StoreQueue) Ack(ctx context.Context, key []byte, members ...[]byte) error {
	if len(members) == 0 {
		return nil
	}
	return q.store.ZRem(ctx, key, members...)
}

func (q *StoreQueue) Requeue(ctx context.Context, key []byte, item ScoredItem) error {
	return q.store.ZAdd(ctx, key, store.ScoredMember{Member: item.Member, Score: item.Score})
}

func (q *StoreQueue) Depth(ctx context.Context, key []byte) (uint64, error) {
	n, err := q.store.ZCard(ctx, key)
	if err != nil {
		return 0, err
	}
	if n < 0 {
		return 0, nil
	}
	return uint64(n), nil
}

func (q *StoreQueue) Close() error {
	if q.closer != nil {
		return q.closer()
	}
	return nil
}

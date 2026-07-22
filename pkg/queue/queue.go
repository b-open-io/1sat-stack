// Package queue provides a durable, score-ordered work queue abstraction.
//
// Queues are the mechanism producers use to hand block-height-ordered work to a
// single consumer. The first implementation, StoreQueue, wraps a store.Store's
// sorted-set operations using the existing q:* keys, so a service can share a
// queue backend independently of its data store.
package queue

import "context"

// ScoredItem is a queue member with its ordering score. Scores are typically
// derived from block height and index (see pkg/types.HeightScore), which makes
// the queue drain in chain order.
type ScoredItem struct {
	Member []byte
	Score  float64
}

// ReadCfg parameterizes a score-ordered read. Results are returned in ascending
// score order.
type ReadCfg struct {
	From  *float64 // inclusive lower bound; nil = -inf
	To    *float64 // inclusive upper bound; nil = +inf
	Limit int      // max items; 0 = unlimited
}

// Queue is a durable, score-ordered work queue. Single consumer per key,
// multiple producers. At-least-once delivery: items stay queued until Ack.
type Queue interface {
	// Enqueue adds items. Re-adding an existing member updates its score.
	Enqueue(ctx context.Context, key []byte, items ...ScoredItem) error
	// Read returns up to cfg.Limit items in ascending score order.
	Read(ctx context.Context, key []byte, cfg ReadCfg) ([]ScoredItem, error)
	// Ack removes processed members from the queue.
	Ack(ctx context.Context, key []byte, members ...[]byte) error
	// Requeue re-scores an item for later retry.
	Requeue(ctx context.Context, key []byte, item ScoredItem) error
	// Depth returns the number of queued items.
	Depth(ctx context.Context, key []byte) (uint64, error)
	// Close releases resources owned by the queue.
	Close() error
}

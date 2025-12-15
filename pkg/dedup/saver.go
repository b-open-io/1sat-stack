package dedup

import (
	"sync"
)

// Saver provides deduplication for concurrent save operations by key
type Saver[K comparable, T any] struct {
	save       func(K, T) error
	operations sync.Map // map[K]*Operation[struct{}]
}

// NewSaver creates a new deduplicated saver with the specified worker function
func NewSaver[K comparable, T any](save func(K, T) error) *Saver[K, T] {
	return &Saver[K, T]{
		save: save,
	}
}

// Save executes the saver function for the given key, deduplicating concurrent calls.
// If another goroutine is already saving the same key, this call will wait
// for that result instead of executing the saver again.
func (d *Saver[K, T]) Save(key K, data T) error {
	var wg sync.WaitGroup
	wg.Add(1)
	defer wg.Done()

	if inflight, loaded := d.operations.LoadOrStore(key, &Operation[struct{}]{wg: &wg}); loaded {
		op := inflight.(*Operation[struct{}])
		op.wg.Wait()
		return op.err
	} else {
		op := inflight.(*Operation[struct{}])
		op.err = d.save(key, data)
		d.operations.Delete(key)
		return op.err
	}
}

// Clear removes all inflight operations
func (d *Saver[K, T]) Clear() {
	d.operations.Range(func(key, value interface{}) bool {
		d.operations.Delete(key)
		return true
	})
}

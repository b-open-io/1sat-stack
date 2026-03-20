package ordfs

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/bsv-blockchain/go-sdk/transaction"
)

type lockEntry struct {
	expiry time.Time
}

// MemoryCoordinator implements CrawlCoordinator using in-memory maps and channels.
type MemoryCoordinator struct {
	mu      sync.Mutex
	locks   map[string]lockEntry
	waiters map[string][]chan string
	done    chan struct{}
}

// NewMemoryCoordinator creates a MemoryCoordinator and starts background cleanup.
func NewMemoryCoordinator() *MemoryCoordinator {
	mc := &MemoryCoordinator{
		locks:   make(map[string]lockEntry),
		waiters: make(map[string][]chan string),
		done:    make(chan struct{}),
	}
	go mc.cleanup()
	return mc
}

func (mc *MemoryCoordinator) AcquireLock(_ context.Context, outpoint *transaction.Outpoint) (bool, error) {
	key := outpoint.String()
	mc.mu.Lock()
	defer mc.mu.Unlock()

	if entry, ok := mc.locks[key]; ok && time.Now().Before(entry.expiry) {
		return false, nil
	}
	mc.locks[key] = lockEntry{expiry: time.Now().Add(15 * time.Second)}
	return true, nil
}

func (mc *MemoryCoordinator) ReleaseLock(outpoint *transaction.Outpoint) {
	key := outpoint.String()
	mc.mu.Lock()
	defer mc.mu.Unlock()
	delete(mc.locks, key)
}

func (mc *MemoryCoordinator) RefreshLocks(_ context.Context, outpoints []*transaction.Outpoint) {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	now := time.Now()
	for _, op := range outpoints {
		key := op.String()
		if _, ok := mc.locks[key]; ok {
			mc.locks[key] = lockEntry{expiry: now.Add(15 * time.Second)}
		}
	}
}

func (mc *MemoryCoordinator) WaitForCrawl(ctx context.Context, outpoint *transaction.Outpoint) error {
	key := outpoint.String()
	ch := make(chan string, 1)

	mc.mu.Lock()
	mc.waiters[key] = append(mc.waiters[key], ch)
	mc.mu.Unlock()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case result := <-ch:
			if result != "" {
				return nil
			}
			return fmt.Errorf("crawl failed")
		case <-ticker.C:
			mc.mu.Lock()
			entry, held := mc.locks[key]
			expired := held && time.Now().After(entry.expiry)
			mc.mu.Unlock()
			if !held || expired {
				return nil
			}
		}
	}
}

func (mc *MemoryCoordinator) PublishComplete(outpoints []*transaction.Outpoint, origin string) {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	for _, op := range outpoints {
		key := op.String()
		for _, ch := range mc.waiters[key] {
			ch <- origin
		}
		delete(mc.waiters, key)
	}
}

func (mc *MemoryCoordinator) PublishFailure(outpoints []*transaction.Outpoint) {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	for _, op := range outpoints {
		key := op.String()
		for _, ch := range mc.waiters[key] {
			ch <- ""
		}
		delete(mc.waiters, key)
	}
}

func (mc *MemoryCoordinator) Close() {
	close(mc.done)
}

func (mc *MemoryCoordinator) cleanup() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-mc.done:
			return
		case <-ticker.C:
			mc.mu.Lock()
			now := time.Now()
			for key, entry := range mc.locks {
				if now.After(entry.expiry) {
					delete(mc.locks, key)
				}
			}
			mc.mu.Unlock()
		}
	}
}

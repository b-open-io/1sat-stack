package spends

import (
	"container/list"
	"context"
	"sync"
	"sync/atomic"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

const entrySize = 36 + 32 // outpoint key + chainhash value

type LRUSpendStorage struct {
	maxBytes    int64
	currentSize atomic.Int64
	cache       map[[36]byte]chainhash.Hash
	lruIndex    map[[36]byte]*list.Element
	lru         *list.List
	mu          sync.RWMutex
}

func NewLRUSpendStorage(maxBytes int64) *LRUSpendStorage {
	return &LRUSpendStorage{
		maxBytes: maxBytes,
		cache:    make(map[[36]byte]chainhash.Hash),
		lruIndex: make(map[[36]byte]*list.Element),
		lru:      list.New(),
	}
}

func outpointKey(op *transaction.Outpoint) [36]byte {
	var key [36]byte
	copy(key[:], op.Bytes())
	return key
}

func (l *LRUSpendStorage) GetSpend(ctx context.Context, outpoint *transaction.Outpoint) (*chainhash.Hash, error) {
	key := outpointKey(outpoint)

	l.mu.Lock()
	defer l.mu.Unlock()

	txid, found := l.cache[key]
	if !found {
		return nil, nil
	}

	if elem, exists := l.lruIndex[key]; exists {
		l.lru.MoveToFront(elem)
	}

	result := txid
	return &result, nil
}

func (l *LRUSpendStorage) PutSpend(ctx context.Context, outpoint *transaction.Outpoint, spendTxid *chainhash.Hash) error {
	if spendTxid == nil {
		return nil
	}

	key := outpointKey(outpoint)

	l.mu.Lock()
	defer l.mu.Unlock()

	if _, found := l.cache[key]; found {
		l.cache[key] = *spendTxid
		if elem, exists := l.lruIndex[key]; exists {
			l.lru.MoveToFront(elem)
		}
		return nil
	}

	l.cache[key] = *spendTxid
	elem := l.lru.PushFront(key)
	l.lruIndex[key] = elem
	l.currentSize.Add(entrySize)

	l.evictIfNeeded()
	return nil
}

func (l *LRUSpendStorage) evictIfNeeded() {
	for l.currentSize.Load() > l.maxBytes && l.lru.Len() > 0 {
		oldest := l.lru.Back()
		if oldest == nil {
			break
		}
		key := oldest.Value.([36]byte)
		delete(l.cache, key)
		delete(l.lruIndex, key)
		l.lru.Remove(oldest)
		l.currentSize.Add(-entrySize)
	}
}

func (l *LRUSpendStorage) GetSpends(ctx context.Context, outpoints []*transaction.Outpoint) ([]*chainhash.Hash, error) {
	results := make([]*chainhash.Hash, len(outpoints))
	for i, op := range outpoints {
		if op == nil {
			continue
		}
		spend, err := l.GetSpend(ctx, op)
		if err != nil {
			return nil, err
		}
		results[i] = spend
	}
	return results, nil
}

func (l *LRUSpendStorage) DeleteSpend(ctx context.Context, outpoint *transaction.Outpoint) error {
	key := outpointKey(outpoint)

	l.mu.Lock()
	defer l.mu.Unlock()

	if _, found := l.cache[key]; !found {
		return nil
	}

	if elem, exists := l.lruIndex[key]; exists {
		l.lru.Remove(elem)
		delete(l.lruIndex, key)
	}
	delete(l.cache, key)
	l.currentSize.Add(-entrySize)

	return nil
}

func (l *LRUSpendStorage) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.cache = make(map[[36]byte]chainhash.Hash)
	l.lruIndex = make(map[[36]byte]*list.Element)
	l.lru.Init()
	l.currentSize.Store(0)

	return nil
}

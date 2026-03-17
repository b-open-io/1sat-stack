package sync

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"

	"github.com/dgraph-io/badger/v4"
)

// ProgressStore tracks sync state for dedup and resumption.
type ProgressStore interface {
	HasTxid(ctx context.Context, txid string) (bool, error)
	AddTxid(ctx context.Context, txid string) error
	LastScore(ctx context.Context) (float64, error)
	SetLastScore(ctx context.Context, score float64) error
	Close() error
}

type badgerProgressStore struct {
	db *badger.DB
}

var (
	txidPrefix   = []byte("txid:")
	lastScoreKey = []byte("last_score")
)

func NewBadgerProgressStore(path string) (ProgressStore, error) {
	opts := badger.DefaultOptions(path).WithLoggingLevel(badger.WARNING)
	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open progress store at %s: %w", path, err)
	}
	return &badgerProgressStore{db: db}, nil
}

func (s *badgerProgressStore) HasTxid(_ context.Context, txid string) (bool, error) {
	err := s.db.View(func(txn *badger.Txn) error {
		_, err := txn.Get(append(txidPrefix, []byte(txid)...))
		return err
	})
	if err == badger.ErrKeyNotFound {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to check txid %s: %w", txid, err)
	}
	return true, nil
}

func (s *badgerProgressStore) AddTxid(_ context.Context, txid string) error {
	err := s.db.Update(func(txn *badger.Txn) error {
		return txn.Set(append(txidPrefix, []byte(txid)...), []byte{})
	})
	if err != nil {
		return fmt.Errorf("failed to add txid %s: %w", txid, err)
	}
	return nil
}

func (s *badgerProgressStore) LastScore(_ context.Context) (float64, error) {
	var score float64
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(lastScoreKey)
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			score = math.Float64frombits(binary.BigEndian.Uint64(val))
			return nil
		})
	})
	if err == badger.ErrKeyNotFound {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("failed to get last score: %w", err)
	}
	return score, nil
}

func (s *badgerProgressStore) SetLastScore(_ context.Context, score float64) error {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, math.Float64bits(score))
	err := s.db.Update(func(txn *badger.Txn) error {
		return txn.Set(lastScoreKey, buf)
	})
	if err != nil {
		return fmt.Errorf("failed to set last score: %w", err)
	}
	return nil
}

func (s *badgerProgressStore) Close() error {
	return s.db.Close()
}

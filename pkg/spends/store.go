package spends

import (
	"context"

	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

var spendHashKey = []byte("spnd")

type StoreSpendStorage struct {
	store store.Store
}

func NewStoreSpendStorage(s store.Store) *StoreSpendStorage {
	return &StoreSpendStorage{store: s}
}

func (s *StoreSpendStorage) GetSpend(ctx context.Context, outpoint *transaction.Outpoint) (*chainhash.Hash, error) {
	spendBytes, err := s.store.HGet(ctx, spendHashKey, outpoint.Bytes())
	if err == store.ErrKeyNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(spendBytes) != 32 {
		return nil, nil
	}
	txid := &chainhash.Hash{}
	copy(txid[:], spendBytes)
	return txid, nil
}

func (s *StoreSpendStorage) GetSpends(ctx context.Context, outpoints []*transaction.Outpoint) ([]*chainhash.Hash, error) {
	if len(outpoints) == 0 {
		return nil, nil
	}
	fields := make([][]byte, len(outpoints))
	for i, op := range outpoints {
		if op != nil {
			fields[i] = op.Bytes()
		}
	}
	values, err := s.store.HMGet(ctx, spendHashKey, fields...)
	if err != nil {
		return nil, err
	}
	results := make([]*chainhash.Hash, len(outpoints))
	for i, v := range values {
		if len(v) == 32 {
			txid := &chainhash.Hash{}
			copy(txid[:], v)
			results[i] = txid
		}
	}
	return results, nil
}

func (s *StoreSpendStorage) PutSpend(ctx context.Context, outpoint *transaction.Outpoint, spendTxid *chainhash.Hash) error {
	return s.store.HSet(ctx, spendHashKey, outpoint.Bytes(), spendTxid[:])
}

func (s *StoreSpendStorage) DeleteSpend(ctx context.Context, outpoint *transaction.Outpoint) error {
	return s.store.HDel(ctx, spendHashKey, outpoint.Bytes())
}

func (s *StoreSpendStorage) Close() error {
	return nil
}

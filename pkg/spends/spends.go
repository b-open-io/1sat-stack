package spends

import (
	"context"
	"errors"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

var ErrNotFound = errors.New("not-found")

type BaseSpendStorage interface {
	GetSpend(ctx context.Context, outpoint *transaction.Outpoint) (*chainhash.Hash, error)
	PutSpend(ctx context.Context, outpoint *transaction.Outpoint, spendTxid *chainhash.Hash) error
	Close() error
}

type Storage struct {
	storages []BaseSpendStorage
}

func NewStorageFromProviders(storages []BaseSpendStorage) *Storage {
	return &Storage{storages: storages}
}

func (s *Storage) GetSpend(ctx context.Context, outpoint *transaction.Outpoint) (*chainhash.Hash, error) {
	for i, storage := range s.storages {
		spendTxid, err := storage.GetSpend(ctx, outpoint)
		if err != nil {
			if err == ctx.Err() {
				return nil, err
			}
			continue
		}
		if spendTxid != nil {
			for j := 0; j < i; j++ {
				s.storages[j].PutSpend(ctx, outpoint, spendTxid)
			}
			return spendTxid, nil
		}
	}
	return nil, nil
}

func (s *Storage) Close() error {
	var errs []error
	for _, storage := range s.storages {
		if err := storage.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errors.New("errors closing storages")
	}
	return nil
}

package spends

import (
	"context"

	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

type TxoSpendStorage struct {
	store *txo.OutputStore
}

func NewTxoSpendStorage(store *txo.OutputStore) *TxoSpendStorage {
	return &TxoSpendStorage{store: store}
}

func (t *TxoSpendStorage) GetSpend(ctx context.Context, outpoint *transaction.Outpoint) (*chainhash.Hash, error) {
	return t.store.GetSpend(ctx, outpoint)
}

func (t *TxoSpendStorage) PutSpend(ctx context.Context, outpoint *transaction.Outpoint, spendTxid *chainhash.Hash) error {
	return nil
}

func (t *TxoSpendStorage) Close() error {
	return nil
}

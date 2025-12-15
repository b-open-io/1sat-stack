package ordfs

import (
	"context"
	"fmt"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// BeefLoader implements Loader using a transaction storage interface
type BeefLoader struct {
	storage TxStorage
	ctx     context.Context
}

// TxStorage is the interface required for loading transactions
type TxStorage interface {
	LoadTx(ctx context.Context, txid *chainhash.Hash) (*transaction.Transaction, error)
}

// SpendTracker is an optional interface for tracking spends
type SpendTracker interface {
	GetSpend(ctx context.Context, outpoint string) (*chainhash.Hash, error)
}

// NewBeefLoader creates a new loader using transaction storage
func NewBeefLoader(ctx context.Context, storage TxStorage) *BeefLoader {
	return &BeefLoader{
		storage: storage,
		ctx:     ctx,
	}
}

// LoadTx loads a transaction by txid
func (l *BeefLoader) LoadTx(txid string) (*transaction.Transaction, error) {
	hash, err := chainhash.NewHashFromHex(txid)
	if err != nil {
		return nil, fmt.Errorf("invalid txid %s: %w", txid, err)
	}
	tx, err := l.storage.LoadTx(l.ctx, hash)
	if err != nil {
		return nil, fmt.Errorf("failed to load tx %s: %w", txid, err)
	}
	return tx, nil
}

// LoadOutput loads a specific output from a transaction
func (l *BeefLoader) LoadOutput(outpoint *transaction.Outpoint) (*transaction.TransactionOutput, error) {
	txid := &outpoint.Txid
	tx, err := l.storage.LoadTx(l.ctx, txid)
	if err != nil {
		return nil, fmt.Errorf("failed to load tx for output %s: %w", outpoint.String(), err)
	}

	if int(outpoint.Index) >= len(tx.Outputs) {
		return nil, fmt.Errorf("output index %d out of range (tx has %d outputs)", outpoint.Index, len(tx.Outputs))
	}

	return tx.Outputs[outpoint.Index], nil
}

// LoadSpend returns the spending txid for an outpoint
// Returns nil if the output is unspent or spend tracking is not supported
func (l *BeefLoader) LoadSpend(outpoint string) (*chainhash.Hash, error) {
	// Check if storage supports spend tracking
	if tracker, ok := l.storage.(SpendTracker); ok {
		return tracker.GetSpend(l.ctx, outpoint)
	}
	// Return nil to indicate spend tracking not supported
	return nil, nil
}

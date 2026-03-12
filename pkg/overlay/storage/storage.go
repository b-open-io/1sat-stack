package storage

import (
	"context"
	"database/sql"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// OutputRecord represents a row in the outputs table.
type OutputRecord struct {
	Outpoint       transaction.Outpoint
	Txid           chainhash.Hash
	Satoshis       uint64
	SpendTxid      *chainhash.Hash
	Score          float64
	Deps           []byte
	InputsConsumed []byte
	ConsumedBy     []byte
	CreatedAt      int64
}

// QueryOpts controls pagination for list queries.
type QueryOpts struct {
	Since   float64 // Return outputs with score > Since
	Limit   uint32  // Max results (0 = unlimited)
	Reverse bool    // If true, order by score descending
}

// TopicStorage is the per-topic overlay database.
// The overlay engine writes membership/GASP state.
// Lookup services write scoped events and optionally use DB() for custom tables.
type TopicStorage interface {
	// --- Engine writes (generic for all overlays) ---
	InsertOutput(ctx context.Context, op *transaction.Outpoint, txid *chainhash.Hash, satoshis uint64, deps []byte, inputsConsumed []byte, score float64) error
	GetOutput(ctx context.Context, op *transaction.Outpoint) (*OutputRecord, error)
	FindOutputs(ctx context.Context, outpoints []*transaction.Outpoint) ([]OutputRecord, error)
	FindOutputsForTransaction(ctx context.Context, txid *chainhash.Hash) ([]OutputRecord, error)
	MarkSpent(ctx context.Context, op *transaction.Outpoint, spendTxid *chainhash.Hash) error
	UpdateConsumedBy(ctx context.Context, op *transaction.Outpoint, consumedBy []byte) error
	FindUTXOs(ctx context.Context, opts *QueryOpts) ([]OutputRecord, error)
	DeleteOutput(ctx context.Context, op *transaction.Outpoint) error
	Rollback(ctx context.Context, txid *chainhash.Hash) error

	InsertAppliedTx(ctx context.Context, txid *chainhash.Hash, score float64) error
	HasAppliedTx(ctx context.Context, txid *chainhash.Hash) (bool, error)

	// --- GASP peer sync ---
	UpdateLastInteraction(ctx context.Context, host string, since float64) error
	GetLastInteraction(ctx context.Context, host string) (float64, error)

	// --- Lookup service writes (overlay-scoped events) ---
	SaveEvent(ctx context.Context, event string, op *transaction.Outpoint, score float64) error
	DeleteEvent(ctx context.Context, event string, op *transaction.Outpoint) error
	FindByEvent(ctx context.Context, event string, opts *QueryOpts) ([]OutputRecord, error)

	// --- Escape hatch for custom lookup schemas ---
	DB() *sql.DB

	Close() error
}

// Factory creates a TopicStorage instance per topic.
// SQLite impl creates {basePath}_{topic}.db with WAL mode.
type Factory func(topic string) (TopicStorage, error)

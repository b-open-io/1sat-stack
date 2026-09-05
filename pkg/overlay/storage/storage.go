package storage

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

const (
	MutationPhaseActive          MutationPhase = "active"
	MutationPhaseApplied         MutationPhase = "applied"
	MutationPhaseRollbackPending MutationPhase = "rollback_pending"
)

// MutationPhase is the durable state of a directly-invoked reversible storage
// mutation. No production engine path invokes these primitives yet.
type MutationPhase string

// MutationRecord identifies a durable mutation for startup recovery callers.
type MutationRecord struct {
	Txid      chainhash.Hash
	Phase     MutationPhase
	CreatedAt int64
}

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

// RollbackResult describes the durable effects of a reversible mutation.
// The result remains queryable until FinalizeRollback is explicitly called.
type RollbackResult struct {
	Evicted  []transaction.Outpoint
	Restored []OutputRecord
}

// ReversibleTopicStorage is an additive storage-only extension for durable,
// reversible mutations. It is deliberately absent from production wiring.
type ReversibleTopicStorage interface {
	TopicStorage
	BeginMutation(ctx context.Context, txid *chainhash.Hash, directInputs []*transaction.Outpoint) error
	CommitMutation(ctx context.Context, txid *chainhash.Hash, score float64) error
	GetMutation(ctx context.Context, txid *chainhash.Hash) (*MutationRecord, error)
	ListMutations(ctx context.Context) ([]MutationRecord, error)
	HasMutationGuard(ctx context.Context, txid *chainhash.Hash) (bool, error)
	RollbackMutation(ctx context.Context, txid *chainhash.Hash) (*RollbackResult, error)
	FinalizeRollback(ctx context.Context, txid *chainhash.Hash) error
	PruneMutation(ctx context.Context, txid *chainhash.Hash) error
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
	TopicID() int

	Close() error
}

// Factory creates a TopicStorage instance per topic.
// SQLite impl creates {basePath}_{topic}.db with WAL mode.
type Factory func(topic string) (TopicStorage, error)

func decodeOutpoints(raw []byte) ([]*transaction.Outpoint, error) {
	if len(raw)%36 != 0 {
		return nil, fmt.Errorf("invalid inputs_consumed length %d", len(raw))
	}
	outpoints := make([]*transaction.Outpoint, 0, len(raw)/36)
	for i := 0; i < len(raw); i += 36 {
		op := transaction.NewOutpointFromBytes(raw[i : i+36])
		if op == nil {
			return nil, fmt.Errorf("invalid outpoint at offset %d", i)
		}
		outpoints = append(outpoints, op)
	}
	return outpoints, nil
}

func newMutationRecord(txidBytes []byte, phase string, createdAt int64) (*MutationRecord, error) {
	if len(txidBytes) != chainhash.HashSize {
		return nil, fmt.Errorf("invalid mutation transaction ID length %d", len(txidBytes))
	}
	mutationPhase := MutationPhase(phase)
	switch mutationPhase {
	case MutationPhaseActive, MutationPhaseApplied, MutationPhaseRollbackPending:
	default:
		return nil, fmt.Errorf("invalid mutation phase %q", phase)
	}
	record := &MutationRecord{Phase: mutationPhase, CreatedAt: createdAt}
	copy(record.Txid[:], txidBytes)
	return record, nil
}

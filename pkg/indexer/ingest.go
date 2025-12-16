package indexer

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// Log keys for transaction tracking
const (
	PendingTxLog   = "tx:pending"
	RollbackTxLog  = "tx:rollback"
	ImmutableTxLog = "tx:immutable"
	RejectedTxLog  = "tx:rejected"
)

// IngestCtx manages the ingestion configuration and process
type IngestCtx struct {
	Tag         string
	Key         string
	Indexers    []Indexer
	Concurrency uint
	Verbose     bool
	PageSize    uint32
	Limit       uint32
	Network     types.Network
	OnIngest    func(ctx context.Context, idxCtx *IndexContext) error
	Once        bool
	Store       *txo.OutputStore
	BeefStorage *beef.Storage
	Logger      *slog.Logger
}

// IndexedTags returns the tags of all registered indexers
func (cfg *IngestCtx) IndexedTags() []string {
	indexedTags := make([]string, 0, len(cfg.Indexers))
	for _, indexer := range cfg.Indexers {
		indexedTags = append(indexedTags, indexer.Tag())
	}
	return indexedTags
}

// ParseTxid parses a transaction by its txid
func (cfg *IngestCtx) ParseTxid(ctx context.Context, txid string) (*IndexContext, error) {
	hash, err := chainhash.NewHashFromHex(txid)
	if err != nil {
		return nil, err
	}
	tx, err := cfg.BeefStorage.LoadTx(ctx, hash)
	if err != nil {
		return nil, err
	}
	return cfg.ParseTx(ctx, tx)
}

// ParseTx parses a transaction and its inputs
func (cfg *IngestCtx) ParseTx(ctx context.Context, tx *transaction.Transaction) (idxCtx *IndexContext, err error) {
	// Load source transactions for inputs
	for _, input := range tx.Inputs {
		if input.SourceTransaction == nil {
			sourceTx, err := cfg.BeefStorage.LoadTx(ctx, input.SourceTXID)
			if err != nil {
				cfg.log().Error("LoadTx error", "txid", input.SourceTXID.String(), "error", err)
				return nil, err
			}
			input.SourceTransaction = sourceTx
		}
	}
	idxCtx = NewIndexContext(ctx, cfg.Store, tx, cfg.Indexers, cfg.Network)
	err = idxCtx.ParseTxn()
	return
}

// IngestTxid ingests a transaction by its txid
func (cfg *IngestCtx) IngestTxid(ctx context.Context, txid string) (*IndexContext, error) {
	hash, err := chainhash.NewHashFromHex(txid)
	if err != nil {
		return nil, err
	}

	if cfg.Once {
		if score, err := cfg.Store.LogScore(ctx, LogKey(cfg.Tag), hash[:]); err != nil {
			return nil, err
		} else if score > 0 {
			cfg.log().Debug("skipping already ingested", "txid", txid)
			return nil, nil
		}
	}

	tx, err := cfg.BeefStorage.LoadTx(ctx, hash)
	if err != nil {
		cfg.log().Error("LoadTx error", "txid", txid, "error", err)
		return nil, err
	}
	if tx == nil {
		return nil, fmt.Errorf("missing-txn %s", txid)
	}

	return cfg.IngestTx(ctx, tx)
}

// IngestTx ingests a transaction
func (cfg *IngestCtx) IngestTx(ctx context.Context, tx *transaction.Transaction) (idxCtx *IndexContext, err error) {
	start := time.Now()

	idxCtx, err = cfg.ParseTx(ctx, tx)
	if err != nil {
		return nil, err
	}

	err = cfg.Save(ctx, idxCtx)
	if err == nil && cfg.Verbose {
		cfg.log().Info("ingested",
			"txid", idxCtx.TxidHex,
			"height", int(idxCtx.Score/1000000000),
			"duration", time.Since(start),
		)
	}

	// Call OnIngest callback if set
	if cfg.OnIngest != nil {
		if err := cfg.OnIngest(ctx, idxCtx); err != nil {
			return idxCtx, err
		}
	}

	return
}

// Save saves the indexed context to the store
func (cfg *IngestCtx) Save(ctx context.Context, idxCtx *IndexContext) (err error) {
	if err = idxCtx.Save(); err != nil {
		return
	}

	txidBytes := idxCtx.Txid[:]

	// Log to pending tx set
	if err = cfg.Store.Log(ctx, PendingTxLog, txidBytes, idxCtx.Score); err != nil {
		return
	}

	// Log to tag-specific set if tag is set
	if len(cfg.Tag) > 0 {
		if err = cfg.Store.Log(ctx, LogKey(cfg.Tag), txidBytes, idxCtx.Score); err != nil {
			return
		}
	}

	return
}

func (cfg *IngestCtx) log() *slog.Logger {
	if cfg.Logger != nil {
		return cfg.Logger
	}
	return slog.Default()
}

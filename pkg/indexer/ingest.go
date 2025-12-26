package indexer

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// IngestCtx manages the ingestion configuration and process
type IngestCtx struct {
	Tags        []string // Which parse tags to run (nil = all defaults)
	Store       *txo.OutputStore
	BeefStorage *beef.Storage
	Logger      *slog.Logger
	Verbose     bool
}

// NewIngestCtx creates a new IngestCtx with the given dependencies
func NewIngestCtx(store *txo.OutputStore, beefStorage *beef.Storage, logger *slog.Logger) *IngestCtx {
	if logger == nil {
		logger = slog.Default()
	}
	return &IngestCtx{
		Store:       store,
		BeefStorage: beefStorage,
		Logger:      logger,
	}
}

// WithTags sets the parse tags to run
func (cfg *IngestCtx) WithTags(tags []string) *IngestCtx {
	cfg.Tags = tags
	return cfg
}

// WithVerbose enables verbose logging
func (cfg *IngestCtx) WithVerbose(verbose bool) *IngestCtx {
	cfg.Verbose = verbose
	return cfg
}

// IngestTxid ingests a transaction by its txid
func (cfg *IngestCtx) IngestTxid(ctx context.Context, txidStr string) (*IndexContext, error) {
	hash, err := chainhash.NewHashFromHex(txidStr)
	if err != nil {
		return nil, fmt.Errorf("invalid txid %s: %w", txidStr, err)
	}

	tx, err := cfg.BeefStorage.LoadTx(ctx, hash)
	if err != nil {
		cfg.Logger.Error("LoadTx error", "txid", txidStr, "error", err)
		return nil, err
	}
	if tx == nil {
		return nil, fmt.Errorf("missing transaction %s", txidStr)
	}

	return cfg.IngestTx(ctx, tx)
}

// IngestTx ingests a transaction
func (cfg *IngestCtx) IngestTx(ctx context.Context, tx *transaction.Transaction) (*IndexContext, error) {
	start := time.Now()

	idxCtx, err := cfg.ParseTx(ctx, tx)
	if err != nil {
		return nil, err
	}

	if err := idxCtx.Save(); err != nil {
		return nil, err
	}

	if cfg.Verbose {
		cfg.Logger.Info("ingested",
			"txid", idxCtx.TxidHex,
			"height", idxCtx.Height,
			"outputs", len(idxCtx.Outputs),
			"spends", len(idxCtx.Spends),
			"duration", time.Since(start),
		)
	}

	return idxCtx, nil
}

// ParseTx parses a transaction and its inputs without saving
func (cfg *IngestCtx) ParseTx(ctx context.Context, tx *transaction.Transaction) (*IndexContext, error) {
	// Load source transactions for inputs if not already populated
	for _, input := range tx.Inputs {
		if input.SourceTransaction == nil && input.SourceTXID != nil {
			sourceTx, err := cfg.BeefStorage.LoadTx(ctx, input.SourceTXID)
			if err != nil {
				cfg.Logger.Error("LoadTx error for input", "txid", input.SourceTXID.String(), "error", err)
				return nil, err
			}
			input.SourceTransaction = sourceTx
		}
	}

	idxCtx := NewIndexContext(ctx, cfg.Store, tx, cfg.Tags)
	if err := idxCtx.ParseTxn(); err != nil {
		return nil, err
	}

	return idxCtx, nil
}

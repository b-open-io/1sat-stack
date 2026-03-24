package indexer

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/ordfs"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/b-open-io/1sat-stack/pkg/wasmparse"
	parsepb "github.com/b-open-io/1sat-stack/proto/parsepb"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// IngestCtx manages the ingestion configuration and process
type IngestCtx struct {
	Tags        []string // Which parse tags to run (nil = all defaults)
	Store       *txo.OutputStore
	BeefStorage *beef.Storage
	Ordfs       *ordfs.Ordfs
	WasmModule  *wasmparse.Module
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

// WithOrdfs sets the ORDFS service for origin resolution
func (cfg *IngestCtx) WithOrdfs(o *ordfs.Ordfs) *IngestCtx {
	cfg.Ordfs = o
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

	// Use BuildFullBeefTx to load transaction with all input source transactions populated
	tx, err := cfg.BeefStorage.BuildFullBeefTx(ctx, hash)
	if err != nil {
		cfg.Logger.Error("BuildFullBeefTx error", "txid", txidStr, "error", err)
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

	// Log parse results before save
	if cfg.Verbose {
		eventCount := 0
		ownerCount := 0
		for _, o := range idxCtx.Outputs {
			if o != nil {
				eventCount += len(o.Events)
				ownerCount += len(o.Owners)
			}
		}
		cfg.Logger.Debug("parsed",
			"txid", idxCtx.TxidHex,
			"height", idxCtx.Height,
			"outputs", len(idxCtx.Outputs),
			"spends", len(idxCtx.Spends),
			"events", eventCount,
			"owners", ownerCount,
			"hasOrdfs", idxCtx.Ordfs != nil,
		)
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

// ParseTx parses a transaction and its inputs without saving.
// Uses WASM module if available, otherwise falls back to Go-native parsing.
// Note: Expects tx.Inputs[].SourceTransaction to be pre-populated (e.g., via BuildFullBeefTx).
func (cfg *IngestCtx) ParseTx(ctx context.Context, tx *transaction.Transaction) (*IndexContext, error) {
	if cfg.WasmModule != nil {
		return cfg.parseTxWasm(ctx, tx)
	}
	idxCtx := NewIndexContext(ctx, cfg.Store, cfg.Ordfs, tx, cfg.Tags)
	if err := idxCtx.ParseTxn(); err != nil {
		return nil, err
	}
	return idxCtx, nil
}

func (cfg *IngestCtx) parseTxWasm(ctx context.Context, tx *transaction.Transaction) (*IndexContext, error) {
	result, err := cfg.WasmModule.ParseTransaction(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("wasm parse: %w", err)
	}

	txid, err := chainhash.NewHash(result.Txid)
	if err != nil {
		return nil, fmt.Errorf("invalid txid in parse result: %w", err)
	}

	idxCtx := &IndexContext{
		Tx:      tx,
		Txid:    txid,
		TxidHex: txid.String(),
		Height:  result.BlockHeight,
		Idx:     result.BlockIdx,
		Score:   types.HeightScore(result.BlockHeight, result.BlockIdx),
		Store:   cfg.Store,
		Ordfs:   cfg.Ordfs,
		Ctx:     ctx,
	}

	for _, out := range result.Outputs {
		op := outpointFromProto(out.Outpoint)
		sats := out.Satoshis
		output := &txo.IndexedOutput{
			Outpoint:    *op,
			BlockHeight: &out.BlockHeight,
			BlockIdx:    &out.BlockIdx,
			Satoshis:    &sats,
			Data:        make(map[string]any),
		}
		for _, event := range out.Events {
			output.AddEvent(event)
		}
		for _, ownerBytes := range out.Owners {
			if pkh := types.PKHashFromBytes(ownerBytes); pkh != nil {
				output.AddOwner(*pkh)
			}
		}
		idxCtx.Outputs = append(idxCtx.Outputs, output)
	}

	for _, spend := range result.Spends {
		op := outpointFromProto(spend.Outpoint)
		sats := spend.Satoshis
		s := &txo.IndexedOutput{
			Outpoint: *op,
			Satoshis: &sats,
			Data:     make(map[string]any),
		}
		if len(spend.SpendTxid) == chainhash.HashSize {
			spendHash, _ := chainhash.NewHash(spend.SpendTxid)
			s.SpendTxid = spendHash
		}
		for _, event := range spend.Events {
			s.AddEvent(event)
		}
		for _, ownerBytes := range spend.Owners {
			if pkh := types.PKHashFromBytes(ownerBytes); pkh != nil {
				s.AddOwner(*pkh)
			}
		}
		idxCtx.Spends = append(idxCtx.Spends, s)
	}

	return idxCtx, nil
}

func outpointFromProto(op *parsepb.OutPoint) *transaction.Outpoint {
	if op == nil {
		return &transaction.Outpoint{}
	}
	result := &transaction.Outpoint{
		Index: op.Index,
	}
	if len(op.Txid) == chainhash.HashSize {
		copy(result.Txid[:], op.Txid)
	}
	return result
}

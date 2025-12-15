package indexer

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// Network represents a BSV network
type Network int

const (
	Mainnet Network = iota
	Testnet
)

// HeightScore calculates the score for a block height and index
func HeightScore(height uint32, idx uint64) float64 {
	if height == 0 {
		return float64(time.Now().UnixNano())
	}
	return float64(uint64(height)*1000000000 + idx)
}

// OutputContext holds additional context for an output during indexing
type OutputContext struct {
	OutAcc uint64 // Accumulated satoshis before this output
}

// IndexContext holds the context for indexing a transaction
type IndexContext struct {
	Tx       *transaction.Transaction `json:"-"`
	Txid     *chainhash.Hash          `json:"txid" swaggertype:"string"`
	TxidHex  string                   `json:"-"`
	Height   uint32                   `json:"height"`
	Idx      uint64                   `json:"idx"`
	Score    float64                  `json:"score"`
	Outputs  []*txo.IndexedOutput     `json:"outputs"`
	Spends   []*txo.IndexedOutput     `json:"spends"`
	Indexers []Indexer                `json:"-"`
	Ctx      context.Context          `json:"-"`
	Network  Network                  `json:"-"`
	Store    *txo.OutputStore         `json:"-"`
	tags     []string                 `json:"-"`

	// OutputContexts holds additional per-output context not stored in IndexedOutput
	OutputContexts []*OutputContext `json:"-"`
	SpendContexts  []*OutputContext `json:"-"`
}

// NewIndexContext creates a new IndexContext for the given transaction
func NewIndexContext(ctx context.Context, store *txo.OutputStore, tx *transaction.Transaction, indexers []Indexer, network ...Network) *IndexContext {
	if tx == nil {
		return nil
	}
	idxCtx := &IndexContext{
		Tx:       tx,
		Txid:     tx.TxID(),
		Indexers: indexers,
		Ctx:      ctx,
		Store:    store,
	}
	if len(network) > 0 {
		idxCtx.Network = network[0]
	} else {
		idxCtx.Network = Mainnet
	}
	idxCtx.TxidHex = idxCtx.Txid.String()

	if tx.MerklePath != nil {
		idxCtx.Height = tx.MerklePath.BlockHeight
		for _, path := range tx.MerklePath.Path[0] {
			if idxCtx.Txid.IsEqual(path.Hash) {
				idxCtx.Idx = path.Offset
				break
			}
		}
	}
	idxCtx.Score = HeightScore(idxCtx.Height, idxCtx.Idx)
	for _, indexer := range indexers {
		idxCtx.tags = append(idxCtx.tags, indexer.Tag())
	}
	return idxCtx
}

// ParseTxn parses the transaction spends and outputs
func (idxCtx *IndexContext) ParseTxn() (err error) {
	if err = idxCtx.ParseSpends(); err != nil {
		return
	}
	return idxCtx.ParseOutputs()
}

// ParseSpends parses the inputs (spent outputs) of the transaction
func (idxCtx *IndexContext) ParseSpends() error {
	if idxCtx.Tx.IsCoinbase() {
		return nil
	}
	for _, txin := range idxCtx.Tx.Inputs {
		if txin.SourceTransaction == nil {
			return fmt.Errorf("missing source transaction for input %s", txin.SourceTXID)
		}
		// Parse the source transaction to get the spent output
		spendCtx := NewIndexContext(idxCtx.Ctx, nil, txin.SourceTransaction, idxCtx.Indexers, idxCtx.Network)
		spendCtx.ParseOutputs()
		idxCtx.Spends = append(idxCtx.Spends, spendCtx.Outputs[txin.SourceTxOutIndex])
		idxCtx.SpendContexts = append(idxCtx.SpendContexts, spendCtx.OutputContexts[txin.SourceTxOutIndex])
	}
	return nil
}

// ParseOutputs parses the outputs of the transaction
func (idxCtx *IndexContext) ParseOutputs() (err error) {
	accSats := uint64(0)
	for vout, txout := range idxCtx.Tx.Outputs {
		output := &txo.IndexedOutput{
			Output: engine.Output{
				Outpoint: transaction.Outpoint{
					Txid:  *idxCtx.Txid,
					Index: uint32(vout),
				},
				BlockHeight: idxCtx.Height,
				BlockIdx:    idxCtx.Idx,
			},
			Satoshis: txout.Satoshis,
			Data:     make(map[string]interface{}),
		}
		outCtx := &OutputContext{
			OutAcc: accSats,
		}

		// Extract P2PKH owner if applicable
		if len(*txout.LockingScript) >= 25 && script.NewFromBytes((*txout.LockingScript)[:25]).IsP2PKH() {
			pkhash := (*txout.LockingScript)[3:23]
			output.AddOwnerFromBytes(pkhash)
		}

		idxCtx.Outputs = append(idxCtx.Outputs, output)
		idxCtx.OutputContexts = append(idxCtx.OutputContexts, outCtx)
		accSats += txout.Satoshis

		// Run each indexer on this output
		for _, indexer := range idxCtx.Indexers {
			if data := indexer.Parse(idxCtx, uint32(vout)); data != nil {
				output.SetData(indexer.Tag(), data)
			}
		}
	}

	// Call PreSave on each indexer for batch processing
	for _, indexer := range idxCtx.Indexers {
		indexer.PreSave(idxCtx)
	}
	return nil
}

// Save saves the indexed outputs and spends to the store
func (idxCtx *IndexContext) Save() error {
	if idxCtx.Store == nil {
		return nil
	}

	// Save each output
	for i, output := range idxCtx.Outputs {
		if output == nil {
			continue
		}
		satoshis := idxCtx.Tx.Outputs[i].Satoshis
		if err := idxCtx.Store.SaveOutput(idxCtx.Ctx, output, satoshis, idxCtx.Score); err != nil {
			slog.Error("save output error", "txid", idxCtx.TxidHex, "vout", i, "error", err)
			return err
		}
	}

	// Save spends
	for i, spend := range idxCtx.Spends {
		if spend != nil {
			input := idxCtx.Tx.Inputs[i]
			outpoint := &transaction.Outpoint{
				Txid:  *input.SourceTXID,
				Index: input.SourceTxOutIndex,
			}
			if err := idxCtx.Store.SaveSpend(idxCtx.Ctx, outpoint, idxCtx.Txid, idxCtx.Score); err != nil {
				slog.Error("save spend error", "outpoint", outpoint.String(), "error", err)
				return err
			}
		}
	}

	return nil
}

// GetOutAcc returns the accumulated satoshis before the output at the given index
func (idxCtx *IndexContext) GetOutAcc(vout uint32) uint64 {
	if int(vout) < len(idxCtx.OutputContexts) {
		return idxCtx.OutputContexts[vout].OutAcc
	}
	return 0
}

// GetSpendOutAcc returns the accumulated satoshis for a spend at the given index
func (idxCtx *IndexContext) GetSpendOutAcc(idx int) uint64 {
	if idx < len(idxCtx.SpendContexts) {
		return idxCtx.SpendContexts[idx].OutAcc
	}
	return 0
}

// Tags returns the tags of all registered indexers
func (idxCtx *IndexContext) Tags() []string {
	return idxCtx.tags
}

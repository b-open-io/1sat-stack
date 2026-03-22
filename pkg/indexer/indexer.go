package indexer

import (
	"context"

	"github.com/b-open-io/1sat-stack/pkg/ordfs"
	"github.com/b-open-io/1sat-stack/pkg/parse"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// IndexContext holds the context for indexing a transaction
type IndexContext struct {
	Tx      *transaction.Transaction
	Txid    *chainhash.Hash
	TxidHex string
	Height  uint32
	Idx     uint64
	Score   float64
	Outputs []*txo.IndexedOutput
	Spends  []*txo.IndexedOutput // Spent outputs with their events
	Tags    []string             // Which parse tags to run (nil = all defaults)
	Store   *txo.OutputStore
	Ordfs   *ordfs.Ordfs
	Ctx     context.Context
}

// NewIndexContext creates a new IndexContext for the given transaction
func NewIndexContext(ctx context.Context, store *txo.OutputStore, o *ordfs.Ordfs, tx *transaction.Transaction, tags []string) *IndexContext {
	if tx == nil {
		return nil
	}

	txid := tx.TxID()
	idxCtx := &IndexContext{
		Tx:      tx,
		Txid:    txid,
		TxidHex: txid.String(),
		Tags:    tags,
		Store:   store,
		Ordfs:   o,
		Ctx:     ctx,
	}

	// Extract block height and index from merkle path if available
	if tx.MerklePath != nil {
		idxCtx.Height = tx.MerklePath.BlockHeight
		for _, path := range tx.MerklePath.Path[0] {
			if txid.IsEqual(path.Hash) {
				idxCtx.Idx = path.Offset
				break
			}
		}
	}
	idxCtx.Score = types.HeightScore(idxCtx.Height, idxCtx.Idx)

	return idxCtx
}

// ParseTxn parses both outputs and spends of the transaction
func (idxCtx *IndexContext) ParseTxn() error {
	if err := idxCtx.ParseSpends(); err != nil {
		return err
	}
	return idxCtx.ParseOutputs()
}

// computeOrigins determines which 1-sat outputs are ordinal origins vs transfers.
// A 1-sat output is a transfer if its corresponding satoshi came from a 1-sat input
// (by summing input/output satoshis in order). Otherwise it's an origin.
func computeOrigins(tx *transaction.Transaction) []bool {
	origins := make([]bool, len(tx.Outputs))

	// Build input satoshi prefix sums to map output satoshi positions to inputs
	inputSats := make([]uint64, len(tx.Inputs))
	for i, inp := range tx.Inputs {
		if inp.SourceTransaction != nil {
			inputSats[i] = inp.SourceTransaction.Outputs[inp.SourceTxOutIndex].Satoshis
		}
	}

	var outputSatsBefore uint64
	for vout, txout := range tx.Outputs {
		if txout.Satoshis == 1 {
			var inputSatsBefore uint64
			isTransfer := false
			for _, sats := range inputSats {
				if inputSatsBefore+sats > outputSatsBefore {
					if sats == 1 {
						isTransfer = true
					}
					break
				}
				inputSatsBefore += sats
			}
			origins[vout] = !isTransfer
		}
		outputSatsBefore += txout.Satoshis
	}

	return origins
}

// ParseOutputs parses all outputs of the transaction using parse.Parse directly
func (idxCtx *IndexContext) ParseOutputs() error {
	origins := computeOrigins(idxCtx.Tx)

	for vout, txout := range idxCtx.Tx.Outputs {
		outpoint := &transaction.Outpoint{
			Txid:  *idxCtx.Txid,
			Index: uint32(vout),
		}

		sats := txout.Satoshis
		output := &txo.IndexedOutput{
			Outpoint:    *outpoint,
			BlockHeight: &idxCtx.Height,
			BlockIdx:    &idxCtx.Idx,
			Satoshis:    &sats,
			Data:        make(map[string]any),
		}

		results, err := parse.Parse(&parse.ParseContext{
			Outpoint:      outpoint,
			LockingScript: txout.LockingScript.Bytes(),
			Satoshis:      txout.Satoshis,
			IsOrigin:      origins[vout],
			Ctx:           idxCtx.Ctx,
			Ordfs:         idxCtx.Ordfs,
		}, idxCtx.Tags)
		if err != nil {
			return err
		}

		// Collect events and owners from parse results
		for tag, result := range results {
			// Add prefixed events
			for _, event := range result.Events {
				output.AddEvent(event)
			}

			// Add owners
			for _, owner := range result.Owners {
				output.AddOwner(*owner)
			}

			// Store tag data
			if result.Data != nil {
				output.SetData(tag, result.Data)
			}
		}

		idxCtx.Outputs = append(idxCtx.Outputs, output)
	}

	return nil
}

// ParseSpends parses the inputs (spent outputs) of the transaction
func (idxCtx *IndexContext) ParseSpends() error {
	if idxCtx.Tx.IsCoinbase() {
		return nil
	}

	for _, txin := range idxCtx.Tx.Inputs {
		if txin.SourceTransaction == nil {
			// Cannot parse spend without source transaction
			idxCtx.Spends = append(idxCtx.Spends, nil)
			continue
		}

		if int(txin.SourceTxOutIndex) >= len(txin.SourceTransaction.Outputs) {
			idxCtx.Spends = append(idxCtx.Spends, nil)
			continue
		}

		spentOutput := txin.SourceTransaction.Outputs[txin.SourceTxOutIndex]
		outpoint := &transaction.Outpoint{
			Txid:  *txin.SourceTXID,
			Index: txin.SourceTxOutIndex,
		}

		// Parse the spent output to derive events (no origin resolution needed for spends)
		results, err := parse.Parse(&parse.ParseContext{
			Outpoint:      outpoint,
			LockingScript: spentOutput.LockingScript.Bytes(),
			Satoshis:      spentOutput.Satoshis,
		}, idxCtx.Tags)
		if err != nil {
			return err
		}

		sats := spentOutput.Satoshis
		spend := &txo.IndexedOutput{
			Outpoint:  *outpoint,
			Satoshis:  &sats,
			Data:      make(map[string]any),
			SpendTxid: idxCtx.Txid,
		}

		// Collect events and owners from parse results
		for tag, result := range results {
			for _, event := range result.Events {
				spend.AddEvent(event)
			}
			for _, owner := range result.Owners {
				spend.AddOwner(*owner)
			}
			if result.Data != nil {
				spend.SetData(tag, result.Data)
			}
		}

		idxCtx.Spends = append(idxCtx.Spends, spend)
	}

	return nil
}

// Save saves the indexed outputs, spends, and pending log to the store.
func (idxCtx *IndexContext) Save() error {
	if idxCtx.Store == nil {
		return nil
	}
	return idxCtx.Store.SaveTransaction(idxCtx.Ctx, idxCtx.Tx, idxCtx.Outputs, idxCtx.Spends, idxCtx.TxidHex, idxCtx.Score)
}

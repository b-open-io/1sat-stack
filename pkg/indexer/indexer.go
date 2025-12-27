package indexer

import (
	"context"
	"log/slog"

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
	Ctx     context.Context
}

// NewIndexContext creates a new IndexContext for the given transaction
func NewIndexContext(ctx context.Context, store *txo.OutputStore, tx *transaction.Transaction, tags []string) *IndexContext {
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

// ParseOutputs parses all outputs of the transaction using parse.Parse directly
func (idxCtx *IndexContext) ParseOutputs() error {
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

		// Call parse.Parse directly
		results := parse.Parse(outpoint, txout.LockingScript.Bytes(), txout.Satoshis, idxCtx.Tags)

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

		// Parse the spent output to derive events
		results := parse.Parse(outpoint, spentOutput.LockingScript.Bytes(), spentOutput.Satoshis, idxCtx.Tags)

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

	// Save spends - this is the key fix for proper spend indexing
	for i, spend := range idxCtx.Spends {
		if spend == nil {
			continue
		}

		input := idxCtx.Tx.Inputs[i]
		outpoint := &transaction.Outpoint{
			Txid:  *input.SourceTXID,
			Index: input.SourceTxOutIndex,
		}

		// Build events list for the spent output
		events := buildEventsFromOutput(spend)

		if err := idxCtx.Store.SaveSpend(idxCtx.Ctx, outpoint, idxCtx.Txid, events, idxCtx.Score); err != nil {
			slog.Error("save spend error", "outpoint", outpoint.String(), "error", err)
			return err
		}
	}

	return nil
}

// buildEventsFromOutput constructs the full events list for an output
func buildEventsFromOutput(output *txo.IndexedOutput) []string {
	events := make([]string, 0, len(output.Events)+len(output.Owners)+1)
	events = append(events, "txid:"+output.Outpoint.Txid.String())
	events = append(events, output.Events...)
	for _, owner := range output.Owners {
		if !owner.IsZero() {
			events = append(events, "own:"+owner.Address())
		}
	}
	return events
}

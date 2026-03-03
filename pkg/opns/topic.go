package opns

import (
	"context"

	"github.com/bitcoin-sv/go-templates/template/opns"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// TopicManager implements the overlay TopicManager interface for OpNS protocol outputs.
type TopicManager struct{}

// IdentifyAdmissibleOutputs determines which transaction outputs contain valid OpNS protocol data.
func (tm *TopicManager) IdentifyAdmissibleOutputs(ctx context.Context, beef *transaction.Beef, txid *chainhash.Hash, previousCoins []uint32) (admit overlay.AdmittanceInstructions, err error) {
	tx := beef.FindTransactionForSigningByHash(txid)
	if tx == nil {
		return admit, engine.ErrInvalidBeef
	}

	// If this is the genesis transaction, admit output 0
	if txid.IsEqual(&opns.GENESIS.Txid) {
		admit.OutputsToAdmit = append(admit.OutputsToAdmit, 0)
		return
	}

	if len(previousCoins) == 0 {
		return
	}

	for _, vin := range previousCoins {
		if int(vin) >= len(tx.Inputs) {
			continue
		}
		txin := tx.Inputs[vin]
		if txin.SourceTransaction == nil {
			continue
		}
		if int(txin.SourceTxOutIndex) >= len(txin.SourceTransaction.Outputs) {
			continue
		}
		txout := txin.SourceTransaction.Outputs[txin.SourceTxOutIndex]

		if o := opns.Decode(txout.LockingScript); o != nil || txin.SourceTXID.IsEqual(&opns.GENESIS.Txid) {
			// This input spends an OpNS contract output or the genesis output
			// Outputs: 0=left mine, 1=right mine, 2=name ordinal
			admit.CoinsToRetain = append(admit.CoinsToRetain, vin)
			admit.OutputsToAdmit = []uint32{0, 1, 2}
		}
	}
	return
}

// IdentifyNeededInputs returns the list of inputs needed for validation.
// OpNS does not require any additional inputs.
func (tm *TopicManager) IdentifyNeededInputs(ctx context.Context, beef *transaction.Beef, txid *chainhash.Hash) ([]*transaction.Outpoint, error) {
	return nil, nil
}

// GetDocumentation returns documentation for this topic manager.
func (tm *TopicManager) GetDocumentation() string {
	return "OpNS Topic Manager"
}

// GetMetaData returns metadata for this topic manager.
func (tm *TopicManager) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{
		Name: "OpNS",
	}
}

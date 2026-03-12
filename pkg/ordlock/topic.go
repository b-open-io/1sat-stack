package ordlock

import (
	"context"
	"slices"

	"github.com/b-open-io/1sat-stack/pkg/overlay"
	"github.com/bitcoin-sv/go-templates/template/ordlock"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	sdkoverlay "github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

type TopicManager struct{}

func (tm *TopicManager) IdentifyAdmissibleOutputs(ctx context.Context, beef *transaction.Beef, txid *chainhash.Hash, previousCoins []uint32) (admit sdkoverlay.AdmittanceInstructions, err error) {
	tx := beef.FindTransactionForSigningByHash(txid)
	if tx == nil {
		return admit, engine.ErrInvalidBeef
	}

	// Admit outputs that contain OrdLock scripts (new listings)
	for vout, output := range tx.Outputs {
		if output.Satoshis != 1 {
			continue
		}
		if ol := ordlock.Decode(output.LockingScript); ol != nil {
			admit.OutputsToAdmit = append(admit.OutputsToAdmit, uint32(vout))
		}
	}

	// Check inputs for spends of existing listings
	spendFound := false
	for vin, txin := range tx.Inputs {
		sourceOutput := txin.SourceTxOutput()
		if sourceOutput == nil || sourceOutput.Satoshis != 1 {
			continue
		}
		if ol := ordlock.Decode(sourceOutput.LockingScript); ol == nil {
			continue
		}

		// This input spends an OrdLock output
		if slices.Contains(previousCoins, uint32(vin)) {
			admit.CoinsToRetain = append(admit.CoinsToRetain, uint32(vin))
			spendFound = true
		} else {
			return admit, &overlay.MissingInputError{
				TransactionID: txid,
				InputIndex:    uint32(vin),
				MissingTxID:   txin.SourceTXID,
				OutputIndex:   txin.SourceTxOutIndex,
				Topic:         TopicName,
			}
		}
	}

	// If this tx spends listings, admit the 1-sat outputs (ordinals moving to buyer/seller)
	// so the spending tx is stored in the overlay for GASP propagation
	if spendFound {
		for vout, output := range tx.Outputs {
			if output.Satoshis == 1 && ordlock.Decode(output.LockingScript) == nil {
				admit.OutputsToAdmit = append(admit.OutputsToAdmit, uint32(vout))
			}
		}
	}

	return
}

func (tm *TopicManager) IdentifyNeededInputs(ctx context.Context, beef *transaction.Beef, txid *chainhash.Hash) ([]*transaction.Outpoint, error) {
	tx := beef.FindTransactionForSigningByHash(txid)
	if tx == nil {
		return nil, engine.ErrInvalidBeef
	}

	var inputs []*transaction.Outpoint
	for _, txin := range tx.Inputs {
		if txin.SourceTransaction == nil {
			inputs = append(inputs, &transaction.Outpoint{
				Txid:  *txin.SourceTXID,
				Index: txin.SourceTxOutIndex,
			})
		}
	}
	return inputs, nil
}

func (tm *TopicManager) GetDocumentation() string {
	return "OrdLock Topic Manager"
}

func (tm *TopicManager) GetMetaData() *sdkoverlay.MetaData {
	return &sdkoverlay.MetaData{
		Name: "ordlock",
	}
}

package bap

import (
	"context"

	"github.com/b-open-io/1sat-stack/pkg/template/bitcom"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// TopicManager implements the overlay TopicManager interface for BAP protocol outputs.
// Performs structural validation only — authority is resolved at query time.
type TopicManager struct{}

// IdentifyAdmissibleOutputs admits outputs that contain valid BAP protocol data
// with a valid AIP signature. No state or identity checks.
func (tm *TopicManager) IdentifyAdmissibleOutputs(ctx context.Context, beef *transaction.Beef, txid *chainhash.Hash, previousCoins []uint32) (admit overlay.AdmittanceInstructions, err error) {
	tx := beef.FindTransactionForSigningByHash(txid)
	if tx == nil {
		return admit, engine.ErrInvalidBeef
	}

	for vout, output := range tx.Outputs {
		bc := bitcom.Decode(output.LockingScript)
		if bc == nil {
			continue
		}
		bap := bitcom.DecodeBAP(bc)
		if bap == nil {
			continue
		}
		var hasValidAIP bool
		for _, a := range bitcom.DecodeAIP(bc) {
			if a.Valid {
				hasValidAIP = true
				break
			}
		}
		if !hasValidAIP {
			continue
		}
		admit.OutputsToAdmit = append(admit.OutputsToAdmit, uint32(vout))
	}
	return
}

// IdentifyNeededInputs returns the list of inputs needed for validation.
// BAP does not require any additional inputs.
func (tm *TopicManager) IdentifyNeededInputs(ctx context.Context, beef *transaction.Beef, txid *chainhash.Hash) ([]*transaction.Outpoint, error) {
	return nil, nil
}

// GetDocumentation returns documentation for this topic manager.
func (tm *TopicManager) GetDocumentation() string {
	return "BAP Topic Manager"
}

// GetMetaData returns metadata for this topic manager.
func (tm *TopicManager) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{
		Name: "bap",
	}
}

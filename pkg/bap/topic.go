package bap

import (
	"context"
	"fmt"

	"github.com/bitcoin-sv/go-templates/template/bitcom"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// BAPStateError is returned when a BAP operation is rejected because the identity
// state doesn't match (e.g., signer is not the current owner). When returned from
// IdentifyAdmissibleOutputs, the overlay engine will NOT mark the tx as applied,
// allowing it to be resubmitted after the dependency is processed.
type BAPStateError struct {
	Txid    string
	Address string
	BapType string
	Message string
}

func (e *BAPStateError) Error() string {
	return fmt.Sprintf("BAP state mismatch: %s operation in tx %s signed by %s - %s",
		e.BapType, e.Txid, e.Address, e.Message)
}

// TopicManager implements the overlay TopicManager interface for BAP protocol outputs.
type TopicManager struct {
	Lookup *LookupService
}

// IdentifyAdmissibleOutputs determines which transaction outputs contain valid BAP protocol data.
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
		var aip *bitcom.AIP
		for _, a := range bitcom.DecodeAIP(bc) {
			if a.Valid {
				aip = a
				break
			}
		}
		if aip == nil {
			continue
		}
		id, err := tm.Lookup.LoadIdentityByAddress(ctx, aip.Address)
		if err != nil {
			return admit, err
		}
		switch bap.Type {
		case bitcom.ID:
			if id == nil {
				admit.OutputsToAdmit = append(admit.OutputsToAdmit, uint32(vout))
			} else if id.CurrentAddress == aip.Address {
				admit.OutputsToAdmit = append(admit.OutputsToAdmit, uint32(vout))
			} else {
				return admit, &BAPStateError{
					Txid:    txid.String(),
					Address: aip.Address,
					BapType: string(bap.Type),
					Message: "signer is not the current identity owner",
				}
			}

		case bitcom.ATTEST, bitcom.REVOKE, bitcom.ALIAS:
			if id == nil {
				return admit, &BAPStateError{
					Txid:    txid.String(),
					Address: aip.Address,
					BapType: string(bap.Type),
					Message: "identity not found",
				}
			} else if id.CurrentAddress == aip.Address {
				admit.OutputsToAdmit = append(admit.OutputsToAdmit, uint32(vout))
			} else {
				return admit, &BAPStateError{
					Txid:    txid.String(),
					Address: aip.Address,
					BapType: string(bap.Type),
					Message: "signer is not the current identity owner",
				}
			}
		}
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


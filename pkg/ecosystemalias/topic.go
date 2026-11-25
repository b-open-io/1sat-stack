package ecosystemalias

import (
	"context"

	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// TopicManager admits valid BRC-169 ecosystem-alias claims.
type TopicManager struct{}

var _ engine.TopicManager = (*TopicManager)(nil)

// IdentifyAdmissibleOutputs admits every output that independently decodes as
// a valid ecosystem-alias claim. Conflicting claims are intentionally retained.
func (tm *TopicManager) IdentifyAdmissibleOutputs(_ context.Context, beef *transaction.Beef, txid *chainhash.Hash, _ []uint32) (admit overlay.AdmittanceInstructions, err error) {
	if beef == nil || txid == nil {
		return admit, engine.ErrInvalidBeef
	}
	tx := beef.FindTransactionForSigningByHash(txid)
	if tx == nil {
		return admit, engine.ErrInvalidBeef
	}

	for vout, output := range tx.Outputs {
		if output == nil {
			continue
		}
		if _, err := Decode(output.LockingScript, output.Satoshis); err == nil {
			admit.OutputsToAdmit = append(admit.OutputsToAdmit, uint32(vout))
		}
	}

	return admit, nil
}

// IdentifyNeededInputs returns no dependencies because each claim is fully
// validated from its own locking script and satoshi value.
func (tm *TopicManager) IdentifyNeededInputs(_ context.Context, _ *transaction.Beef, _ *chainhash.Hash) ([]*transaction.Outpoint, error) {
	return nil, nil
}

// GetDocumentation returns documentation for this topic manager.
func (tm *TopicManager) GetDocumentation() string {
	return "BRC-169 Ecosystem Alias Topic Manager"
}

// GetMetaData returns metadata for the exact BRC-169 topic.
func (tm *TopicManager) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{
		Name:        TopicName,
		Description: "Valid BRC-169 ecosystem-alias claims",
		Version:     ProtocolVersion,
	}
}

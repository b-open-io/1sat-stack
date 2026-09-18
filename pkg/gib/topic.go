package gib

import (
	"context"

	gibtpl "github.com/b-open-io/1sat-stack/pkg/template/gib"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// TopicManager admits gib commit heads into tm_gib.
type TopicManager struct{}

var _ engine.TopicManager = (*TopicManager)(nil)

// IdentifyAdmissibleOutputs admits every output that decodes as a commit
// head. Spent heads are not retained in the engine: the lookup service keeps
// the full push history in its own table.
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
		if gibtpl.IsHead(output.LockingScript, output.Satoshis) {
			admit.OutputsToAdmit = append(admit.OutputsToAdmit, uint32(vout))
		}
	}
	return admit, nil
}

// IdentifyNeededInputs returns nothing: a head is validated from its own
// locking script, so no GASP dependency resolution is required.
func (tm *TopicManager) IdentifyNeededInputs(_ context.Context, _ *transaction.Beef, _ *chainhash.Hash) ([]*transaction.Outpoint, error) {
	return nil, nil
}

// GetDocumentation returns documentation for this topic manager.
func (tm *TopicManager) GetDocumentation() string {
	return "gib commit heads: 1-sat PushDrop coins with fields [\"gib\", origin, branch, root, identity]"
}

// GetMetaData returns metadata for the topic.
func (tm *TopicManager) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{
		Name:        TopicName,
		Description: "gib on-chain git branch pointers (commit heads)",
		Version:     ProtocolVersion,
	}
}

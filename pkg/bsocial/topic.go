package bsocial

import (
	"context"
	"slices"

	"github.com/bitcoin-sv/go-templates/template/bitcom"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// TopicManager implements the overlay TopicManager interface for BSocial protocol outputs.
type TopicManager struct{}

var mapTypes = []string{"post", "message", "like", "unlike", "follow", "unfollow", "friend", "unfriend", "repost", "tags"}

// IdentifyAdmissibleOutputs determines which transaction outputs contain valid BSocial protocol data.
func (tm *TopicManager) IdentifyAdmissibleOutputs(ctx context.Context, beef *transaction.Beef, txid *chainhash.Hash, previousCoins []uint32) (admit overlay.AdmittanceInstructions, err error) {
	tx := beef.FindTransactionForSigningByHash(txid)
	if tx == nil {
		return admit, engine.ErrInvalidBeef
	}

	for vout, output := range tx.Outputs {
		if bc := bitcom.Decode(output.LockingScript); bc != nil {
			for _, proto := range bc.Protocols {
				if proto.Protocol == bitcom.MapPrefix {
					if m := bitcom.DecodeMap(proto.Script); m == nil {
						continue
					} else if t, ok := m.Data["type"]; ok && slices.Contains(mapTypes, t) {
						admit.OutputsToAdmit = append(admit.OutputsToAdmit, uint32(vout))
						break
					}
				}
			}
		}
	}

	return
}

// IdentifyNeededInputs returns the list of inputs needed for validation.
// BSocial does not require any additional inputs.
func (tm *TopicManager) IdentifyNeededInputs(ctx context.Context, beef *transaction.Beef, txid *chainhash.Hash) ([]*transaction.Outpoint, error) {
	return nil, nil
}

// GetDocumentation returns documentation for this topic manager.
func (tm *TopicManager) GetDocumentation() string {
	return "BSocial Topic Manager"
}

// GetMetaData returns metadata for this topic manager.
func (tm *TopicManager) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{
		Name: "bsocial",
	}
}

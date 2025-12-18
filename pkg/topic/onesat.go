package topic

import (
	"context"

	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// OneSatTopicManager implements a catch-all topic manager that admits all outputs.
// This is the primary topic manager for 1Sat ordinals indexing.
type OneSatTopicManager struct{}

// NewOneSatTopicManager creates a new 1Sat topic manager
func NewOneSatTopicManager() *OneSatTopicManager {
	return &OneSatTopicManager{}
}

// IdentifyAdmissibleOutputs admits ALL outputs from the transaction.
// As a catch-all topic, every output is potentially interesting for 1Sat indexing.
func (tm *OneSatTopicManager) IdentifyAdmissibleOutputs(ctx context.Context, beefBytes []byte, previousCoins []uint32) (admit overlay.AdmittanceInstructions, err error) {
	_, tx, _, err := transaction.ParseBeef(beefBytes)
	if err != nil {
		return admit, err
	} else if tx == nil {
		return admit, engine.ErrInvalidBeef
	}

	// Admit all outputs
	admit.OutputsToAdmit = make([]uint32, len(tx.Outputs))
	for i := range tx.Outputs {
		admit.OutputsToAdmit[i] = uint32(i)
	}

	// Retain all input coins that were previously tracked
	admit.CoinsToRetain = previousCoins

	return admit, nil
}

// IdentifyNeededInputs returns empty list - 1Sat topic doesn't require input resolution
func (tm *OneSatTopicManager) IdentifyNeededInputs(ctx context.Context, beefBytes []byte) ([]*transaction.Outpoint, error) {
	return nil, nil
}

// GetDocumentation returns documentation for this topic manager
func (tm *OneSatTopicManager) GetDocumentation() string {
	return "1Sat Ordinals Topic Manager - admits all outputs for comprehensive indexing"
}

// GetMetaData returns metadata for this topic manager
func (tm *OneSatTopicManager) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{
		Name: "1Sat Ordinals",
	}
}

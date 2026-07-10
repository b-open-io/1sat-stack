package shrug

import (
	"context"
	"log/slog"

	shrugtemplate "github.com/bitcoin-sv/go-templates/template/shrug"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// ShrugDiscoveryTopicManager implements a global topic manager that admits
// all shrug deploy outputs (no token id in the prefix). Registered as the
// discovery topic so new tokens can be found and per-token topics created.
type ShrugDiscoveryTopicManager struct {
	topic  string
	logger *slog.Logger
}

// NewShrugDiscoveryTopicManager creates a new discovery topic manager
func NewShrugDiscoveryTopicManager(topic string, logger *slog.Logger) *ShrugDiscoveryTopicManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &ShrugDiscoveryTopicManager{
		topic:  topic,
		logger: logger,
	}
}

// IdentifyAdmissibleOutputs admits all deploy outputs for token discovery
func (tm *ShrugDiscoveryTopicManager) IdentifyAdmissibleOutputs(ctx context.Context, beef *transaction.Beef, txid *chainhash.Hash, previousCoins []uint32) (admit overlay.AdmittanceInstructions, err error) {
	tx := beef.FindTransactionForSigningByHash(txid)
	if tx == nil {
		return admit, engine.ErrInvalidBeef
	}

	for vout, output := range tx.Outputs {
		if s := shrugtemplate.Decode(output.LockingScript); s != nil && s.Id == nil {
			admit.OutputsToAdmit = append(admit.OutputsToAdmit, uint32(vout))
			tm.logger.Debug("shrug token discovered",
				"topic", tm.topic,
				"txid", txid.String(),
				"vout", vout,
				"amount", s.Amount.String())
		}
	}

	return
}

// IdentifyNeededInputs returns nothing since deploy outputs need no inputs
func (tm *ShrugDiscoveryTopicManager) IdentifyNeededInputs(ctx context.Context, beef *transaction.Beef, txid *chainhash.Hash) ([]*transaction.Outpoint, error) {
	return nil, nil
}

// GetDocumentation returns documentation for this topic manager
func (tm *ShrugDiscoveryTopicManager) GetDocumentation() string {
	return "Shrug Discovery Topic Manager - admits all deploy outputs for token discovery"
}

// GetMetaData returns metadata for this topic manager
func (tm *ShrugDiscoveryTopicManager) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{
		Name: "Shrug Discovery",
	}
}

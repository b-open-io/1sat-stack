package collection

import (
	"context"
	"log/slog"

	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// DiscoveryTopicManager admits collection root mints to tm_1sat_collection.
// Requires MAP subType=collection and a valid SIGMA signature on the output.
// Mint-only: no transfer tracking.
type DiscoveryTopicManager struct {
	logger *slog.Logger
}

// NewDiscoveryTopicManager creates a discovery topic manager.
func NewDiscoveryTopicManager(logger *slog.Logger) *DiscoveryTopicManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &DiscoveryTopicManager{logger: logger.With("component", "collection-discovery")}
}

// IdentifyAdmissibleOutputs admits roots with collection MAP + valid SIGMA.
func (tm *DiscoveryTopicManager) IdentifyAdmissibleOutputs(ctx context.Context, beef *transaction.Beef, txid *chainhash.Hash, previousCoins []uint32) (admit overlay.AdmittanceInstructions, err error) {
	tx := beef.FindTransactionForSigningByHash(txid)
	if tx == nil {
		return admit, engine.ErrInvalidBeef
	}

	for vout, output := range tx.Outputs {
		if output == nil {
			continue
		}
		fields := DecodeMapFields(output.LockingScript)
		if fields == nil || fields.SubType != SubTypeCollection {
			continue
		}
		if FirstValidSigma(tx, vout) == nil {
			tm.logger.Debug("collection root missing valid SIGMA",
				"txid", txid.String(),
				"vout", vout,
			)
			continue
		}
		admit.OutputsToAdmit = append(admit.OutputsToAdmit, uint32(vout))
		tm.logger.Debug("collection root admitted",
			"txid", txid.String(),
			"vout", vout,
			"name", fields.Name,
		)
	}
	return admit, nil
}

// IdentifyNeededInputs returns nil — discovery admission is self-contained.
func (tm *DiscoveryTopicManager) IdentifyNeededInputs(ctx context.Context, beef *transaction.Beef, txid *chainhash.Hash) ([]*transaction.Outpoint, error) {
	return nil, nil
}

// GetDocumentation returns documentation for this topic manager.
func (tm *DiscoveryTopicManager) GetDocumentation() string {
	return "1Sat collection discovery — admits collection root mints (MAP subType=collection + SIGMA)"
}

// GetMetaData returns metadata for this topic manager.
func (tm *DiscoveryTopicManager) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{Name: "1Sat Collection Discovery"}
}

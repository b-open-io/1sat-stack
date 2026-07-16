package collection

import (
	"context"
	"log/slog"

	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// ItemTopicManager admits collection item mints for a single collectionId.
// Requires MAP subType=collectionItem, matching normalized collectionId, and
// a valid SIGMA signature. Does not match signer against collection authority at
// ingest (BAP key rotation can race). Mint-only: no transfer tracking.
type ItemTopicManager struct {
	collectionID string
	logger       *slog.Logger
}

// NewItemTopicManager creates a per-collection item topic manager.
func NewItemTopicManager(collectionID string, logger *slog.Logger) *ItemTopicManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &ItemTopicManager{
		collectionID: collectionID,
		logger: logger.With(
			"component", "collection-item",
			"collectionId", collectionID,
		),
	}
}

// CollectionID returns the collection this manager admits for.
func (tm *ItemTopicManager) CollectionID() string {
	return tm.collectionID
}

// IdentifyAdmissibleOutputs admits matching collectionItem mints with SIGMA.
func (tm *ItemTopicManager) IdentifyAdmissibleOutputs(ctx context.Context, beef *transaction.Beef, txid *chainhash.Hash, previousCoins []uint32) (admit overlay.AdmittanceInstructions, err error) {
	tx := beef.FindTransactionForSigningByHash(txid)
	if tx == nil {
		return admit, engine.ErrInvalidBeef
	}

	for vout, output := range tx.Outputs {
		if output == nil {
			continue
		}
		fields := DecodeMapFields(output.LockingScript)
		if fields == nil || fields.SubType != SubTypeCollectionItem {
			continue
		}
		outpoint := &transaction.Outpoint{Txid: *txid, Index: uint32(vout)}
		collectionID := NormalizeCollectionID(fields.CollectionID, outpoint)
		if collectionID == "" || collectionID != tm.collectionID {
			continue
		}
		if FirstValidSigma(tx, vout) == nil {
			tm.logger.Debug("collection item missing valid SIGMA",
				"txid", txid.String(),
				"vout", vout,
			)
			continue
		}
		admit.OutputsToAdmit = append(admit.OutputsToAdmit, uint32(vout))
		tm.logger.Debug("collection item admitted",
			"txid", txid.String(),
			"vout", vout,
		)
	}
	return admit, nil
}

// IdentifyNeededInputs returns nil — item mint admission is self-contained.
func (tm *ItemTopicManager) IdentifyNeededInputs(ctx context.Context, beef *transaction.Beef, txid *chainhash.Hash) ([]*transaction.Outpoint, error) {
	return nil, nil
}

// GetDocumentation returns documentation for this topic manager.
func (tm *ItemTopicManager) GetDocumentation() string {
	return "1Sat collection items — admits collectionItem mints for a collectionId (MAP + SIGMA)"
}

// GetMetaData returns metadata for this topic manager.
func (tm *ItemTopicManager) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{Name: "1Sat Collection Items"}
}

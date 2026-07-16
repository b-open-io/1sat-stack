package collection

import (
	"context"
	"log/slog"

	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// MemberTopicManager admits collection item mints for a single collectionId.
// Requires MAP subType=collectionItem, matching normalized collectionId, and
// a valid SIGMA signature. Does not match signer against root authority at
// ingest (BAP key rotation can race). Mint-only: no transfer tracking.
type MemberTopicManager struct {
	collectionID string
	logger       *slog.Logger
}

// NewMemberTopicManager creates a per-collection member topic manager.
func NewMemberTopicManager(collectionID string, logger *slog.Logger) *MemberTopicManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &MemberTopicManager{
		collectionID: collectionID,
		logger: logger.With(
			"component", "collection-member",
			"collectionId", collectionID,
		),
	}
}

// CollectionID returns the collection this manager admits for.
func (tm *MemberTopicManager) CollectionID() string {
	return tm.collectionID
}

// IdentifyAdmissibleOutputs admits matching collectionItem mints with SIGMA.
func (tm *MemberTopicManager) IdentifyAdmissibleOutputs(ctx context.Context, beef *transaction.Beef, txid *chainhash.Hash, previousCoins []uint32) (admit overlay.AdmittanceInstructions, err error) {
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

// IdentifyNeededInputs returns nil — member mint admission is self-contained.
func (tm *MemberTopicManager) IdentifyNeededInputs(ctx context.Context, beef *transaction.Beef, txid *chainhash.Hash) ([]*transaction.Outpoint, error) {
	return nil, nil
}

// GetDocumentation returns documentation for this topic manager.
func (tm *MemberTopicManager) GetDocumentation() string {
	return "1Sat collection members — admits collectionItem mints for a collectionId (MAP + SIGMA)"
}

// GetMetaData returns metadata for this topic manager.
func (tm *MemberTopicManager) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{Name: "1Sat Collection Members"}
}

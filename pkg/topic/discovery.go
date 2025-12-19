package topic

import (
	"context"
	"log/slog"

	"github.com/bitcoin-sv/go-templates/template/bsv21"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// Bsv21DiscoveryTopicManager implements a global topic manager that admits ALL deploy+mint operations.
// This is used for token discovery - registered as topic "tm_bsv21".
// When a new mint is discovered, it can trigger fee service notification.
type Bsv21DiscoveryTopicManager struct {
	topic   string
	storage engine.Storage
	logger  *slog.Logger
}

// NewBsv21DiscoveryTopicManager creates a new discovery topic manager
func NewBsv21DiscoveryTopicManager(topic string, storage engine.Storage, logger *slog.Logger) *Bsv21DiscoveryTopicManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Bsv21DiscoveryTopicManager{
		topic:   topic,
		storage: storage,
		logger:  logger,
	}
}

// IdentifyAdmissibleOutputs admits all deploy+mint outputs for token discovery
func (tm *Bsv21DiscoveryTopicManager) IdentifyAdmissibleOutputs(ctx context.Context, beefBytes []byte, previousCoins []uint32) (admit overlay.AdmittanceInstructions, err error) {
	_, tx, txid, err := transaction.ParseBeef(beefBytes)
	if err != nil {
		return admit, err
	} else if tx == nil {
		return admit, engine.ErrInvalidBeef
	}

	// Only admit deploy+mint operations
	for vout, output := range tx.Outputs {
		if b := bsv21.Decode(output.LockingScript); b != nil {
			if b.Op == string(bsv21.OpMint) {
				// This is a deploy+mint - admit it for discovery
				admit.OutputsToAdmit = append(admit.OutputsToAdmit, uint32(vout))
				tm.logger.Debug("token discovered",
					"topic", tm.topic,
					"txid", txid.String(),
					"vout", vout)
			}
		}
	}

	return
}

// IdentifyNeededInputs returns empty list since deploy+mint needs no inputs
func (tm *Bsv21DiscoveryTopicManager) IdentifyNeededInputs(ctx context.Context, beefBytes []byte) ([]*transaction.Outpoint, error) {
	return nil, nil
}

// GetDocumentation returns documentation for this topic manager
func (tm *Bsv21DiscoveryTopicManager) GetDocumentation() string {
	return "BSV21 Discovery Topic Manager - admits all deploy+mint operations for token discovery"
}

// GetMetaData returns metadata for this topic manager
func (tm *Bsv21DiscoveryTopicManager) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{
		Name: "BSV21 Discovery",
	}
}

package ordlock

import (
	"context"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/template/ordlock"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// TopicNameV2 is the overlay topic for OrdLock v2 (batch, tag-output) listings.
// Listing storage is scoped to the v2 topic.
const TopicNameV2 = "tm_ordlock_v2"

type TopicManagerV2 struct{}

func (tm *TopicManagerV2) IdentifyAdmissibleOutputs(ctx context.Context, beef *transaction.Beef, txid *chainhash.Hash, previousCoins []uint32) (admit overlay.AdmittanceInstructions, err error) {
	tx := beef.FindTransactionForSigningByHash(txid)
	if tx == nil {
		return admit, engine.ErrInvalidBeef
	}
	for vout, output := range tx.Outputs {
		if output.Satoshis != 1 {
			continue
		}
		if ordlock.IsOrdLockV2(output.LockingScript) {
			admit.OutputsToAdmit = append(admit.OutputsToAdmit, uint32(vout))
		}
	}
	for vin, input := range tx.Inputs {
		src := input.SourceTxOutput()
		if src == nil || src.Satoshis != 1 {
			continue
		}
		if ordlock.IsOrdLockV2(src.LockingScript) {
			admit.CoinsToRetain = append(admit.CoinsToRetain, uint32(vin))
		}
	}
	if len(admit.OutputsToAdmit) > 0 || len(admit.CoinsToRetain) > 0 {
		slog.Info("ordlock v2 admission", "txid", txid.String(), "outputs", admit.OutputsToAdmit, "retained", admit.CoinsToRetain)
	}
	return
}

func (tm *TopicManagerV2) IdentifyNeededInputs(ctx context.Context, beef *transaction.Beef, txid *chainhash.Hash) ([]*transaction.Outpoint, error) {
	tx := beef.FindTransactionForSigningByHash(txid)
	if tx == nil {
		return nil, engine.ErrInvalidBeef
	}
	var inputs []*transaction.Outpoint
	for _, input := range tx.Inputs {
		src := input.SourceTxOutput()
		if src == nil || src.Satoshis != 1 {
			continue
		}
		if ordlock.IsOrdLockV2(src.LockingScript) {
			inputs = append(inputs, &transaction.Outpoint{Txid: *input.SourceTXID, Index: input.SourceTxOutIndex})
		}
	}
	return inputs, nil
}

func (tm *TopicManagerV2) GetDocumentation() string { return "OrdLock v2 (batch) Topic Manager" }
func (tm *TopicManagerV2) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{Name: "ordlock2"}
}

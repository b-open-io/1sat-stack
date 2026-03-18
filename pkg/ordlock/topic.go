package ordlock

import (
	"context"
	"log/slog"

	"github.com/bitcoin-sv/go-templates/template/ordlock"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

type TopicManager struct{}

func (tm *TopicManager) IdentifyAdmissibleOutputs(ctx context.Context, beef *transaction.Beef, txid *chainhash.Hash, previousCoins []uint32) (admit overlay.AdmittanceInstructions, err error) {
	tx := beef.FindTransactionForSigningByHash(txid)
	if tx == nil {
		return admit, engine.ErrInvalidBeef
	}

	for vout, output := range tx.Outputs {
		if output.Satoshis != 1 {
			continue
		}
		if ordlock.Decode(output.LockingScript) != nil {
			admit.OutputsToAdmit = append(admit.OutputsToAdmit, uint32(vout))
		}
	}

	for vin, input := range tx.Inputs {
		src := input.SourceTxOutput()
		if src == nil || src.Satoshis != 1 {
			continue
		}
		if ordlock.Decode(src.LockingScript) != nil {
			admit.CoinsToRetain = append(admit.CoinsToRetain, uint32(vin))
		}
	}

	if len(admit.OutputsToAdmit) > 0 || len(admit.CoinsToRetain) > 0 {
		slog.Info("ordlock admission",
			"txid", txid.String(),
			"outputs", admit.OutputsToAdmit,
			"retained", admit.CoinsToRetain,
			"total_outputs", len(tx.Outputs),
		)
	}

	return
}

func (tm *TopicManager) IdentifyNeededInputs(ctx context.Context, beef *transaction.Beef, txid *chainhash.Hash) ([]*transaction.Outpoint, error) {
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
		if ordlock.Decode(src.LockingScript) != nil {
			inputs = append(inputs, &transaction.Outpoint{
				Txid:  *input.SourceTXID,
				Index: input.SourceTxOutIndex,
			})
		}
	}
	return inputs, nil
}

func (tm *TopicManager) GetDocumentation() string {
	return "OrdLock Topic Manager"
}

func (tm *TopicManager) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{Name: "ordlock"}
}

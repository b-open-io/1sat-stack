package ordlock

import (
	"context"

	"github.com/bitcoin-sv/go-templates/template/ordlock"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/script"
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
		if ordlock.Decode(output.LockingScript) == nil {
			continue
		}
		admit.OutputsToAdmit = append(admit.OutputsToAdmit, uint32(vout))
	}

	// Retain inputs that spend previous ordlock outputs so the engine tracks spends
	for vin, input := range tx.Inputs {
		if !isOrdlockInput(input) {
			continue
		}
		admit.CoinsToRetain = append(admit.CoinsToRetain, uint32(vin))
	}

	return
}

func (tm *TopicManager) IdentifyNeededInputs(ctx context.Context, beef *transaction.Beef, txid *chainhash.Hash) ([]*transaction.Outpoint, error) {
	tx := beef.FindTransactionForSigningByHash(txid)
	if tx == nil {
		return nil, engine.ErrInvalidBeef
	}

	var needed []*transaction.Outpoint
	for _, input := range tx.Inputs {
		if input.SourceTransaction != nil {
			continue
		}
		if input.SourceTXID == nil {
			continue
		}
		needed = append(needed, &transaction.Outpoint{
			Txid:  *input.SourceTXID,
			Index: input.SourceTxOutIndex,
		})
	}
	return needed, nil
}

func (tm *TopicManager) GetDocumentation() string {
	return "OrdLock Topic Manager"
}

func (tm *TopicManager) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{Name: "ordlock"}
}

func isOrdlockInput(input *transaction.TransactionInput) bool {
	src := input.SourceTxOutput()
	if src == nil || src.Satoshis != 1 {
		return false
	}
	return ordlock.Decode(script.NewFromBytes(src.LockingScript.Bytes())) != nil
}

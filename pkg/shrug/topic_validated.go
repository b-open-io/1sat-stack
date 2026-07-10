package shrug

import (
	"context"
	"log/slog"
	"math/big"
	"slices"

	overlayerr "github.com/b-open-io/1sat-stack/pkg/overlay"
	shrugtemplate "github.com/bitcoin-sv/go-templates/template/shrug"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// ShrugValidatedTopicManager implements the overlay TopicManager interface
// for shrug tokens. Shrug has no operation field: an output with amount 0 is
// a minting authority, an output with amount > 0 is token value, and an
// output with no token id is a deploy. Validation is contextual — spending a
// valid authority admits value outputs without balance coverage (minting);
// otherwise input/output conservation applies.
type ShrugValidatedTopicManager struct {
	topic    string
	tokenIds map[string]struct{}
	metadata *overlay.MetaData
}

// NewShrugValidatedTopicManager creates a new shrug validated topic manager
func NewShrugValidatedTopicManager(topic string, tokenIds []string, metadata *overlay.MetaData) *ShrugValidatedTopicManager {
	tm := &ShrugValidatedTopicManager{
		topic:    topic,
		metadata: metadata,
	}
	if len(tokenIds) > 0 {
		tm.tokenIds = make(map[string]struct{}, len(tokenIds))
		for _, tokenId := range tokenIds {
			tm.tokenIds[tokenId] = struct{}{}
		}
	}
	if tm.metadata == nil {
		tm.metadata = &overlay.MetaData{Name: "Shrug"}
	}
	return tm
}

// HasTokenId returns true if the tokenId is managed by this topic
func (tm *ShrugValidatedTopicManager) HasTokenId(tokenId string) bool {
	if tm.tokenIds == nil {
		return true // Accept all tokens if no whitelist
	}
	_, ok := tm.tokenIds[tokenId]
	return ok
}

// tokenSummary tracks per-tokenId state while classifying a tx's outputs and
// inputs for admittance. Amounts are unbounded script numbers, so all
// accumulation uses big.Int.
type tokenSummary struct {
	// Token value entering the tx from valid value inputs (amount > 0,
	// including deploy outputs that carry supply). Authority inputs
	// contribute nothing.
	tokensIn *big.Int
	// Sum of amounts across this tx's value outputs.
	valueOut *big.Int
	// Output indices grouped by kind so each group admits on its own rule.
	valueVouts []uint32
	authVouts  []uint32
	// True when at least one input is a valid authority (amount 0, including
	// a deploy with amount 0) for this tokenId. Confers unlimited mint
	// authority for the tx's value and authority outputs.
	hasAuthInput bool
}

// IdentifyAdmissibleOutputs determines which outputs should be admitted to
// the topic:
//
//   - Deploy outputs (no token id): admitted unconditionally.
//   - Authority outputs (amount 0): admitted when a valid authority input
//     for that tokenId is spent.
//   - Value outputs (amount > 0): admitted without balance coverage when a
//     valid authority input is spent (minting); otherwise admitted when
//     value inputs cover the total value output for that tokenId,
//     all-or-nothing per token.
//
// Excess input value is burned implicitly — there is nothing to admit for
// it. Shrug has no explicit burn outputs.
func (tm *ShrugValidatedTopicManager) IdentifyAdmissibleOutputs(ctx context.Context, beef *transaction.Beef, txid *chainhash.Hash, previousCoins []uint32) (admit overlay.AdmittanceInstructions, err error) {
	tx := beef.FindTransactionForSigningByHash(txid)
	if tx == nil {
		return admit, engine.ErrInvalidBeef
	}

	summary := make(map[string]*tokenSummary)
	getSummary := func(id string) *tokenSummary {
		ts, ok := summary[id]
		if !ok {
			ts = &tokenSummary{
				tokensIn: new(big.Int),
				valueOut: new(big.Int),
			}
			summary[id] = ts
		}
		return ts
	}

	// First pass: classify outputs.
	for vout, output := range tx.Outputs {
		s := shrugtemplate.Decode(output.LockingScript)
		if s == nil {
			continue
		}
		if s.Id == nil {
			// Deploy: the token id is this output's own outpoint.
			tokenId := (&transaction.Outpoint{
				Txid:  *txid,
				Index: uint32(vout),
			}).OrdinalString()
			if !tm.HasTokenId(tokenId) {
				continue
			}
			admit.OutputsToAdmit = append(admit.OutputsToAdmit, uint32(vout))
			continue
		}
		tokenId := s.Id.OrdinalString()
		if !tm.HasTokenId(tokenId) {
			continue
		}
		ts := getSummary(tokenId)
		if s.Amount.Sign() == 0 {
			ts.authVouts = append(ts.authVouts, uint32(vout))
		} else {
			ts.valueOut.Add(ts.valueOut, s.Amount)
			ts.valueVouts = append(ts.valueVouts, uint32(vout))
		}
	}

	if len(summary) == 0 {
		// Only deploy outputs (or no relevant outputs) — nothing depends on
		// input classification.
		return admit, nil
	}

	ancillaryTxids := make(map[chainhash.Hash]struct{}, len(tx.Inputs))

	// Second pass: classify inputs.
	for vin, txin := range tx.Inputs {
		sourceOutput := txin.SourceTxOutput()
		if sourceOutput == nil {
			continue
		}
		s := shrugtemplate.Decode(sourceOutput.LockingScript)
		if s == nil {
			continue
		}
		var tokenId string
		if s.Id == nil {
			tokenId = (&transaction.Outpoint{
				Txid:  *txin.SourceTXID,
				Index: txin.SourceTxOutIndex,
			}).OrdinalString()
		} else {
			tokenId = s.Id.OrdinalString()
		}
		if !tm.HasTokenId(tokenId) {
			continue
		}
		ts, ok := summary[tokenId]
		if !ok {
			// No outputs for this tokenId. The input is incidental; its
			// tokens are burned implicitly.
			continue
		}
		ancillaryTxids[*txin.SourceTXID] = struct{}{}

		if !slices.Contains(previousCoins, uint32(vin)) {
			return admit, &overlayerr.MissingInputError{
				TransactionID: txid,
				InputIndex:    uint32(vin),
				MissingTxID:   txin.SourceTXID,
				OutputIndex:   txin.SourceTxOutIndex,
				Topic:         tm.topic,
			}
		}
		admit.CoinsToRetain = append(admit.CoinsToRetain, uint32(vin))

		if s.Amount.Sign() == 0 {
			slog.Debug("SHRUG_AUTH_INPUT",
				"topic", tm.topic,
				"txid", txid.String(),
				"vin", vin,
				"source_txid", txin.SourceTXID.String())
			ts.hasAuthInput = true
		} else {
			slog.Debug("SHRUG_VALUE_INPUT",
				"topic", tm.topic,
				"txid", txid.String(),
				"vin", vin,
				"source_txid", txin.SourceTXID.String(),
				"amount", s.Amount.String())
			ts.tokensIn.Add(ts.tokensIn, s.Amount)
		}
	}

	// Per-tokenId admission decisions.
	for _, ts := range summary {
		if ts.hasAuthInput {
			// Authority present: value outputs mint freely, authority
			// outputs continue the authority line.
			admit.OutputsToAdmit = append(admit.OutputsToAdmit, ts.valueVouts...)
			admit.OutputsToAdmit = append(admit.OutputsToAdmit, ts.authVouts...)
		} else if ts.tokensIn.Cmp(ts.valueOut) >= 0 {
			// Conservation: value inputs cover value outputs.
			admit.OutputsToAdmit = append(admit.OutputsToAdmit, ts.valueVouts...)
		}
	}

	if len(ancillaryTxids) > 0 {
		admit.AncillaryTxids = make([]*chainhash.Hash, 0, len(ancillaryTxids))
		for txidHash := range ancillaryTxids {
			hash := txidHash
			admit.AncillaryTxids = append(admit.AncillaryTxids, &hash)
		}
	}

	return admit, nil
}

// IdentifyNeededInputs returns the inputs needed for processing
func (tm *ShrugValidatedTopicManager) IdentifyNeededInputs(ctx context.Context, beef *transaction.Beef, txid *chainhash.Hash) ([]*transaction.Outpoint, error) {
	tx := beef.FindTransactionForSigningByHash(txid)
	if tx == nil {
		return nil, engine.ErrInvalidBeef
	}

	needsInputs := false
	for _, output := range tx.Outputs {
		if s := shrugtemplate.Decode(output.LockingScript); s != nil && s.Id != nil {
			if tm.HasTokenId(s.Id.OrdinalString()) {
				needsInputs = true
				break
			}
		}
	}

	if !needsInputs {
		return nil, nil
	}

	var inputs []*transaction.Outpoint
	for _, txin := range tx.Inputs {
		if txin.SourceTransaction == nil {
			inputs = append(inputs, &transaction.Outpoint{
				Txid:  *txin.SourceTXID,
				Index: txin.SourceTxOutIndex,
			})
		}
	}
	return inputs, nil
}

// GetDocumentation returns documentation for this topic manager
func (tm *ShrugValidatedTopicManager) GetDocumentation() string {
	return "Shrug Validated Topic Manager"
}

// GetMetaData returns metadata for this topic manager
func (tm *ShrugValidatedTopicManager) GetMetaData() *overlay.MetaData {
	return tm.metadata
}

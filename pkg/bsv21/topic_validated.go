package bsv21

import (
	"context"
	"log/slog"
	"slices"

	overlayerr "github.com/b-open-io/1sat-stack/pkg/overlay"
	bsv21template "github.com/b-open-io/1sat-stack/pkg/template/bsv21"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// Bsv21ValidatedTopicManager implements the overlay TopicManager interface for BSV21.
// It validates token transfers by checking input/output balances.
// This matches the implementation in bsv21-overlay/topics/bsv21-topic-validated.go
type Bsv21ValidatedTopicManager struct {
	topic    string
	tokenIds map[string]struct{}
	metadata *overlay.MetaData
}

// NewBsv21ValidatedTopicManager creates a new BSV21 validated topic manager
func NewBsv21ValidatedTopicManager(topic string, tokenIds []string, metadata *overlay.MetaData) *Bsv21ValidatedTopicManager {
	tm := &Bsv21ValidatedTopicManager{
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
		tm.metadata = &overlay.MetaData{Name: "BSV21"}
	}
	return tm
}

// HasTokenId returns true if the tokenId is managed by this topic
func (tm *Bsv21ValidatedTopicManager) HasTokenId(tokenId string) bool {
	if tm.tokenIds == nil {
		return true // Accept all tokens if no whitelist
	}
	_, ok := tm.tokenIds[tokenId]
	return ok
}

// tokenSummary tracks per-tokenId state while classifying a tx's outputs and
// inputs for admittance. The fields are populated per BSV21 op so transfer,
// burn, mint and auth admission rules can be applied independently.
type tokenSummary struct {
	// Spendable token amount entering the tx from inputs (transfer, mint, or
	// deploy+mint outputs being spent). Auth and burn inputs do not contribute.
	tokensIn uint64
	// Sum of Amt across this tx's transfer outputs.
	transferOut uint64
	// Sum of Amt across this tx's burn outputs.
	burnOut uint64
	// Output indices grouped by op so each group can be admitted on its own
	// rule.
	transferVouts []uint32
	burnVouts     []uint32
	mintVouts     []uint32
	authVouts     []uint32
	// True when at least one input is an auth UTXO (Op=auth or
	// Op=deploy+auth) for this tokenId. Confers unlimited mint authority for
	// the tx's mint and auth outputs.
	hasAuthInput bool
}

// IdentifyAdmissibleOutputs determines which outputs should be admitted to
// the topic. Admission rules are split per BSV21 op:
//
//   - Deploy outputs (deploy+mint, deploy+auth): admitted unconditionally.
//   - Transfer / burn outputs: admitted when input token balance covers the
//     combined transfer + burn output amount for that tokenId.
//   - Mint / auth outputs: admitted when an auth input for that tokenId is
//     spent (auth confers unlimited mint authority).
//
// Burn outputs are admitted (so circulating supply can be computed from
// mints minus burns) but their amount is consumed against `tokensIn` so a
// caller cannot burn tokens it does not hold.
//
// Auth inputs are required to be in `previousCoins`; the engine relies on
// this to enforce the dependency chain. Burn inputs are ignored entirely —
// a burn output may legitimately be spent later for satoshi recovery, with
// no contribution to topic state.
func (tm *Bsv21ValidatedTopicManager) IdentifyAdmissibleOutputs(ctx context.Context, beef *transaction.Beef, txid *chainhash.Hash, previousCoins []uint32) (admit overlay.AdmittanceInstructions, err error) {
	tx := beef.FindTransactionForSigningByHash(txid)
	if tx == nil {
		return admit, engine.ErrInvalidBeef
	}

	summary := make(map[string]*tokenSummary)
	getSummary := func(id string) *tokenSummary {
		ts, ok := summary[id]
		if !ok {
			ts = &tokenSummary{}
			summary[id] = ts
		}
		return ts
	}

	// First pass: classify outputs by op.
	for vout, output := range tx.Outputs {
		b := bsv21template.Decode(output.LockingScript)
		if b == nil {
			continue
		}
		// For deploy operations, tokenId = outpoint of the deploy itself.
		if b.Op == string(bsv21template.OpDeployMint) || b.Op == string(bsv21template.OpDeployAuth) {
			b.Id = (&transaction.Outpoint{
				Txid:  *txid,
				Index: uint32(vout),
			}).OrdinalString()
		}
		if !tm.HasTokenId(b.Id) {
			continue
		}
		switch b.Op {
		case string(bsv21template.OpDeployMint), string(bsv21template.OpDeployAuth):
			admit.OutputsToAdmit = append(admit.OutputsToAdmit, uint32(vout))
		case string(bsv21template.OpTransfer):
			ts := getSummary(b.Id)
			ts.transferOut += b.Amt
			ts.transferVouts = append(ts.transferVouts, uint32(vout))
		case string(bsv21template.OpBurn):
			ts := getSummary(b.Id)
			ts.burnOut += b.Amt
			ts.burnVouts = append(ts.burnVouts, uint32(vout))
		case string(bsv21template.OpMint):
			ts := getSummary(b.Id)
			ts.mintVouts = append(ts.mintVouts, uint32(vout))
		case string(bsv21template.OpAuth):
			ts := getSummary(b.Id)
			ts.authVouts = append(ts.authVouts, uint32(vout))
		}
	}

	if len(summary) == 0 {
		// Only deploy outputs (or no relevant outputs) — nothing depends on
		// input classification.
		return admit, nil
	}

	ancillaryTxids := make(map[chainhash.Hash]struct{}, len(tx.Inputs))

	// Second pass: classify inputs by op.
	for vin, txin := range tx.Inputs {
		sourceOutput := txin.SourceTxOutput()
		if sourceOutput == nil {
			continue
		}
		b := bsv21template.Decode(sourceOutput.LockingScript)
		if b == nil {
			continue
		}
		if b.Op == string(bsv21template.OpDeployMint) || b.Op == string(bsv21template.OpDeployAuth) {
			b.Id = (&transaction.Outpoint{
				Txid:  *txin.SourceTXID,
				Index: txin.SourceTxOutIndex,
			}).OrdinalString()
		}
		if !tm.HasTokenId(b.Id) {
			continue
		}
		ts, ok := summary[b.Id]
		if !ok {
			// No outputs for this tokenId. The input is incidental; nothing to
			// account for. Don't record an ancillary txid either.
			continue
		}
		ancillaryTxids[*txin.SourceTXID] = struct{}{}

		switch b.Op {
		case string(bsv21template.OpBurn):
			// Burn outputs being spent (e.g. to recoup the satoshi). They
			// contribute nothing to balance and aren't required to be in
			// previousCoins.
			continue
		case string(bsv21template.OpAuth), string(bsv21template.OpDeployAuth):
			if !slices.Contains(previousCoins, uint32(vin)) {
				return admit, &overlayerr.MissingInputError{
					TransactionID: txid,
					InputIndex:    uint32(vin),
					MissingTxID:   txin.SourceTXID,
					OutputIndex:   txin.SourceTxOutIndex,
					Topic:         tm.topic,
				}
			}
			slog.Debug("BSV21_AUTH_INPUT",
				"topic", tm.topic,
				"txid", txid.String(),
				"vin", vin,
				"source_txid", txin.SourceTXID.String())
			admit.CoinsToRetain = append(admit.CoinsToRetain, uint32(vin))
			ts.hasAuthInput = true
		case string(bsv21template.OpTransfer), string(bsv21template.OpMint), string(bsv21template.OpDeployMint):
			if !slices.Contains(previousCoins, uint32(vin)) {
				return admit, &overlayerr.MissingInputError{
					TransactionID: txid,
					InputIndex:    uint32(vin),
					MissingTxID:   txin.SourceTXID,
					OutputIndex:   txin.SourceTxOutIndex,
					Topic:         tm.topic,
				}
			}
			slog.Debug("BSV21_TOKEN_INPUT",
				"topic", tm.topic,
				"txid", txid.String(),
				"vin", vin,
				"source_txid", txin.SourceTXID.String(),
				"amt", b.Amt)
			admit.CoinsToRetain = append(admit.CoinsToRetain, uint32(vin))
			ts.tokensIn += b.Amt
		}
	}

	// Per-tokenId admission decisions. Transfer/burn admit on balance;
	// mint/auth admit on auth-input presence. The two layers are independent:
	// a tx with insufficient balance can still admit valid mint outputs.
	for _, ts := range summary {
		if ts.tokensIn >= ts.transferOut+ts.burnOut {
			admit.OutputsToAdmit = append(admit.OutputsToAdmit, ts.transferVouts...)
			admit.OutputsToAdmit = append(admit.OutputsToAdmit, ts.burnVouts...)
		}
		if ts.hasAuthInput {
			admit.OutputsToAdmit = append(admit.OutputsToAdmit, ts.mintVouts...)
			admit.OutputsToAdmit = append(admit.OutputsToAdmit, ts.authVouts...)
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
func (tm *Bsv21ValidatedTopicManager) IdentifyNeededInputs(ctx context.Context, beef *transaction.Beef, txid *chainhash.Hash) ([]*transaction.Outpoint, error) {
	tx := beef.FindTransactionForSigningByHash(txid)
	if tx == nil {
		return nil, engine.ErrInvalidBeef
	}

	tokens := make(map[string]struct{})
	for _, output := range tx.Outputs {
		if b := bsv21template.Decode(output.LockingScript); b != nil {
			if !tm.HasTokenId(b.Id) {
				continue
			}
			tokens[b.Id] = struct{}{}
		}
	}

	if len(tokens) == 0 {
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
func (tm *Bsv21ValidatedTopicManager) GetDocumentation() string {
	return "BSV21 Validated Topic Manager"
}

// GetMetaData returns metadata for this topic manager
func (tm *Bsv21ValidatedTopicManager) GetMetaData() *overlay.MetaData {
	return tm.metadata
}

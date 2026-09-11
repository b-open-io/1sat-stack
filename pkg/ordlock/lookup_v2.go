package ordlock

import (
	"context"

	"github.com/b-open-io/1sat-stack/pkg/ordfs"
	"github.com/b-open-io/1sat-stack/pkg/template/ordlock"
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/overlay/lookup"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// LookupServiceV2 indexes OrdLock v2 (batch) listings into the shared listings
// store (scoped to the v2 topic_id). Identical to v1 except the recognizer:
// DecodeV2 instead of Decode. Spend classification (sale/cancel by last unlock
// byte) and all storage are reused.
type LookupServiceV2 struct {
	ol *OrdLock
}

func NewLookupServiceV2(ol *OrdLock) *LookupServiceV2 {
	return &LookupServiceV2{ol: ol}
}

func (l *LookupServiceV2) OutputAdmittedByTopic(ctx context.Context, payload *engine.OutputAdmittedByTopic) error {
	_, tx, txid, err := transaction.ParseBeef(payload.AtomicBEEF)
	if err != nil {
		return err
	}
	output := tx.Outputs[payload.OutputIndex]
	outpoint := &transaction.Outpoint{Txid: *txid, Index: payload.OutputIndex}

	ld := l.extractListingData(ctx, outpoint, output.LockingScript)
	if ld == nil {
		return nil
	}
	return l.ol.UpsertListing(ctx, outpoint, ld, types.ScoreFromTx(tx, txid))
}

func (l *LookupServiceV2) OutputSpent(ctx context.Context, payload *engine.OutputSpent) error {
	spendScore := float64(0)
	if payload.SpendingAtomicBEEF != nil {
		if _, tx, txid, err := transaction.ParseBeef(payload.SpendingAtomicBEEF); err == nil {
			spendScore = types.ScoreFromTx(tx, txid)
		}
	}
	return l.ol.MarkSpent(ctx, payload.Outpoint, payload.SpendingTxid, classifySpend(payload.UnlockingScript), spendScore)
}

func (l *LookupServiceV2) OutputNoLongerRetainedInHistory(ctx context.Context, outpoint *transaction.Outpoint, topic string) error {
	return nil
}

func (l *LookupServiceV2) OutputEvicted(ctx context.Context, outpoint *transaction.Outpoint) error {
	return l.ol.DeleteListing(ctx, outpoint)
}

func (l *LookupServiceV2) OutputBlockHeightUpdated(ctx context.Context, txid *chainhash.Hash, blockHeight uint32, blockIndex uint64) error {
	return nil
}

func (l *LookupServiceV2) Lookup(ctx context.Context, question *lookup.LookupQuestion) (*lookup.LookupAnswer, error) {
	return &lookup.LookupAnswer{Type: lookup.AnswerTypeFreeform}, nil
}

func (l *LookupServiceV2) GetDocumentation() string { return "OrdLock v2 (batch) Lookup Service" }

func (l *LookupServiceV2) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{Name: "ordlock2"}
}

func (l *LookupServiceV2) extractListingData(ctx context.Context, outpoint *transaction.Outpoint, lockingScript *script.Script) *listingData {
	lock := ordlock.DecodeV2(lockingScript)
	if lock == nil || lock.Price > 2_100_000_000_000_000 {
		return nil
	}
	ld := &listingData{price: lock.Price, seller: lock.Seller.AddressString}
	return enrichListingData(ctx, l.ol.ordfs, outpoint, lockingScript, ld)
}

func (l *LookupServiceV2) SetOrdfs(o *ordfs.Ordfs) { l.ol.ordfs = o }

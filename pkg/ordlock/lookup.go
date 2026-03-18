package ordlock

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/b-open-io/1sat-stack/pkg/ordfs"
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bitcoin-sv/go-templates/template/inscription"
	"github.com/bitcoin-sv/go-templates/template/ordlock"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/overlay/lookup"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

type LookupService struct {
	ol *OrdLock
}

func NewLookupService(ol *OrdLock) *LookupService {
	return &LookupService{ol: ol}
}

func (l *LookupService) OutputAdmittedByTopic(ctx context.Context, payload *engine.OutputAdmittedByTopic) error {
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

func (l *LookupService) OutputSpent(ctx context.Context, payload *engine.OutputSpent) error {
	spendScore := float64(0)
	if payload.SpendingAtomicBEEF != nil {
		if _, tx, txid, err := transaction.ParseBeef(payload.SpendingAtomicBEEF); err == nil {
			spendScore = types.ScoreFromTx(tx, txid)
		}
	}

	return l.ol.MarkSpent(ctx, payload.Outpoint, payload.SpendingTxid, classifySpend(payload.UnlockingScript), spendScore)
}

func (l *LookupService) OutputNoLongerRetainedInHistory(ctx context.Context, outpoint *transaction.Outpoint, topic string) error {
	return nil
}

func (l *LookupService) OutputEvicted(ctx context.Context, outpoint *transaction.Outpoint) error {
	return l.ol.DeleteListing(ctx, outpoint)
}

func (l *LookupService) OutputBlockHeightUpdated(ctx context.Context, txid *chainhash.Hash, blockHeight uint32, blockIndex uint64) error {
	return nil
}

func (l *LookupService) Lookup(ctx context.Context, question *lookup.LookupQuestion) (*lookup.LookupAnswer, error) {
	return &lookup.LookupAnswer{Type: lookup.AnswerTypeFreeform}, nil
}

func (l *LookupService) GetDocumentation() string {
	return "OrdLock Lookup Service"
}

func (l *LookupService) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{Name: "ordlock"}
}

func (l *LookupService) extractListingData(ctx context.Context, outpoint *transaction.Outpoint, lockingScript *script.Script) *listingData {
	lock := ordlock.Decode(lockingScript)
	if lock == nil || lock.Price > 2_100_000_000_000_000 {
		return nil
	}

	ld := &listingData{
		price:  lock.Price,
		seller: lock.Seller.AddressString,
	}

	insc := inscription.Decode(lockingScript)
	if insc != nil {
		ld.contentType = insc.File.Type

		if ld.contentType == "application/bsv-20" {
			return nil
		}

		if ld.contentType == "application/op-ns" {
			ld.name = string(insc.File.Content)
		}

		if insc.Parent != nil {
			ld.origin = insc.Parent
		}

		if ld.name == "" {
			if nameField, ok := insc.Fields["name"]; ok {
				ld.name = string(nameField)
			}
		}
	}

	if insc == nil && l.ol.ordfs != nil {
		seq := 0
		resp, err := l.ol.ordfs.Load(ctx, &ordfs.Request{
			Outpoint: outpoint,
			Seq:      &seq,
			Content:  true,
			Map:      true,
		})
		if err == nil && resp != nil {
			if resp.Origin != nil {
				ld.origin = resp.Origin
			}
			if resp.ContentType != "" {
				ld.contentType = strings.Split(resp.ContentType, ";")[0]
				ld.contentType = strings.TrimSpace(ld.contentType)
			}
			if ld.contentType == "application/bsv-20" {
				return nil
			}
			if ld.contentType == "application/op-ns" && len(resp.Content) > 0 {
				ld.name = string(resp.Content)
			}
			if ld.name == "" && resp.Map != nil {
				var mapData map[string]string
				if err := json.Unmarshal(resp.Map, &mapData); err == nil {
					if n, ok := mapData["name"]; ok && n != "" {
						ld.name = n
					}
				}
			}
		}
	}

	if ld.origin == nil {
		ld.origin = outpoint
	}

	return ld
}

func (l *LookupService) SetOrdfs(o *ordfs.Ordfs) {
	l.ol.ordfs = o
}

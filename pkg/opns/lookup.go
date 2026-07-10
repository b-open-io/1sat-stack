package opns

import (
	"context"
	"fmt"
	"log/slog"

	overlaystorage "github.com/b-open-io/1sat-stack/pkg/overlay/storage"
	"github.com/b-open-io/1sat-stack/pkg/template/inscription"
	"github.com/b-open-io/1sat-stack/pkg/template/opns"
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/overlay/lookup"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

const topicName = "tm_opns"
const maxEventValueLen = 256

// LookupService implements the engine.LookupService interface for OpNS.
type LookupService struct {
	db overlaystorage.TopicStorage
}

// NewLookupService creates a new OpNS lookup service backed by per-topic overlay storage.
func NewLookupService(db overlaystorage.TopicStorage) *LookupService {
	return &LookupService{db: db}
}

// OutputAdmittedByTopic processes a newly admitted OpNS output and indexes events.
func (l *LookupService) OutputAdmittedByTopic(ctx context.Context, payload *engine.OutputAdmittedByTopic) error {
	_, tx, txid, err := transaction.ParseBeef(payload.AtomicBEEF)
	if err != nil {
		return fmt.Errorf("failed to parse BEEF: %w", err)
	}

	if int(payload.OutputIndex) >= len(tx.Outputs) {
		return nil
	}

	outpoint := &transaction.Outpoint{
		Txid:  *txid,
		Index: payload.OutputIndex,
	}

	score := types.ScoreFromTx(tx, txid)
	txOut := tx.Outputs[payload.OutputIndex]

	if o := opns.Decode(txOut.LockingScript); o != nil {
		if err := l.db.SaveEvent(ctx, "mine:"+o.Domain, outpoint, score); err != nil {
			return fmt.Errorf("failed to save mine event for %s: %w", o.Domain, err)
		}
	} else if insc := inscription.Decode(txOut.LockingScript); insc != nil && insc.File.Type == "application/op-ns" && len(insc.File.Content) <= maxEventValueLen {
		if err := l.db.SaveEvent(ctx, "name:"+string(insc.File.Content), outpoint, score); err != nil {
			return fmt.Errorf("failed to save name event for %s: %w", string(insc.File.Content), err)
		}
	}

	slog.Debug("OpNS output indexed",
		"outpoint", outpoint.OrdinalString(),
	)
	return nil
}

// OutputSpent handles spent outputs. Events are append-only; latest entry (highest score) wins.
func (l *LookupService) OutputSpent(ctx context.Context, payload *engine.OutputSpent) error {
	return nil
}

// OutputNoLongerRetainedInHistory is called when historical retention is no longer required.
func (l *LookupService) OutputNoLongerRetainedInHistory(ctx context.Context, outpoint *transaction.Outpoint, topic string) error {
	return nil
}

// OutputEvicted is called when an output is permanently evicted.
func (l *LookupService) OutputEvicted(ctx context.Context, outpoint *transaction.Outpoint) error {
	return nil
}

// OutputBlockHeightUpdated is called when a transaction's block height is updated.
func (l *LookupService) OutputBlockHeightUpdated(ctx context.Context, txid *chainhash.Hash, blockHeight uint32, blockIndex uint64) error {
	return nil
}

// Lookup handles generic lookup queries.
func (l *LookupService) Lookup(ctx context.Context, question *lookup.LookupQuestion) (*lookup.LookupAnswer, error) {
	return &lookup.LookupAnswer{
		Type: lookup.AnswerTypeFormula,
	}, nil
}

// GetDocumentation returns documentation for this lookup service.
func (l *LookupService) GetDocumentation() string {
	return "OpNS Lookup Service"
}

// GetMetaData returns metadata for this lookup service.
func (l *LookupService) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{
		Name: "OpNS",
	}
}

// Origin returns the current outpoint for a registered OpNS domain.
// The highest-scored entry is the most recent (valid) one.
func (l *LookupService) Origin(ctx context.Context, domain string) (*transaction.Outpoint, error) {
	results, err := l.db.FindByEvent(ctx, "name:"+domain, &overlaystorage.QueryOpts{
		Limit:   1,
		Reverse: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query opns name for domain %s: %w", domain, err)
	}
	if len(results) == 0 {
		return nil, nil
	}
	return &results[0].Outpoint, nil
}

// ValidateOrigins checks which of the given outpoints exist in the OPNS overlay.
// Returns a set of outpoint strings (using canonical format) for those that are valid.
func (l *LookupService) ValidateOrigins(ctx context.Context, outpoints []*transaction.Outpoint) (map[string]bool, error) {
	found, err := l.db.FindOutputs(ctx, outpoints)
	if err != nil {
		return nil, fmt.Errorf("failed to validate origins: %w", err)
	}
	valid := make(map[string]bool, len(found))
	for i := range found {
		valid[found[i].Outpoint.String()] = true
	}
	return valid, nil
}

// MineResult represents the mining status of an OpNS domain.
type MineResult struct {
	Outpoint *transaction.Outpoint `json:"outpoint"`
	Domain   string                `json:"domain"`
}

// Mine looks up the mining status of a domain by progressively truncating the name.
// If the exact domain has existing mine outputs, returns nil (domain is taken).
// Otherwise, searches for the longest prefix that has been mined.
func (l *LookupService) Mine(ctx context.Context, domain string) (*MineResult, error) {
	results, err := l.db.FindByEvent(ctx, "mine:"+domain, &overlaystorage.QueryOpts{
		Limit:   1,
		Reverse: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query mine for domain %s: %w", domain, err)
	}
	if len(results) > 0 {
		return nil, nil
	}

	search := domain
	for len(search) > 0 {
		search = search[:len(search)-1]
		results, err = l.db.FindByEvent(ctx, "mine:"+search, &overlaystorage.QueryOpts{
			Limit:   1,
			Reverse: true,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to query mine for prefix %s: %w", search, err)
		}
		if len(results) > 0 {
			return &MineResult{
				Outpoint: &results[0].Outpoint,
				Domain:   search,
			}, nil
		}
	}

	return nil, nil
}

package opns

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bitcoin-sv/go-templates/template/inscription"
	"github.com/bitcoin-sv/go-templates/template/opns"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/overlay/lookup"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// nameKey returns the ZSet key for an OPNS domain origin: tm_opns:name:{name}
func nameKey(name string) []byte {
	return []byte("tm_opns:name:" + name)
}

// mineKey returns the ZSet key for OPNS mine state: tm_opns:mine:{prefix}
func mineKey(prefix string) []byte {
	return []byte("tm_opns:mine:" + prefix)
}

// LookupService implements the engine.LookupService interface for OpNS.
type LookupService struct {
	store store.Store
}

// NewLookupService creates a new OpNS lookup service using the provided store.
func NewLookupService(s store.Store) *LookupService {
	return &LookupService{
		store: s,
	}
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
	opBytes := outpoint.Bytes()
	member := store.ScoredMember{Member: opBytes, Score: score}

	txOut := tx.Outputs[payload.OutputIndex]

	if o := opns.Decode(txOut.LockingScript); o != nil {
		if err := l.store.ZAdd(ctx, mineKey(o.Domain), member); err != nil {
			return fmt.Errorf("failed to add to mine ZSet %s: %w", o.Domain, err)
		}
	} else if insc := inscription.Decode(txOut.LockingScript); insc != nil && insc.File.Type == "application/op-ns" {
		if err := l.store.ZAdd(ctx, nameKey(string(insc.File.Content)), member); err != nil {
			return fmt.Errorf("failed to add to name ZSet %s: %w", string(insc.File.Content), err)
		}
	}

	slog.Debug("OpNS output indexed",
		"outpoint", outpoint.OrdinalString(),
	)
	return nil
}

// OutputSpent handles spent outputs. ZSets are append-only; latest entry (highest score) wins.
func (l *LookupService) OutputSpent(ctx context.Context, payload *engine.OutputSpent) error {
	return nil
}

// OutputNoLongerRetainedInHistory is called when historical retention is no longer required.
func (l *LookupService) OutputNoLongerRetainedInHistory(ctx context.Context, outpoint *transaction.Outpoint, topic string) error {
	return nil
}

// OutputEvicted is called when an output is permanently evicted.
// ZSets are append-only; no cleanup needed.
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
	members, err := l.store.ZRevRange(ctx, nameKey(domain), store.ScoreRange{})
	if err != nil {
		return nil, fmt.Errorf("failed to query opns name for domain %s: %w", domain, err)
	}

	if len(members) == 0 {
		return nil, nil
	}

	outpoint := transaction.NewOutpointFromBytes(members[0].Member)
	if outpoint == nil {
		return nil, fmt.Errorf("failed to decode outpoint for domain %s", domain)
	}

	return outpoint, nil
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
	members, err := l.store.ZRevRange(ctx, mineKey(domain), store.ScoreRange{})
	if err != nil {
		return nil, fmt.Errorf("failed to query mine for domain %s: %w", domain, err)
	}
	if len(members) > 0 {
		return nil, nil
	}

	search := domain
	for len(search) > 0 {
		search = search[:len(search)-1]
		members, err = l.store.ZRevRange(ctx, mineKey(search), store.ScoreRange{})
		if err != nil {
			return nil, fmt.Errorf("failed to query mine for prefix %s: %w", search, err)
		}
		if len(members) > 0 {
			outpoint := transaction.NewOutpointFromBytes(members[0].Member)
			if outpoint == nil {
				return nil, fmt.Errorf("failed to decode outpoint for domain prefix %s", search)
			}
			return &MineResult{
				Outpoint: outpoint,
				Domain:   search,
			}, nil
		}
	}

	return nil, nil
}

package opns

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

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

// Key prefixes for OPNS ZSets and SSets.
const keyPrefix = "opns:ev:"

// outpointEventsKey returns the SSet key for tracking which events an outpoint belongs to.
func outpointEventsKey(outpoint *transaction.Outpoint) []byte {
	return []byte("opns:op:" + outpoint.OrdinalString())
}

// eventKey returns the ZSet key for a specific event.
func eventKey(event string) []byte {
	return []byte(keyPrefix + event)
}

// LookupService implements the engine.LookupService interface for OpNS.
// Uses store.Store ZSets for sorted event indexing and SSets for per-outpoint event tracking.
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
// Events follow the taxonomy: opns:{domain}, mine:{prefix}
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

	txOut := tx.Outputs[payload.OutputIndex]
	outputEvents := make([]string, 0, 2)

	// Decode OpNS contract state (mine event)
	if o := opns.Decode(txOut.LockingScript); o != nil {
		outputEvents = append(outputEvents, "mine:"+o.Domain)
	} else if insc := inscription.Decode(txOut.LockingScript); insc != nil && insc.File.Type == "application/op-ns" {
		// Inscription claiming a domain — record origin (permanent)
		outputEvents = append(outputEvents, "opns:"+string(insc.File.Content))
	}

	if len(outputEvents) == 0 {
		return nil
	}

	// Calculate score from transaction
	score := types.ScoreFromTx(tx, txid)
	opBytes := outpoint.Bytes()
	member := store.ScoredMember{Member: opBytes, Score: score}

	// Store each event in its ZSet and track per-outpoint event membership
	eventMembers := make([][]byte, 0, len(outputEvents))
	for _, evt := range outputEvents {
		if err := l.store.ZAdd(ctx, eventKey(evt), member); err != nil {
			return fmt.Errorf("failed to add to event ZSet %s: %w", evt, err)
		}
		eventMembers = append(eventMembers, []byte(evt))
	}

	// Store per-outpoint events in SSet for later lookup (input event inheritance, spend cleanup)
	if err := l.store.SAdd(ctx, outpointEventsKey(outpoint), eventMembers...); err != nil {
		return fmt.Errorf("failed to save outpoint events: %w", err)
	}

	slog.Debug("OpNS events indexed",
		"outpoint", outpoint.OrdinalString(),
		"events", outputEvents,
	)
	return nil
}

// OutputSpent handles spent outputs. Only removes mine: events (opns: origins are permanent).
func (l *LookupService) OutputSpent(ctx context.Context, payload *engine.OutputSpent) error {
	if payload.Outpoint == nil {
		return nil
	}

	// Look up which events this outpoint belongs to
	opEventsKey := outpointEventsKey(payload.Outpoint)
	events, err := l.store.SMembers(ctx, opEventsKey)
	if err != nil {
		return fmt.Errorf("failed to load outpoint events for spend: %w", err)
	}

	opBytes := payload.Outpoint.Bytes()

	// Only remove mine: events (opns: events are permanent origins)
	for _, evt := range events {
		evtStr := string(evt)
		if strings.HasPrefix(evtStr, "mine:") {
			if err := l.store.ZRem(ctx, eventKey(evtStr), opBytes); err != nil {
				slog.Warn("failed to remove mine event on spend",
					"event", evtStr,
					"outpoint", payload.Outpoint.OrdinalString(),
					"error", err,
				)
			}
		}
	}

	// Clean up the per-outpoint events SSet
	if err := l.store.Del(ctx, opEventsKey); err != nil {
		slog.Warn("failed to delete outpoint events key on spend",
			"outpoint", payload.Outpoint.OrdinalString(),
			"error", err,
		)
	}

	return nil
}

// OutputNoLongerRetainedInHistory is called when historical retention is no longer required.
func (l *LookupService) OutputNoLongerRetainedInHistory(ctx context.Context, outpoint *transaction.Outpoint, topic string) error {
	return nil
}

// OutputEvicted is called when an output is permanently evicted.
// Cleans up all event ZSets and the per-outpoint event SSet, same as OutputSpent.
func (l *LookupService) OutputEvicted(ctx context.Context, outpoint *transaction.Outpoint) error {
	if outpoint == nil {
		return nil
	}

	opEventsKey := outpointEventsKey(outpoint)
	events, err := l.store.SMembers(ctx, opEventsKey)
	if err != nil {
		return fmt.Errorf("failed to load outpoint events for eviction: %w", err)
	}

	opBytes := outpoint.Bytes()

	// Only remove mine: events (opns: events are permanent origins)
	for _, evt := range events {
		evtStr := string(evt)
		if strings.HasPrefix(evtStr, "mine:") {
			if err := l.store.ZRem(ctx, eventKey(evtStr), opBytes); err != nil {
				slog.Warn("failed to remove mine event on eviction",
					"event", evtStr,
					"outpoint", outpoint.OrdinalString(),
					"error", err,
				)
			}
		}
	}

	if err := l.store.Del(ctx, opEventsKey); err != nil {
		slog.Warn("failed to delete outpoint events key on eviction",
			"outpoint", outpoint.OrdinalString(),
			"error", err,
		)
	}

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
// Callers use ORDFS to resolve this outpoint to the full ordinal state.
func (l *LookupService) Origin(ctx context.Context, domain string) (*transaction.Outpoint, error) {
	key := eventKey("opns:" + domain)
	members, err := l.store.ZRange(ctx, key, store.ScoreRange{})
	if err != nil {
		return nil, fmt.Errorf("failed to query opns event for domain %s: %w", domain, err)
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
	// Check if the exact domain already has mine outputs (domain is taken)
	key := eventKey("mine:" + domain)
	members, err := l.store.ZRange(ctx, key, store.ScoreRange{})
	if err != nil {
		return nil, fmt.Errorf("failed to query mine event for domain %s: %w", domain, err)
	}
	if len(members) > 0 {
		// Domain already has mine outputs -- not available
		return nil, nil
	}

	// Progressively truncate domain to find the longest mined prefix
	search := domain
	for len(search) > 0 {
		search = search[:len(search)-1]
		key = eventKey("mine:" + search)
		members, err = l.store.ZRange(ctx, key, store.ScoreRange{})
		if err != nil {
			return nil, fmt.Errorf("failed to query mine event for prefix %s: %w", search, err)
		}
		if len(members) > 1 {
			return nil, fmt.Errorf("multiple outputs found for domain prefix %s", search)
		}
		if len(members) == 1 {
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

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
	"github.com/bitcoin-sv/go-templates/template/ordlock"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/overlay/lookup"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/template/p2pkh"
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
// Events follow the taxonomy: opns:{domain}, mine:{domain}, origin:{outpoint}, p2pkh:{address}, list:{domain}
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
	outputEvents := make([]string, 0, 5)
	var domain string

	// Track ordinal origin: find which input's ordinal maps to this output
	if txOut.Satoshis == 1 {
		satsOut := uint64(0)
		for _, output := range tx.Outputs[:payload.OutputIndex] {
			satsOut += output.Satoshis
		}

		satsIn := uint64(0)
		for _, input := range tx.Inputs {
			sourceOut := input.SourceTxOutput()
			if sourceOut == nil {
				break
			}
			if satsIn < satsOut {
				satsIn += sourceOut.Satoshis
				continue
			} else if satsIn == satsOut && sourceOut.Satoshis == 1 {
				// This input carries the ordinal to our output -- inherit events
				inputOutpoint := &transaction.Outpoint{
					Txid:  *input.SourceTXID,
					Index: input.SourceTxOutIndex,
				}
				inputEvents, err := l.store.SMembers(ctx, outpointEventsKey(inputOutpoint))
				if err != nil {
					return fmt.Errorf("failed to load input events: %w", err)
				}
				for _, evt := range inputEvents {
					evtStr := string(evt)
					if strings.HasPrefix(evtStr, "opns:") {
						domain = strings.TrimPrefix(evtStr, "opns:")
						outputEvents = append(outputEvents, evtStr)
					} else if strings.HasPrefix(evtStr, "origin:") {
						outputEvents = append(outputEvents, evtStr)
					}
				}
				break
			} else {
				// New ordinal origin -- this output is the origin
				outputEvents = append(outputEvents, "origin:"+outpoint.OrdinalString())
				break
			}
		}
	}

	// Decode OpNS contract state (mine event)
	if o := opns.Decode(txOut.LockingScript); o != nil {
		outputEvents = append(outputEvents, "mine:"+o.Domain)
	} else if insc := inscription.Decode(txOut.LockingScript); insc != nil && insc.File.Type == "application/op-ns" {
		// Inscription claiming a domain
		domain = string(insc.File.Content)
		outputEvents = append(outputEvents, "opns:"+domain)
		// Extract address from inscription prefix or suffix
		if p := p2pkh.Decode(script.NewFromBytes(insc.ScriptPrefix), true); p != nil {
			outputEvents = append(outputEvents, fmt.Sprintf("p2pkh:%s", p.AddressString))
		} else if p := p2pkh.Decode(script.NewFromBytes(insc.ScriptSuffix), true); p != nil {
			outputEvents = append(outputEvents, fmt.Sprintf("p2pkh:%s", p.AddressString))
		}
	}

	// Detect p2pkh lock or ordlock listing
	if p := p2pkh.Decode(txOut.LockingScript, true); p != nil {
		outputEvents = append(outputEvents, fmt.Sprintf("p2pkh:%s", p.AddressString))
	} else if ol := ordlock.Decode(txOut.LockingScript); ol != nil && domain != "" {
		outputEvents = append(outputEvents, fmt.Sprintf("list:%s", domain))
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

// OutputSpent removes a spent output from all event ZSets.
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

	// Remove from each event ZSet
	for _, evt := range events {
		if err := l.store.ZRem(ctx, eventKey(string(evt)), opBytes); err != nil {
			slog.Warn("failed to remove from event ZSet on spend",
				"event", string(evt),
				"outpoint", payload.Outpoint.OrdinalString(),
				"error", err,
			)
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

	for _, evt := range events {
		if err := l.store.ZRem(ctx, eventKey(string(evt)), opBytes); err != nil {
			slog.Warn("failed to remove from event ZSet on eviction",
				"event", string(evt),
				"outpoint", outpoint.OrdinalString(),
				"error", err,
			)
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

// OwnerResult represents the owner of an OpNS domain.
type OwnerResult struct {
	Outpoint *transaction.Outpoint `json:"outpoint"`
	Address  string                `json:"address"`
}

// Owner looks up the current owner of a domain.
// Returns the outpoint holding the domain and its p2pkh address.
func (l *LookupService) Owner(ctx context.Context, domain string) (*OwnerResult, error) {
	key := eventKey("opns:" + domain)
	members, err := l.store.ZRange(ctx, key, store.ScoreRange{})
	if err != nil {
		return nil, fmt.Errorf("failed to query opns event for domain %s: %w", domain, err)
	}

	if len(members) == 0 {
		return nil, nil
	}
	if len(members) > 1 {
		return nil, fmt.Errorf("multiple outputs found for domain %s", domain)
	}

	outpoint := transaction.NewOutpointFromBytes(members[0].Member)
	if outpoint == nil {
		return nil, fmt.Errorf("failed to decode outpoint for domain %s", domain)
	}

	// Look up p2pkh address from the outpoint's events
	events, err := l.store.SMembers(ctx, outpointEventsKey(outpoint))
	if err != nil {
		return nil, fmt.Errorf("failed to load events for outpoint %s: %w", outpoint.OrdinalString(), err)
	}

	for _, evt := range events {
		evtStr := string(evt)
		if strings.HasPrefix(evtStr, "p2pkh:") {
			return &OwnerResult{
				Outpoint: outpoint,
				Address:  strings.TrimPrefix(evtStr, "p2pkh:"),
			}, nil
		}
	}

	return nil, nil
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

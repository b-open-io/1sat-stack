package lookup

import (
	"context"
	"log/slog"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/parse"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/overlay/lookup"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// OneSatLookup implements the LookupService interface for 1Sat ordinals.
// It runs configured parsers on admitted outputs and stores the results.
type OneSatLookup struct {
	storage *txo.OutputStore
	tags    []string // Which parsers to run (nil = all default)
	logger  *slog.Logger
}

// NewOneSatLookup creates a new 1Sat lookup service with default parsers
func NewOneSatLookup(storage *txo.OutputStore, logger *slog.Logger) *OneSatLookup {
	if logger == nil {
		logger = slog.Default()
	}

	return &OneSatLookup{
		storage: storage,
		tags:    nil, // Use default tags
		logger:  logger,
	}
}

// NewOneSatLookupWithTags creates a new 1Sat lookup service with specific parsers
func NewOneSatLookupWithTags(storage *txo.OutputStore, logger *slog.Logger, tags []string) *OneSatLookup {
	if logger == nil {
		logger = slog.Default()
	}

	return &OneSatLookup{
		storage: storage,
		tags:    tags,
		logger:  logger,
	}
}

// OutputAdmittedByTopic is called when an output is admitted to a topic
func (l *OneSatLookup) OutputAdmittedByTopic(ctx context.Context, payload *engine.OutputAdmittedByTopic) error {
	_, tx, txid, err := transaction.ParseBeef(payload.AtomicBEEF)
	if err != nil {
		return err
	}

	if int(payload.OutputIndex) >= len(tx.Outputs) {
		return nil
	}

	output := tx.Outputs[payload.OutputIndex]
	outpoint := &transaction.Outpoint{
		Txid:  *txid,
		Index: payload.OutputIndex,
	}

	// Parse the output
	results := parse.Parse(outpoint, output.LockingScript.Bytes(), output.Satoshis, l.tags)

	// If no parsers matched, nothing to index
	if len(results) == 0 {
		return nil
	}

	// Collect all events with tag prefixes
	var allEvents []string
	data := make(map[string]any)

	for tag, result := range results {
		// Add prefixed events
		for _, event := range result.Events {
			allEvents = append(allEvents, tag+":"+event)
		}

		// Add owner events (shared across tags)
		for _, owner := range result.Owners {
			allEvents = append(allEvents, "own:"+owner.Address())
		}

		// Store data by tag
		if result.Data != nil {
			data[tag] = result.Data
		}
	}

	// Save to storage
	score := float64(time.Now().UnixNano())
	if err := l.storage.SaveEvents(ctx, outpoint, allEvents, data, score); err != nil {
		l.logger.Error("failed to save events",
			"outpoint", outpoint.String(),
			"error", err)
		return err
	}

	l.logger.Debug("indexed output",
		"outpoint", outpoint.String(),
		"tags", len(results),
		"events", len(allEvents))

	return nil
}

// GetDocumentation returns documentation for this lookup service
func (l *OneSatLookup) GetDocumentation() string {
	return "1Sat Ordinals Lookup Service - indexes inscriptions, tokens, and protocols"
}

// GetMetaData returns metadata for this lookup service
func (l *OneSatLookup) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{
		Name: "1Sat Ordinals",
	}
}

// OutputSpent is called when a previously-admitted UTXO is spent
func (l *OneSatLookup) OutputSpent(ctx context.Context, payload *engine.OutputSpent) error {
	// Mark the output as spent in storage
	// The OutputStore handles this via its spend tracking
	return nil
}

// OutputNoLongerRetainedInHistory is called when historical retention is no longer required
func (l *OneSatLookup) OutputNoLongerRetainedInHistory(ctx context.Context, outpoint *transaction.Outpoint, topic string) error {
	return nil
}

// OutputEvicted permanently removes the UTXO from all indices
func (l *OneSatLookup) OutputEvicted(ctx context.Context, outpoint *transaction.Outpoint) error {
	return nil
}

// OutputBlockHeightUpdated is called when a transaction's block height is updated
func (l *OneSatLookup) OutputBlockHeightUpdated(ctx context.Context, txid *chainhash.Hash, blockHeight uint32, blockIndex uint64) error {
	return nil
}

// Lookup handles generic lookup queries
func (l *OneSatLookup) Lookup(ctx context.Context, question *lookup.LookupQuestion) (*lookup.LookupAnswer, error) {
	return &lookup.LookupAnswer{
		Type: lookup.AnswerTypeFormula,
	}, nil
}

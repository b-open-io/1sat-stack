package lookup

import (
	"context"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/parse"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/b-open-io/1sat-stack/pkg/types"
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
	l.logger.Debug("OutputAdmittedByTopic called", "topic", payload.Topic, "vout", payload.OutputIndex)

	_, tx, txid, err := transaction.ParseBeef(payload.AtomicBEEF)
	if err != nil {
		l.logger.Error("failed to parse BEEF", "error", err)
		return err
	}

	if int(payload.OutputIndex) >= len(tx.Outputs) {
		l.logger.Debug("output index out of range", "vout", payload.OutputIndex, "outputs", len(tx.Outputs))
		return nil
	}

	output := tx.Outputs[payload.OutputIndex]
	outpoint := &transaction.Outpoint{
		Txid:  *txid,
		Index: payload.OutputIndex,
	}

	// Parse the output
	results := parse.Parse(outpoint, output.LockingScript.Bytes(), output.Satoshis, l.tags)

	l.logger.Debug("parsed output", "outpoint", outpoint.String(), "results", len(results))

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

	// Extract score from transaction (block height if confirmed, timestamp if not)
	score := types.ScoreFromTx(tx, txid)

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
	_, tx, txid, err := transaction.ParseBeef(payload.SpendingAtomicBEEF)
	if err != nil {
		l.logger.Error("failed to parse spending BEEF", "error", err)
		return err
	}

	if int(payload.InputIndex) >= len(tx.Inputs) {
		l.logger.Debug("input index out of range", "vin", payload.InputIndex, "inputs", len(tx.Inputs))
		return nil
	}

	input := tx.Inputs[payload.InputIndex]
	if input.SourceTransaction == nil {
		l.logger.Debug("source transaction not populated", "vin", payload.InputIndex)
		return nil
	}

	if int(payload.Outpoint.Index) >= len(input.SourceTransaction.Outputs) {
		l.logger.Debug("output index out of range in source tx", "vout", payload.Outpoint.Index)
		return nil
	}

	spentOutput := input.SourceTransaction.Outputs[payload.Outpoint.Index]

	// Parse the spent output to derive events
	results := parse.Parse(payload.Outpoint, spentOutput.LockingScript.Bytes(), spentOutput.Satoshis, l.tags)

	if len(results) == 0 {
		return nil
	}

	// Collect all events with tag prefixes (same as OutputAdmittedByTopic)
	var allEvents []string
	for tag, result := range results {
		for _, event := range result.Events {
			allEvents = append(allEvents, tag+":"+event)
		}
		for _, owner := range result.Owners {
			allEvents = append(allEvents, "own:"+owner.Address())
		}
	}

	// Extract score from the spending transaction
	score := types.ScoreFromTx(tx, txid)

	// Update spent event indexes
	return l.storage.IndexSpentEvents(ctx, payload.Outpoint, allEvents, score)
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

package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// Compile-time check that EngineAdapter implements engine.Storage.
var _ engine.Storage = (*EngineAdapter)(nil)

// IngestTxFunc is a callback for triggering main indexing from overlay flow.
type IngestTxFunc func(ctx context.Context, tx *transaction.Transaction) error

// EngineAdapter implements engine.Storage by routing topic-specific operations
// to per-topic TopicStorage instances and BEEF operations to the shared BeefStore.
type EngineAdapter struct {
	// LookupTopic scopes topic-less formula hydration for a single-topic engine.
	LookupTopic  *string
	factory      Factory
	beefStore    *beef.Storage
	txTopicIndex TxTopicIndexer
	IngestTx     IngestTxFunc
}

// NewEngineAdapter creates an adapter that bridges engine.Storage to per-topic SQLite databases.
func NewEngineAdapter(factory Factory, beefStore *beef.Storage, txTopicIndex TxTopicIndexer) *EngineAdapter {
	return &EngineAdapter{
		factory:      factory,
		beefStore:    beefStore,
		txTopicIndex: txTopicIndex,
	}
}

func (a *EngineAdapter) topicDB(topic string) (TopicStorage, error) {
	return a.factory(topic)
}

// InsertOutputs inserts transaction outputs for a topic.
// Saves BEEF to shared store, triggers general indexer via IngestTx callback,
// then records membership in the per-topic database.
func (a *EngineAdapter) InsertOutputs(ctx context.Context, topic string, txid *chainhash.Hash, outputVouts []uint32, outpointsConsumed []*transaction.Outpoint, bf *transaction.Beef, ancillaryTxids []*chainhash.Hash) error {
	if len(outputVouts) == 0 {
		return nil
	}

	// Save BEEF to shared store
	if a.beefStore != nil && bf != nil {
		if err := a.beefStore.SaveBeef(ctx, txid, bf); err != nil {
			return err
		}
	}

	// Trigger general indexer
	if a.IngestTx != nil {
		if bf == nil {
			return fmt.Errorf("InsertOutputs: cannot index tx %s - BEEF is nil", txid.String())
		}
		tx := bf.FindTransactionByHash(txid)
		if tx == nil {
			return fmt.Errorf("InsertOutputs: cannot index tx %s - transaction not found in BEEF", txid.String())
		}
		for _, input := range tx.Inputs {
			if input.SourceTransaction == nil {
				input.SourceTransaction = bf.FindTransactionByHash(input.SourceTXID)
			}
		}
		if err := a.IngestTx(ctx, tx); err != nil {
			return fmt.Errorf("InsertOutputs: ingest tx %s failed: %w", txid.String(), err)
		}
	}

	db, err := a.topicDB(topic)
	if err != nil {
		return fmt.Errorf("InsertOutputs: open topic %s: %w", topic, err)
	}

	score := float64(time.Now().UnixMilli()) / 1000.0

	// Encode deps and inputs_consumed as concatenated bytes
	var deps []byte
	for _, h := range ancillaryTxids {
		deps = append(deps, h[:]...)
	}
	var inputsConsumed []byte
	for _, op := range outpointsConsumed {
		inputsConsumed = append(inputsConsumed, op.Bytes()...)
	}

	// Resolve satoshis from BEEF
	var tx *transaction.Transaction
	if bf != nil {
		tx = bf.FindTransactionByHash(txid)
	}

	for _, vout := range outputVouts {
		op := &transaction.Outpoint{Txid: *txid, Index: vout}
		var sats uint64
		if tx != nil && int(vout) < len(tx.Outputs) {
			sats = tx.Outputs[vout].Satoshis
		}
		if err := db.InsertOutput(ctx, op, txid, sats, deps, inputsConsumed, score); err != nil {
			return err
		}
	}

	// Record txid→topic mapping for rollback lookups
	if a.txTopicIndex != nil {
		if err := a.txTopicIndex.Record(ctx, txid, topic); err != nil {
			return fmt.Errorf("InsertOutputs: record tx topic mapping: %w", err)
		}
	}

	return nil
}

// FindOutput finds a single output by outpoint.
func (a *EngineAdapter) FindOutput(ctx context.Context, outpoint *transaction.Outpoint, topic *string, spent *bool, includeBEEF bool) (*engine.Output, error) {
	if topic == nil {
		topic = a.LookupTopic
	}
	if topic == nil {
		return nil, fmt.Errorf("FindOutput: topic is required")
	}

	db, err := a.topicDB(*topic)
	if err != nil {
		return nil, err
	}

	rec, err := db.GetOutput(ctx, outpoint)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, nil
	}

	output := recordToEngineOutput(rec, *topic)

	if spent != nil && output.Spent != *spent {
		return nil, nil
	}

	if includeBEEF {
		if err := a.loadBEEF(ctx, output); err != nil {
			return nil, err
		}
	}

	return output, nil
}

// FindOutputs finds multiple outputs by outpoints.
// The returned slice is index-aligned with outpoints: missing (or spent-filtered)
// entries are nil. GASP HasOutputs and engine input resolution depend on this.
func (a *EngineAdapter) FindOutputs(ctx context.Context, outpoints []*transaction.Outpoint, topic string, spent *bool, includeBEEF bool) ([]*engine.Output, error) {
	db, err := a.topicDB(topic)
	if err != nil {
		return nil, err
	}

	recs, err := db.FindOutputs(ctx, outpoints)
	if err != nil {
		return nil, err
	}

	byOutpoint := make(map[string]*engine.Output, len(recs))
	for i := range recs {
		o := recordToEngineOutput(&recs[i], topic)
		if spent != nil && o.Spent != *spent {
			continue
		}
		if includeBEEF {
			if err := a.loadBEEF(ctx, o); err != nil {
				return nil, err
			}
		}
		byOutpoint[recs[i].Outpoint.String()] = o
	}

	outputs := make([]*engine.Output, len(outpoints))
	for i, op := range outpoints {
		if op == nil {
			continue
		}
		outputs[i] = byOutpoint[op.String()]
	}

	return outputs, nil
}

// FindOutputsForTransaction finds outputs for a transaction across all topics
// using the txid→topics index.
func (a *EngineAdapter) FindOutputsForTransaction(ctx context.Context, txid *chainhash.Hash, includeBEEF bool) ([]*engine.Output, error) {
	if a.txTopicIndex == nil {
		return nil, nil
	}

	topics, err := a.txTopicIndex.Topics(ctx, txid)
	if err != nil {
		return nil, fmt.Errorf("FindOutputsForTransaction: lookup topics: %w", err)
	}

	var outputs []*engine.Output
	for _, topic := range topics {
		db, err := a.topicDB(topic)
		if err != nil {
			return nil, err
		}
		recs, err := db.FindOutputsForTransaction(ctx, txid)
		if err != nil {
			return nil, err
		}
		for i := range recs {
			o := recordToEngineOutput(&recs[i], topic)
			if includeBEEF {
				if err := a.loadBEEF(ctx, o); err != nil {
					return nil, err
				}
			}
			outputs = append(outputs, o)
		}
	}

	return outputs, nil
}

// FindUTXOsForTopic finds unspent outputs for a topic.
func (a *EngineAdapter) FindUTXOsForTopic(ctx context.Context, topic string, since float64, limit uint32, includeBEEF bool) ([]*engine.Output, error) {
	db, err := a.topicDB(topic)
	if err != nil {
		return nil, err
	}

	recs, err := db.FindUTXOs(ctx, &QueryOpts{Since: since, Limit: limit})
	if err != nil {
		return nil, err
	}

	outputs := make([]*engine.Output, 0, len(recs))
	for i := range recs {
		o := recordToEngineOutput(&recs[i], topic)
		if includeBEEF {
			if err := a.loadBEEF(ctx, o); err != nil {
				return nil, err
			}
		}
		outputs = append(outputs, o)
	}

	return outputs, nil
}

// DeleteOutput removes an output from a topic.
func (a *EngineAdapter) DeleteOutput(ctx context.Context, outpoint *transaction.Outpoint, topic string) error {
	db, err := a.topicDB(topic)
	if err != nil {
		return err
	}
	return db.DeleteOutput(ctx, outpoint)
}

// MarkUTXOsAsSpent marks multiple outputs as spent.
func (a *EngineAdapter) MarkUTXOsAsSpent(ctx context.Context, outpoints []*transaction.Outpoint, topic string, spendTxid *chainhash.Hash) error {
	if spendTxid == nil {
		return nil
	}
	db, err := a.topicDB(topic)
	if err != nil {
		return err
	}
	for _, op := range outpoints {
		if err := db.MarkSpent(ctx, op, spendTxid); err != nil {
			return err
		}
	}
	return nil
}

// UpdateConsumedBy persists the consumed_by relationship.
func (a *EngineAdapter) UpdateConsumedBy(ctx context.Context, outpoint *transaction.Outpoint, topic string, consumedBy []*transaction.Outpoint) error {
	db, err := a.topicDB(topic)
	if err != nil {
		return err
	}
	var blob []byte
	for _, op := range consumedBy {
		blob = append(blob, op.Bytes()...)
	}
	return db.UpdateConsumedBy(ctx, outpoint, blob)
}

// UpdateTransactionBEEF updates BEEF in the shared store.
func (a *EngineAdapter) UpdateTransactionBEEF(ctx context.Context, txid *chainhash.Hash, bf *transaction.Beef) error {
	if a.beefStore != nil && bf != nil {
		return a.beefStore.SaveBeef(ctx, txid, bf)
	}
	return nil
}

// UpdateOutputBlockHeight is a no-op — overlay databases use ingestion timestamp, not block height.
func (a *EngineAdapter) UpdateOutputBlockHeight(ctx context.Context, outpoint *transaction.Outpoint, topic string, blockHeight uint32, blockIndex uint64) error {
	return nil
}

// InsertAppliedTransaction records an applied transaction.
func (a *EngineAdapter) InsertAppliedTransaction(ctx context.Context, tx *overlay.AppliedTransaction) error {
	db, err := a.topicDB(tx.Topic)
	if err != nil {
		return err
	}
	score := float64(time.Now().UnixMilli()) / 1000.0
	return db.InsertAppliedTx(ctx, tx.Txid, score)
}

// DoesAppliedTransactionExist checks if a transaction has been applied.
func (a *EngineAdapter) DoesAppliedTransactionExist(ctx context.Context, tx *overlay.AppliedTransaction) (bool, error) {
	db, err := a.topicDB(tx.Topic)
	if err != nil {
		return false, err
	}
	return db.HasAppliedTx(ctx, tx.Txid)
}

// UpdateLastInteraction updates the last GASP sync timestamp.
func (a *EngineAdapter) UpdateLastInteraction(ctx context.Context, host, topic string, since float64) error {
	db, err := a.topicDB(topic)
	if err != nil {
		return err
	}
	return db.UpdateLastInteraction(ctx, host, since)
}

// GetLastInteraction retrieves the last GASP sync timestamp.
func (a *EngineAdapter) GetLastInteraction(ctx context.Context, host, topic string) (float64, error) {
	db, err := a.topicDB(topic)
	if err != nil {
		return 0, err
	}
	return db.GetLastInteraction(ctx, host)
}

// FindOutpointsByMerkleState is a no-op — proof validation is handled by PendingAuditor.
func (a *EngineAdapter) FindOutpointsByMerkleState(ctx context.Context, topic string, state engine.MerkleState, limit uint32) ([]*transaction.Outpoint, error) {
	return nil, nil
}

// ReconcileMerkleRoot is a no-op — proof validation is handled by PendingAuditor.
func (a *EngineAdapter) ReconcileMerkleRoot(ctx context.Context, topic string, blockHeight uint32, merkleRoot *chainhash.Hash) error {
	return nil
}

// LoadAncillaryBeef merges ancillary transactions into output's BEEF.
func (a *EngineAdapter) LoadAncillaryBeef(ctx context.Context, output *engine.Output) error {
	if len(output.AncillaryTxids) == 0 || a.beefStore == nil {
		return nil
	}
	if output.Beef == nil {
		return fmt.Errorf("output has no BEEF to merge into")
	}
	mergedBeef, err := a.beefStore.MergeBeef(ctx, output.Beef, output.AncillaryTxids)
	if err != nil {
		return err
	}
	output.Beef = mergedBeef
	return nil
}

// --- conversion helpers ---

func recordToEngineOutput(rec *OutputRecord, topic string) *engine.Output {
	o := &engine.Output{
		Outpoint: rec.Outpoint,
		Topic:    topic,
		Score:    rec.Score,
		Spent:    rec.SpendTxid != nil,
	}

	// Decode deps → AncillaryTxids
	if len(rec.Deps) > 0 && len(rec.Deps)%32 == 0 {
		for i := 0; i < len(rec.Deps); i += 32 {
			h := &chainhash.Hash{}
			copy(h[:], rec.Deps[i:i+32])
			o.AncillaryTxids = append(o.AncillaryTxids, h)
		}
	}

	// Decode inputs_consumed → OutputsConsumed
	if len(rec.InputsConsumed) > 0 && len(rec.InputsConsumed)%36 == 0 {
		for i := 0; i < len(rec.InputsConsumed); i += 36 {
			op := transaction.NewOutpointFromBytes(rec.InputsConsumed[i : i+36])
			if op != nil {
				o.OutputsConsumed = append(o.OutputsConsumed, op)
			}
		}
	}

	// Decode consumed_by → ConsumedBy
	if len(rec.ConsumedBy) > 0 && len(rec.ConsumedBy)%36 == 0 {
		for i := 0; i < len(rec.ConsumedBy); i += 36 {
			op := transaction.NewOutpointFromBytes(rec.ConsumedBy[i : i+36])
			if op != nil {
				o.ConsumedBy = append(o.ConsumedBy, op)
			}
		}
	}

	return o
}

func (a *EngineAdapter) loadBEEF(ctx context.Context, output *engine.Output) error {
	if a.beefStore == nil || output.Beef != nil {
		return nil
	}
	bf, err := a.beefStore.LoadBeef(ctx, &output.Outpoint.Txid)
	if err != nil {
		return fmt.Errorf("failed to load BEEF for %s: %w", output.Outpoint.Txid.String(), err)
	}
	output.Beef = bf
	return nil
}

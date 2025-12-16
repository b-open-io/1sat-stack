package txo

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// engine.Storage implementation for OutputStore
// These methods handle overlay/topic operations using timestamp-based scoring

// InsertOutputs inserts transaction outputs for a topic (overlay flow)
// Uses UnixNano timestamp for scoring since these are not indexed by block position
func (s *OutputStore) InsertOutputs(ctx context.Context, topic string, txid *chainhash.Hash, outputVouts []uint32, outpointsConsumed []*transaction.Outpoint, beefData []byte, ancillaryTxids []*chainhash.Hash) error {
	if len(outputVouts) == 0 {
		return nil
	}

	// Store BEEF in shared storage
	if s.BeefStore != nil && len(beefData) > 0 {
		if err := s.BeefStore.SaveBeef(ctx, txid, beefData); err != nil {
			return err
		}
	}

	// Use timestamp-based score for topic events (NOT HeightScore)
	score := float64(time.Now().UnixNano())

	// Process each output
	for _, vout := range outputVouts {
		op := &transaction.Outpoint{Txid: *txid, Index: vout}

		// Save inputs consumed for this topic
		if len(outpointsConsumed) > 0 {
			if err := s.SaveInputsConsumed(ctx, op, topic, outpointsConsumed); err != nil {
				return err
			}
		}

		// Save ancillary txids (deps) for this topic
		if len(ancillaryTxids) > 0 {
			if err := s.SaveDeps(ctx, op, topic, ancillaryTxids); err != nil {
				return err
			}
		}

		// Add to topic's output set
		if err := s.AddToTopic(ctx, op, topic, score); err != nil {
			return err
		}
	}

	// Add txid to topic's applied transactions
	return s.AddTxToTopic(ctx, txid, topic, score)
}

// FindOutput finds a single output by outpoint
func (s *OutputStore) FindOutput(ctx context.Context, outpoint *transaction.Outpoint, topic *string, spent *bool, includeBEEF bool) (*engine.Output, error) {
	// Check spent status if filtering
	var isSpent bool
	if spent != nil {
		spendTxid, err := s.GetSpend(ctx, outpoint)
		if err != nil {
			return nil, err
		}
		isSpent = spendTxid != nil
		if *spent != isSpent {
			return nil, nil
		}
	}

	indexed, err := s.LoadOutput(ctx, outpoint, nil)
	if err != nil || indexed == nil {
		return nil, err
	}

	output := &indexed.Output
	output.Spent = isSpent
	if topic != nil {
		output.Topic = *topic
	}

	// Load BEEF if requested
	if includeBEEF && s.BeefStore != nil && len(output.Beef) == 0 {
		beef, err := s.BeefStore.LoadBeef(ctx, &outpoint.Txid)
		if err != nil {
			return nil, fmt.Errorf("failed to load BEEF for %s: %w", outpoint.Txid.String(), err)
		}
		output.Beef = beef
	}

	return output, nil
}

// FindOutputs finds multiple outputs by outpoints
func (s *OutputStore) FindOutputs(ctx context.Context, outpoints []*transaction.Outpoint, topic string, spent *bool, includeBEEF bool) ([]*engine.Output, error) {
	// Get spent status for all outpoints
	spends, err := s.GetSpends(ctx, outpoints)
	if err != nil {
		return nil, err
	}

	// Determine which to load based on spent filter
	var toLoad []*transaction.Outpoint
	var toLoadIdx []int
	isSpent := make([]bool, len(outpoints))

	for i, spendTxid := range spends {
		isSpent[i] = spendTxid != nil
		if spent == nil || *spent == isSpent[i] {
			toLoad = append(toLoad, outpoints[i])
			toLoadIdx = append(toLoadIdx, i)
		}
	}

	if len(toLoad) == 0 {
		return []*engine.Output{}, nil
	}

	indexed, err := s.loadOutputs(ctx, toLoad, nil)
	if err != nil {
		return nil, err
	}

	outputs := make([]*engine.Output, len(indexed))
	for i, idx := range indexed {
		if idx != nil {
			output := &idx.Output
			output.Topic = topic
			output.Spent = isSpent[toLoadIdx[i]]
			if includeBEEF && s.BeefStore != nil && len(output.Beef) == 0 {
				beef, err := s.BeefStore.LoadBeef(ctx, &outpoints[toLoadIdx[i]].Txid)
				if err != nil {
					return nil, fmt.Errorf("failed to load BEEF for %s: %w", outpoints[toLoadIdx[i]].Txid.String(), err)
				}
				output.Beef = beef
			}
			outputs[i] = output
		}
	}

	return outputs, nil
}

// FindOutputsForTransaction finds outputs for a transaction
func (s *OutputStore) FindOutputsForTransaction(ctx context.Context, txid *chainhash.Hash, includeBEEF bool) ([]*engine.Output, error) {
	indexed, err := s.LoadOutputsByTxid(ctx, txid, nil)
	if err != nil {
		return nil, err
	}

	// Get spent status
	ops := make([]*transaction.Outpoint, 0, len(indexed))
	validIndexed := make([]*IndexedOutput, 0, len(indexed))
	for _, idx := range indexed {
		if idx != nil {
			ops = append(ops, &idx.Outpoint)
			validIndexed = append(validIndexed, idx)
		}
	}

	spends, err := s.GetSpends(ctx, ops)
	if err != nil {
		return nil, err
	}

	outputs := make([]*engine.Output, len(validIndexed))
	for i, idx := range validIndexed {
		output := &idx.Output
		output.Spent = spends[i] != nil
		if includeBEEF && s.BeefStore != nil && len(output.Beef) == 0 {
			beef, err := s.BeefStore.LoadBeef(ctx, txid)
			if err != nil {
				return nil, fmt.Errorf("failed to load BEEF for %s: %w", txid.String(), err)
			}
			output.Beef = beef
		}
		outputs[i] = output
	}

	return outputs, nil
}

// FindUTXOsForTopic finds unspent outputs for a topic
func (s *OutputStore) FindUTXOsForTopic(ctx context.Context, topic string, since float64, limit uint32, includeBEEF bool) ([]*engine.Output, error) {
	cfg := &OutputSearchCfg{
		SearchCfg: store.SearchCfg{
			Keys:  [][]byte{keyTopicOut(topic)},
			From:  &since,
			Limit: limit,
		},
		FilterSpent: true,
	}

	// Search directly on the topic key (already has z: prefix from keyTopicOut)
	results, err := s.Store.Search(ctx, &cfg.SearchCfg)
	if err != nil {
		return nil, err
	}

	// Filter spent
	results, err = s.filterSpent(ctx, results)
	if err != nil {
		return nil, err
	}

	ops := make([]*transaction.Outpoint, len(results))
	for i, r := range results {
		ops[i] = transaction.NewOutpointFromBytes(r.Member)
	}

	indexed, err := s.loadOutputs(ctx, ops, nil)
	if err != nil {
		return nil, err
	}

	outputs := make([]*engine.Output, 0, len(indexed))
	for _, idx := range indexed {
		if idx != nil {
			output := &idx.Output
			output.Topic = topic
			output.Spent = false
			if includeBEEF && s.BeefStore != nil && len(output.Beef) == 0 {
				beef, err := s.BeefStore.LoadBeef(ctx, &idx.Outpoint.Txid)
				if err != nil {
					return nil, fmt.Errorf("failed to load BEEF for %s: %w", idx.Outpoint.Txid.String(), err)
				}
				output.Beef = beef
			}
			outputs = append(outputs, output)
		}
	}

	return outputs, nil
}

// DeleteOutput removes an output from a topic
func (s *OutputStore) DeleteOutput(ctx context.Context, outpoint *transaction.Outpoint, topic string) error {
	opBytes := outpointBytes(outpoint)

	// Remove from topic sorted set
	s.Store.ZRem(ctx, keyTopicOut(topic), opBytes)

	// Remove topic-specific deps and inputs
	hashKey := keyOutHash(outpoint)
	s.Store.HDel(ctx, hashKey, []byte(fldDeps+topic))
	s.Store.HDel(ctx, hashKey, []byte(fldInputs+topic))

	// Note: We don't delete the output itself as it may belong to other topics
	return nil
}

// MarkUTXOsAsSpent marks multiple outputs as spent
// Note: In the unified model, spend tracking is primarily handled by the indexer via SaveSpend
// This method is kept for engine.Storage compatibility
func (s *OutputStore) MarkUTXOsAsSpent(ctx context.Context, outpoints []*transaction.Outpoint, topic string, spendTxid *chainhash.Hash) error {
	if spendTxid == nil {
		return nil
	}

	score := float64(time.Now().UnixNano())
	for _, op := range outpoints {
		if err := s.SaveSpend(ctx, op, spendTxid, score); err != nil {
			return err
		}
	}
	return nil
}

// UpdateConsumedBy is a no-op - consumed-by is derived on the fly
func (s *OutputStore) UpdateConsumedBy(ctx context.Context, outpoint *transaction.Outpoint, topic string, consumedBy []*transaction.Outpoint) error {
	// No-op: ConsumedBy is derived by:
	// 1. Get spend txid: GetSpend(outpoint)
	// 2. Find outputs of spend tx: LoadOutputsByTxid(spendTxid)
	// 3. Filter by topic if needed
	return nil
}

// UpdateTransactionBEEF updates the BEEF for a transaction
func (s *OutputStore) UpdateTransactionBEEF(ctx context.Context, txid *chainhash.Hash, beefData []byte) error {
	if s.BeefStore != nil {
		return s.BeefStore.SaveBeef(ctx, txid, beefData)
	}
	return nil
}

// UpdateOutputBlockHeight updates the block height for an output
func (s *OutputStore) UpdateOutputBlockHeight(ctx context.Context, outpoint *transaction.Outpoint, topic string, blockHeight uint32, blockIndex uint64) error {
	score := HeightScore(blockHeight, blockIndex)

	// Update in topic sorted set
	return s.Store.ZAdd(ctx, keyTopicOut(topic), store.ScoredMember{
		Member: outpointBytes(outpoint),
		Score:  score,
	})
}

// InsertAppliedTransaction records an applied transaction
func (s *OutputStore) InsertAppliedTransaction(ctx context.Context, tx *overlay.AppliedTransaction) error {
	score := float64(time.Now().UnixNano())
	return s.AddTxToTopic(ctx, tx.Txid, tx.Topic, score)
}

// DoesAppliedTransactionExist checks if a transaction has been applied to a topic
func (s *OutputStore) DoesAppliedTransactionExist(ctx context.Context, tx *overlay.AppliedTransaction) (bool, error) {
	return s.IsTxInTopic(ctx, tx.Txid, tx.Topic)
}

// UpdateLastInteraction updates the last interaction timestamp for a host/topic
func (s *OutputStore) UpdateLastInteraction(ctx context.Context, host, topic string, since float64) error {
	return s.Store.HSet(ctx, keyPeerInteraction(topic), []byte(host), []byte(fmt.Sprintf("%.0f", since)))
}

// GetLastInteraction retrieves the last interaction timestamp for a host/topic
func (s *OutputStore) GetLastInteraction(ctx context.Context, host, topic string) (float64, error) {
	scoreBytes, err := s.Store.HGet(ctx, keyPeerInteraction(topic), []byte(host))
	if err == store.ErrKeyNotFound || len(scoreBytes) == 0 {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(string(scoreBytes), 64)
}

// FindOutpointsByMerkleState finds outpoints by their merkle validation state
func (s *OutputStore) FindOutpointsByMerkleState(ctx context.Context, topic string, state engine.MerkleState, limit uint32) ([]*transaction.Outpoint, error) {
	// Query merkle state index
	stateKey := []byte(fmt.Sprintf("%smerkle:%s:%d", pfxZSet, topic, state))

	results, err := s.Store.ZRange(ctx, stateKey, store.ScoreRange{Count: int64(limit)})
	if err != nil {
		return nil, err
	}

	outpoints := make([]*transaction.Outpoint, 0, len(results))
	for _, r := range results {
		op := transaction.NewOutpointFromBytes(r.Member)
		if op != nil {
			outpoints = append(outpoints, op)
		}
	}

	return outpoints, nil
}

// ReconcileMerkleRoot reconciles outputs at a specific block height
func (s *OutputStore) ReconcileMerkleRoot(ctx context.Context, topic string, blockHeight uint32, merkleRoot *chainhash.Hash) error {
	// This would update merkle state for all outputs at the given block height
	// For now, no-op since we update during UpdateTransactionBEEF
	return nil
}

// LoadAncillaryBeef merges ancillary transactions into output's BEEF
func (s *OutputStore) LoadAncillaryBeef(ctx context.Context, output *engine.Output) error {
	if len(output.AncillaryTxids) == 0 || s.BeefStore == nil {
		return nil
	}

	beefParsed, _, _, err := transaction.ParseBeef(output.Beef)
	if err != nil {
		return fmt.Errorf("failed to parse beef: %w", err)
	}

	mergedBeef, err := s.BeefStore.MergeBeef(ctx, beefParsed, output.AncillaryTxids)
	if err != nil {
		return err
	}

	output.Beef, err = mergedBeef.Bytes()
	return err
}

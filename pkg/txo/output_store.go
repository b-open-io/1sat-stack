package txo

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/pubsub"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// Bulk lookup hash keys
var (
	hashSats = KeySatoshis
	hashSpnd = KeySpends
)

// Hash field prefixes (within h:{outpoint})
const (
	fldEvent  = "ev"  // events (JSON string array)
	fldMerkle = "ms"  // merkle state (binary)
	fldData   = "dt:" // prefix for dt:{tag}
	fldDeps   = "dp:" // prefix for dp:{topic} - ancillary txids
	fldInputs = "in:" // prefix for in:{topic} - inputs consumed
)

// === OutputStore ===

// IngestTxFunc is a callback for triggering main indexing from overlay flow.
// Set this after initialization to wire up the indexer.
type IngestTxFunc func(ctx context.Context, tx *transaction.Transaction) error

// OutputStore provides global output storage.
// Output data is keyed by binary outpoint for locality.
// Spends and satoshis use hashes for efficient bulk lookups.
type OutputStore struct {
	Store     store.Store
	PubSub    pubsub.PubSub
	BeefStore *beef.Storage

	// IngestTx is an optional callback to trigger main indexing when
	// outputs are inserted via overlay flow. Set after initialization
	// to wire up the indexer (avoids circular imports).
	IngestTx IngestTxFunc
}

// NewOutputStore creates a new global OutputStore
func NewOutputStore(s store.Store, ps pubsub.PubSub, beefStore *beef.Storage) *OutputStore {
	return &OutputStore{
		Store:     s,
		PubSub:    ps,
		BeefStore: beefStore,
	}
}

// === Save Operations ===

// SaveOutput saves a single output with its events and data (indexer flow)
func (s *OutputStore) SaveOutput(ctx context.Context, output *IndexedOutput, satoshis uint64, score float64) error {
	op := &output.Outpoint
	opBytes := op.Bytes()
	hashKey := KeyOutHash(op)

	// Build events list - include txid and owner events
	events := make([]string, 0, len(output.Events)+len(output.Owners)+1)
	events = append(events, "txid:"+op.Txid.String())
	events = append(events, output.Events...)
	for _, owner := range output.Owners {
		if !owner.IsZero() {
			events = append(events, "own:"+owner.Address())
		}
	}

	// Store events in hash
	if len(events) > 0 {
		eventsJSON, err := json.Marshal(events)
		if err != nil {
			return err
		}
		if err := s.Store.HSet(ctx, hashKey, []byte(fldEvent), eventsJSON); err != nil {
			return err
		}
	}

	// Store merkle state if we have block info
	if output.BlockHeight != nil && *output.BlockHeight > 0 {
		ms := make([]byte, 12) // height(4) + idx(8)
		binary.BigEndian.PutUint32(ms[0:4], *output.BlockHeight)
		blockIdx := uint64(0)
		if output.BlockIdx != nil {
			blockIdx = *output.BlockIdx
		}
		binary.BigEndian.PutUint64(ms[4:12], blockIdx)
		if err := s.Store.HSet(ctx, hashKey, []byte(fldMerkle), ms); err != nil {
			return err
		}
	}

	// Store tag-specific data
	for tag, data := range output.Data {
		if data != nil {
			dataJSON, err := json.Marshal(data)
			if err != nil {
				return err
			}
			if err := s.Store.HSet(ctx, hashKey, []byte(fldData+tag), dataJSON); err != nil {
				return err
			}
		}
	}

	// Store satoshis in bulk lookup hash
	satsBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(satsBytes, satoshis)
	if err := s.Store.HSet(ctx, hashSats, opBytes, satsBytes); err != nil {
		return err
	}

	// Add to sorted sets for each event
	for _, event := range events {
		if err := s.Store.ZAdd(ctx, KeyEvent(event), store.ScoredMember{
			Member: opBytes,
			Score:  score,
		}); err != nil {
			return err
		}
	}

	// Publish events
	if s.PubSub != nil {
		opStr := op.String()
		for _, event := range events {
			s.PubSub.Publish(ctx, event, opStr)
		}
	}

	return nil
}

// SaveEvents saves events and data for an outpoint (lookup service flow)
// Uses timestamp-based scoring (time.Now().UnixNano() passed as score)
func (s *OutputStore) SaveEvents(ctx context.Context, op *transaction.Outpoint, events []string, data map[string]any, score float64) error {
	opBytes := op.Bytes()
	hashKey := KeyOutHash(op)

	// Store events in hash
	if len(events) > 0 {
		eventsJSON, err := json.Marshal(events)
		if err != nil {
			return err
		}
		if err := s.Store.HSet(ctx, hashKey, []byte(fldEvent), eventsJSON); err != nil {
			return err
		}
	}

	// Store tag-specific data
	for tag, tagData := range data {
		if tagData != nil {
			dataJSON, err := json.Marshal(tagData)
			if err != nil {
				return err
			}
			if err := s.Store.HSet(ctx, hashKey, []byte(fldData+tag), dataJSON); err != nil {
				return err
			}
		}
	}

	// Add to sorted sets for each event
	for _, event := range events {
		if err := s.Store.ZAdd(ctx, KeyEvent(event), store.ScoredMember{
			Member: opBytes,
			Score:  score,
		}); err != nil {
			return err
		}
	}

	// Publish events
	if s.PubSub != nil {
		opStr := op.String()
		for _, event := range events {
			s.PubSub.Publish(ctx, event, opStr)
		}
	}

	return nil
}

// === Spend Operations ===

// SaveSpend marks an output as spent and updates spent indexes.
func (s *OutputStore) SaveSpend(ctx context.Context, op *transaction.Outpoint, spendTxid *chainhash.Hash, events []string, score float64) error {
	// Store spend txid in bulk lookup hash
	if err := s.Store.HSet(ctx, hashSpnd, op.Bytes(), spendTxid[:]); err != nil {
		return err
	}

	return s.IndexSpentEvents(ctx, op, events, score)
}

// IndexSpentEvents adds the outpoint to spent event sorted sets with the given score.
// Events are derived by the lookup service from the spent output's script.
func (s *OutputStore) IndexSpentEvents(ctx context.Context, op *transaction.Outpoint, events []string, score float64) error {
	opBytes := op.Bytes()

	for _, event := range events {
		if err := s.Store.ZAdd(ctx, KeyEventSpent(event), store.ScoredMember{
			Member: opBytes,
			Score:  score,
		}); err != nil {
			return fmt.Errorf("failed to add to spent index %s: %w", event, err)
		}
	}

	return nil
}

// SaveTransaction saves outputs, spends, and logs the transaction as pending.
func (s *OutputStore) SaveTransaction(ctx context.Context, tx *transaction.Transaction, outputs []*IndexedOutput, spends []*IndexedOutput, txidHex string, score float64) error {
	txid := tx.TxID()

	// Save each output
	for i, output := range outputs {
		if output == nil {
			continue
		}
		satoshis := tx.Outputs[i].Satoshis
		if err := s.SaveOutput(ctx, output, satoshis, score); err != nil {
			return err
		}
	}

	// Save spends
	for i, spend := range spends {
		if spend == nil {
			continue
		}
		input := tx.Inputs[i]
		outpoint := &transaction.Outpoint{
			Txid:  *input.SourceTXID,
			Index: input.SourceTxOutIndex,
		}
		events := buildSpendEvents(spend)
		if err := s.SaveSpend(ctx, outpoint, txid, events, score); err != nil {
			return err
		}
	}

	// Log to tx:pending — the audit process handles promotion to tx:immutable
	if err := s.Store.ZAdd(ctx, KeyLog(PendingTxLog), store.ScoredMember{
		Member: []byte(txidHex),
		Score:  score,
	}); err != nil {
		return err
	}

	return nil
}

// buildSpendEvents constructs the events list for a spent output.
func buildSpendEvents(output *IndexedOutput) []string {
	events := make([]string, 0, len(output.Events)+len(output.Owners)+1)
	events = append(events, "txid:"+output.Outpoint.Txid.String())
	events = append(events, output.Events...)
	for _, owner := range output.Owners {
		if !owner.IsZero() {
			events = append(events, "own:"+owner.Address())
		}
	}
	return events
}

// GetSpend returns the spending txid for an outpoint (nil if unspent)
func (s *OutputStore) GetSpend(ctx context.Context, op *transaction.Outpoint) (*chainhash.Hash, error) {
	spendBytes, err := s.Store.HGet(ctx, hashSpnd, op.Bytes())
	if err == store.ErrKeyNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(spendBytes) != 32 {
		return nil, nil
	}

	txid := &chainhash.Hash{}
	copy(txid[:], spendBytes)
	return txid, nil
}

// GetSpends returns spending txids for multiple outpoints (bulk)
func (s *OutputStore) GetSpends(ctx context.Context, ops []*transaction.Outpoint) ([]*chainhash.Hash, error) {
	if len(ops) == 0 {
		return nil, nil
	}

	fields := make([][]byte, len(ops))
	for i, op := range ops {
		if op == nil {
			continue
		}
		fields[i] = op.Bytes()
	}

	values, err := s.Store.HMGet(ctx, hashSpnd, fields...)
	if err != nil {
		return nil, err
	}

	result := make([]*chainhash.Hash, len(values))
	for i, v := range values {
		if len(v) == 32 {
			result[i] = &chainhash.Hash{}
			copy(result[i][:], v)
		}
	}
	return result, nil
}

// IsSpent checks if an output is spent
func (s *OutputStore) IsSpent(ctx context.Context, op *transaction.Outpoint) (bool, error) {
	spend, err := s.GetSpend(ctx, op)
	return spend != nil, err
}

// === Satoshi Operations ===

// GetSats returns satoshi value for an outpoint
func (s *OutputStore) GetSats(ctx context.Context, op *transaction.Outpoint) (uint64, error) {
	value, err := s.Store.HGet(ctx, hashSats, op.Bytes())
	if err == store.ErrKeyNotFound {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if len(value) != 8 {
		return 0, nil
	}
	return binary.BigEndian.Uint64(value), nil
}

// GetSatsBulk returns satoshi values for multiple outpoints
func (s *OutputStore) GetSatsBulk(ctx context.Context, ops []*transaction.Outpoint) ([]uint64, error) {
	if len(ops) == 0 {
		return nil, nil
	}

	fields := make([][]byte, len(ops))
	for i, op := range ops {
		fields[i] = op.Bytes()
	}

	values, err := s.Store.HMGet(ctx, hashSats, fields...)
	if err != nil {
		return nil, err
	}

	sats := make([]uint64, len(ops))
	for i, v := range values {
		if len(v) == 8 {
			sats[i] = binary.BigEndian.Uint64(v)
		}
	}
	return sats, nil
}

// === Search Operations ===

// Search performs a multi-key search with optional spent filtering.
// Keys are expected to have a type prefix:
//   - "ev:{event}" → events (have hash data)
//   - "tp:{topic}" → topic membership (no hash data)
//
// If no prefix is provided, "ev:" is assumed for backwards compatibility.
// The "z:" storage prefix is added automatically.
//
// When FilterSpent is true, the search paginates internally to ensure the
// requested Limit of unspent results is returned (if available).
func (s *OutputStore) Search(ctx context.Context, cfg *OutputSearchCfg) ([]store.ScoredMember, error) {
	prefixedCfg := cfg.SearchCfg
	prefixedCfg.Keys = make([][]byte, len(cfg.Keys))

	for i, k := range cfg.Keys {
		key := string(k)
		prefixedCfg.Keys[i] = prefixKey(key)
	}

	// If not filtering spent, just do a simple search
	if !cfg.FilterSpent {
		return s.Store.Search(ctx, &prefixedCfg)
	}

	// When filtering spent outputs, we need to paginate internally to collect
	// enough unspent results to satisfy the requested limit
	requestedLimit := prefixedCfg.Limit
	if requestedLimit == 0 {
		// No limit specified - fetch all and filter
		results, err := s.Store.Search(ctx, &prefixedCfg)
		if err != nil {
			return nil, err
		}
		return s.filterSpent(ctx, results)
	}

	// Paginate to collect enough unspent results
	const batchMultiplier uint32 = 4 // Fetch more than needed to account for spent outputs
	const maxIterations = 100        // Safety limit to prevent infinite loops

	var unspent []store.ScoredMember
	var currentFrom *float64
	if prefixedCfg.From != nil {
		fromCopy := *prefixedCfg.From
		currentFrom = &fromCopy
	}

	for i := 0; i < maxIterations && uint32(len(unspent)) < requestedLimit; i++ {
		// Calculate how many more we need, with buffer for filtering
		remaining := requestedLimit - uint32(len(unspent))
		batchSize := remaining * batchMultiplier
		if batchSize < 100 {
			batchSize = 100 // Minimum batch size
		}

		batchCfg := prefixedCfg
		batchCfg.Limit = batchSize
		batchCfg.From = currentFrom

		results, err := s.Store.Search(ctx, &batchCfg)
		if err != nil {
			return nil, err
		}

		if len(results) == 0 {
			break // No more results
		}

		// Filter this batch
		filtered, err := s.filterSpent(ctx, results)
		if err != nil {
			return nil, err
		}

		unspent = append(unspent, filtered...)

		// Update cursor for next iteration - use math.Nextafter to get next
		// representable float64 value, ensuring we don't re-fetch the same score
		lastScore := results[len(results)-1].Score
		var nextScore float64
		if prefixedCfg.Reverse {
			// For reverse order, go to the next smaller value
			nextScore = math.Nextafter(lastScore, math.Inf(-1))
		} else {
			// For forward order, go to the next larger value
			nextScore = math.Nextafter(lastScore, math.Inf(1))
		}
		currentFrom = new(float64)
		*currentFrom = nextScore

		// If we got fewer results than requested, we've exhausted the data
		if uint32(len(results)) < batchSize {
			break
		}
	}

	// Trim to requested limit
	if uint32(len(unspent)) > requestedLimit {
		unspent = unspent[:requestedLimit]
	}

	return unspent, nil
}

// prefixKey ensures a key has an application namespace prefix.
// Keys starting with "ev:" or "tp:" pass through as-is.
// All other keys default to "ev:" prefix for backwards compatibility.
func prefixKey(key string) []byte {
	if len(key) >= 3 {
		switch key[:3] {
		case PfxEvent: // "ev:"
			return []byte(key)
		case PfxTopic: // "tp:"
			return []byte(key)
		}
	}

	return KeyEvent(key)
}

// CheckMembership checks if specific members exist in the given keys.
// Returns ScoredMembers for those that exist (with their scores from the ZSets).
// This is useful for validating specific outpoints against topic or event indices
// without iterating through the entire ZSet.
//
// Keys are automatically prefixed if needed (same as Search).
// Members that don't exist in any of the keys are omitted from results.
// Multiple keys are combined with union logic (member found in any key).
func (s *OutputStore) CheckMembership(ctx context.Context, keys [][]byte, members [][]byte) ([]store.ScoredMember, error) {
	var results []store.ScoredMember
	seen := make(map[string]bool) // Deduplicate if member exists in multiple keys

	// Prefix keys same way Search does
	prefixedKeys := make([][]byte, len(keys))
	for i, k := range keys {
		prefixedKeys[i] = prefixKey(string(k))
	}

	// Check each member against each key
	for _, key := range prefixedKeys {
		for _, member := range members {
			memberStr := string(member)
			if seen[memberStr] {
				continue // Already found in a previous key
			}

			score, err := s.Store.ZScore(ctx, key, member)
			if err == store.ErrKeyNotFound {
				continue // Not in this key, try next
			}
			if err != nil {
				return nil, err
			}

			// Found it!
			results = append(results, store.ScoredMember{
				Member: member,
				Score:  score,
				Key:    key,
			})
			seen[memberStr] = true
		}
	}

	return results, nil
}

// SearchBalance calculates total satoshi balance (excludes spent)
func (s *OutputStore) SearchBalance(ctx context.Context, cfg *OutputSearchCfg) (uint64, int, error) {
	cfg.FilterSpent = true
	results, err := s.Search(ctx, cfg)
	if err != nil {
		return 0, 0, err
	}

	ops := make([]*transaction.Outpoint, len(results))
	for i, r := range results {
		ops[i] = transaction.NewOutpointFromBytes(r.Member)
	}

	sats, err := s.GetSatsBulk(ctx, ops)
	if err != nil {
		return 0, 0, err
	}

	var balance uint64
	for _, sat := range sats {
		balance += sat
	}
	return balance, len(results), nil
}

// filterSpent removes spent outputs from results
func (s *OutputStore) filterSpent(ctx context.Context, results []store.ScoredMember) ([]store.ScoredMember, error) {
	ops := make([]*transaction.Outpoint, len(results))
	for i, r := range results {
		ops[i] = transaction.NewOutpointFromBytes(r.Member)
	}

	spends, err := s.GetSpends(ctx, ops)
	if err != nil {
		return nil, err
	}

	unspent := make([]store.ScoredMember, 0, len(results))
	for i, r := range results {
		if spends[i] == nil {
			unspent = append(unspent, r)
		}
	}
	return unspent, nil
}

// === Load Operations ===

// LoadOutput loads a single output by outpoint
func (s *OutputStore) LoadOutput(ctx context.Context, op *transaction.Outpoint, cfg *OutputSearchCfg) (*IndexedOutput, error) {
	outputs, err := s.LoadOutputs(ctx, []*transaction.Outpoint{op}, cfg)
	if err != nil {
		return nil, err
	}
	if len(outputs) == 0 || outputs[0] == nil {
		return nil, nil
	}
	return outputs[0], nil
}

// LoadOutputsByTxid loads all outputs for a transaction using prefix scan
func (s *OutputStore) LoadOutputsByTxid(ctx context.Context, txid *chainhash.Hash, cfg *OutputSearchCfg) ([]*IndexedOutput, error) {
	// Scan for all h:{txid}* keys
	results, err := s.Store.Scan(ctx, KeyTxidPrefix(txid), 0)
	if err != nil {
		return nil, err
	}

	// Extract unique outpoints from scan results
	// Keys are h:{txid:32}{vout:4} = 38 bytes total
	seen := make(map[uint32]bool)
	var ops []*transaction.Outpoint

	for _, kv := range results {
		if len(kv.Key) < 36 {
			continue
		}
		// Extract vout from key (bytes 32-36 after the h: prefix was stripped by Scan)
		// Actually Scan returns the full key minus the kv: prefix, so we have h:{txid}{vout}
		// The key is 2 + 32 + 4 = 38 bytes
		if len(kv.Key) >= 38 {
			vout := binary.BigEndian.Uint32(kv.Key[34:38])
			if !seen[vout] {
				seen[vout] = true
				ops = append(ops, &transaction.Outpoint{Txid: *txid, Index: vout})
			}
		}
	}

	return s.LoadOutputs(ctx, ops, cfg)
}

// LoadOutputsFromResults loads outputs from search results, including scores
func (s *OutputStore) LoadOutputsFromResults(ctx context.Context, results []store.ScoredMember, cfg *OutputSearchCfg) ([]*IndexedOutput, error) {
	if len(results) == 0 {
		return nil, nil
	}

	// Extract outpoints and scores
	ops := make([]*transaction.Outpoint, len(results))
	scores := make([]float64, len(results))
	for i, r := range results {
		ops[i] = transaction.NewOutpointFromBytes(r.Member)
		scores[i] = r.Score
	}

	return s.loadOutputsWithScores(ctx, ops, scores, cfg)
}

// LoadOutputs loads multiple outputs based on cfg flags
func (s *OutputStore) LoadOutputs(ctx context.Context, ops []*transaction.Outpoint, cfg *OutputSearchCfg) ([]*IndexedOutput, error) {
	// No scores available - pass nil
	return s.loadOutputsWithScores(ctx, ops, nil, cfg)
}

// loadOutputsWithScores loads multiple outputs with optional scores based on cfg flags
func (s *OutputStore) loadOutputsWithScores(ctx context.Context, ops []*transaction.Outpoint, scores []float64, cfg *OutputSearchCfg) ([]*IndexedOutput, error) {
	if len(ops) == 0 {
		return nil, nil
	}

	outputs := make([]*IndexedOutput, len(ops))

	// Bulk load satoshis if requested
	var sats []uint64
	var err error
	if cfg == nil || cfg.IncludeSats {
		sats, err = s.GetSatsBulk(ctx, ops)
		if err != nil {
			return nil, err
		}
	}

	// Bulk load spends if requested
	var spends []*chainhash.Hash
	if cfg != nil && cfg.IncludeSpend {
		spends, err = s.GetSpends(ctx, ops)
		if err != nil {
			return nil, err
		}
	}

	// Determine if we need to load hash data
	needHashData := cfg == nil || cfg.IncludeBlock || cfg.IncludeEvents || len(cfg.IncludeTags) > 0

	for i, op := range ops {
		if op == nil {
			continue
		}

		output := &IndexedOutput{}
		output.Outpoint = *op

		// Set score if available
		if scores != nil && i < len(scores) {
			output.Score = scores[i]
		}

		// Set satoshis if loaded
		if sats != nil {
			output.Satoshis = &sats[i]
		}

		// Set spend if loaded
		if spends != nil {
			output.SpendTxid = spends[i]
		}

		// Load hash data if needed
		if needHashData {
			hashKey := KeyOutHash(op)
			fields, err := s.Store.HGetAll(ctx, hashKey)
			if err != nil {
				return nil, err
			}

			// Parse merkle state if requested
			if (cfg == nil || cfg.IncludeBlock) && len(fields) > 0 {
				if ms, ok := fields[fldMerkle]; ok && len(ms) >= 12 {
					blockHeight := binary.BigEndian.Uint32(ms[0:4])
					blockIdx := binary.BigEndian.Uint64(ms[4:12])
					output.BlockHeight = &blockHeight
					output.BlockIdx = &blockIdx
				}
			}

			// Parse events if requested
			if (cfg == nil || cfg.IncludeEvents) && len(fields) > 0 {
				if ev, ok := fields[fldEvent]; ok {
					if err := json.Unmarshal(ev, &output.Events); err != nil {
						return nil, fmt.Errorf("failed to unmarshal events for %s: %w", op.String(), err)
					}
				}
			}

			// Parse tag data based on cfg.IncludeTags
			if len(fields) > 0 {
				// Check if wildcard "*" is requested or no cfg provided
				loadAllTags := cfg == nil
				if cfg != nil && len(cfg.IncludeTags) > 0 {
					for _, tag := range cfg.IncludeTags {
						if tag == "*" {
							loadAllTags = true
							break
						}
					}
				}

				if loadAllTags {
					// Load all tag data
					output.Data = make(map[string]any)
					for field, data := range fields {
						if len(field) > len(fldData) && field[:len(fldData)] == fldData {
							tag := field[len(fldData):]
							var tagData any
							if err := json.Unmarshal(data, &tagData); err != nil {
								return nil, fmt.Errorf("failed to unmarshal tag %s for %s: %w", tag, op.String(), err)
							}
							output.Data[tag] = tagData
						}
					}
				} else if cfg != nil && len(cfg.IncludeTags) > 0 {
					// Load specific tags
					output.Data = make(map[string]any)
					for _, tag := range cfg.IncludeTags {
						if data, ok := fields[fldData+tag]; ok {
							var tagData any
							if err := json.Unmarshal(data, &tagData); err != nil {
								return nil, fmt.Errorf("failed to unmarshal tag %s for %s: %w", tag, op.String(), err)
							}
							output.Data[tag] = tagData
						}
					}
				}
			}
		}

		outputs[i] = output
	}

	return outputs, nil
}

// === Events ===

// GetEvents returns all events for an outpoint
func (s *OutputStore) GetEvents(ctx context.Context, op *transaction.Outpoint) ([]string, error) {
	eventsBytes, err := s.Store.HGet(ctx, KeyOutHash(op), []byte(fldEvent))
	if err == store.ErrKeyNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var events []string
	err = json.Unmarshal(eventsBytes, &events)
	return events, err
}

// === Rollback ===

// Rollback removes all data for a transaction and recursively rolls back
// any transactions that spent its outputs. Order of operations:
// 1. Recursively rollback downstream spenders
// 2. Un-spend inputs this transaction consumed
// 3. Delete event indices
// 4. Delete hash field data
// 5. Delete output references
func (s *OutputStore) Rollback(ctx context.Context, txid *chainhash.Hash) error {
	outputs, err := s.LoadOutputsByTxid(ctx, txid, nil)
	if err != nil {
		return fmt.Errorf("failed to load outputs for %s: %w", txid.String(), err)
	}

	// Collect inputs consumed by this transaction (from in:* fields)
	inputsToUnspend := make(map[string]*transaction.Outpoint)

	for _, output := range outputs {
		if output == nil {
			continue
		}
		hashKey := KeyOutHash(&output.Outpoint)
		fields, err := s.Store.HGetAll(ctx, hashKey)
		if err != nil {
			return fmt.Errorf("failed to get fields for %s: %w", output.Outpoint.String(), err)
		}
		for field, data := range fields {
			if len(field) > len(fldInputs) && field[:len(fldInputs)] == fldInputs {
				for i := 0; i+36 <= len(data); i += 36 {
					input := transaction.NewOutpointFromBytes(data[i : i+36])
					if input != nil {
						inputsToUnspend[input.String()] = input
					}
				}
			}
		}
	}

	// 1. Recursively rollback any transactions that spent our outputs
	for _, output := range outputs {
		if output == nil {
			continue
		}
		spendTxid, err := s.GetSpend(ctx, &output.Outpoint)
		if err != nil {
			return fmt.Errorf("failed to get spend for %s: %w", output.Outpoint.String(), err)
		}
		if spendTxid != nil {
			if err := s.Rollback(ctx, spendTxid); err != nil {
				return fmt.Errorf("failed to rollback spending tx %s: %w", spendTxid.String(), err)
			}
		}
	}

	// 2. Un-spend the inputs this transaction consumed
	for _, input := range inputsToUnspend {
		// Remove spend marker
		if err := s.Store.HDel(ctx, hashSpnd, input.Bytes()); err != nil {
			return fmt.Errorf("failed to un-spend %s: %w", input.String(), err)
		}
		// Remove from spent event indices
		events, err := s.GetEvents(ctx, input)
		if err != nil {
			return fmt.Errorf("failed to get events for input %s: %w", input.String(), err)
		}
		for _, event := range events {
			if err := s.Store.ZRem(ctx, KeyEventSpent(event), input.Bytes()); err != nil {
				return fmt.Errorf("failed to remove from spent index %s: %w", event, err)
			}
		}
	}

	// 3-5. Delete our event indices, hash fields, and references
	for _, output := range outputs {
		if output == nil {
			continue
		}

		op := &output.Outpoint
		opBytes := op.Bytes()
		hashKey := KeyOutHash(op)

		// 3. Delete event index entries
		events, err := s.GetEvents(ctx, op)
		if err != nil {
			return fmt.Errorf("failed to get events for %s: %w", op.String(), err)
		}
		for _, event := range events {
			if err := s.Store.ZRem(ctx, KeyEvent(event), opBytes); err != nil {
				return fmt.Errorf("failed to remove event %s: %w", event, err)
			}
			if err := s.Store.ZRem(ctx, KeyEventSpent(event), opBytes); err != nil {
				return fmt.Errorf("failed to remove event spend %s: %w", event, err)
			}
		}

		// 4. Delete all hash fields
		fields, err := s.Store.HGetAll(ctx, hashKey)
		if err != nil {
			return fmt.Errorf("failed to get hash fields for %s: %w", op.String(), err)
		}
		for field := range fields {
			if err := s.Store.HDel(ctx, hashKey, []byte(field)); err != nil {
				return fmt.Errorf("failed to delete hash field %s: %w", field, err)
			}
		}

		// 5. Remove from bulk lookup hashes (output references - last)
		if err := s.Store.HDel(ctx, hashSpnd, opBytes); err != nil {
			return fmt.Errorf("failed to remove spend reference for %s: %w", op.String(), err)
		}
		if err := s.Store.HDel(ctx, hashSats, opBytes); err != nil {
			return fmt.Errorf("failed to remove sats reference for %s: %w", op.String(), err)
		}
	}

	return nil
}

// === Logging (for indexer) ===

// Log adds a member to a sorted set with score
func (s *OutputStore) Log(ctx context.Context, key string, member []byte, score float64) error {
	return s.Store.ZAdd(ctx, KeyEvent(key), store.ScoredMember{
		Member: member,
		Score:  score,
	})
}

// LogScore returns the score for a member in a sorted set
func (s *OutputStore) LogScore(ctx context.Context, key string, member []byte) (float64, error) {
	return s.Store.ZScore(ctx, KeyEvent(key), member)
}

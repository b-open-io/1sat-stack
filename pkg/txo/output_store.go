package txo

import (
	"context"
	"encoding/binary"
	"encoding/json"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/pubsub"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// Key prefixes
const (
	pfxHash = "h:"  // Hash keys
	pfxZSet = "z:"  // Sorted set keys
	pfxTopic = "tp:" // Topic prefix within ZSet
)

// Bulk lookup hash keys
var (
	hashSats = []byte(pfxHash + "sats") // h:sats → {outpoint:36} → satoshis (uint64)
	hashSpnd = []byte(pfxHash + "spnd") // h:spnd → {outpoint:36} → spend txid (32 bytes)
)

// Hash field prefixes (within h:{outpoint})
const (
	fldEvent  = "ev"  // events (JSON string array)
	fldMerkle = "ms"  // merkle state (binary)
	fldData   = "dt:" // prefix for dt:{tag}
	fldDeps   = "dp:" // prefix for dp:{topic} - ancillary txids
	fldInputs = "in:" // prefix for in:{topic} - inputs consumed
)

// === Key Builders (binary) ===

// outpointBytes converts an outpoint to 36 bytes (32 byte txid + 4 byte vout BE)
func outpointBytes(op *transaction.Outpoint) []byte {
	b := make([]byte, 36)
	copy(b, op.Txid[:])
	binary.BigEndian.PutUint32(b[32:], op.Index)
	return b
}

// outpointFromBytes parses 36 bytes back to an outpoint
func outpointFromBytes(b []byte) *transaction.Outpoint {
	if len(b) != 36 {
		return nil
	}
	op := &transaction.Outpoint{}
	copy(op.Txid[:], b[:32])
	op.Index = binary.BigEndian.Uint32(b[32:])
	return op
}

// keyOutHash builds the hash key for an outpoint: h:{outpoint:36}
func keyOutHash(op *transaction.Outpoint) []byte {
	key := make([]byte, 2+36) // "h:" + 36 byte outpoint
	copy(key, pfxHash)
	copy(key[2:], outpointBytes(op))
	return key
}

// keyTxidPrefix builds prefix for scanning all outputs of a txid: h:{txid:32}
func keyTxidPrefix(txid *chainhash.Hash) []byte {
	key := make([]byte, 2+32) // "h:" + 32 byte txid
	copy(key, pfxHash)
	copy(key[2:], txid[:])
	return key
}

// keyEvent builds ZSet key for an event: z:{event}
func keyEvent(event string) []byte {
	return []byte(pfxZSet + event)
}

// keyEventSpnd builds ZSet key for spent event: z:{event}:spnd
func keyEventSpnd(event string) []byte {
	return []byte(pfxZSet + event + ":spnd")
}

// keyTopicOut builds ZSet key for topic outputs: z:tp:{topic}
func keyTopicOut(topic string) []byte {
	return []byte(pfxZSet + pfxTopic + topic)
}

// keyTopicTx builds ZSet key for topic applied txids: z:tp:{topic}:tx
func keyTopicTx(topic string) []byte {
	return []byte(pfxZSet + pfxTopic + topic + ":tx")
}

// keyPeerInteraction builds hash key for peer interactions: h:pi:{topic}
func keyPeerInteraction(topic string) []byte {
	return []byte(pfxHash + "pi:" + topic)
}

// === OutputStore ===

// OutputStore provides global output storage.
// Output data is keyed by binary outpoint for locality.
// Spends and satoshis use hashes for efficient bulk lookups.
type OutputStore struct {
	Store     store.Store
	PubSub    pubsub.PubSub
	BeefStore *beef.Storage
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
	opBytes := outpointBytes(op)
	hashKey := keyOutHash(op)

	// Build events list - include txid event and owner events
	events := make([]string, 0, len(output.Events)+len(output.Owners)+1)
	events = append(events, "txid:"+op.Txid.String())
	events = append(events, output.Events...)
	for _, owner := range output.Owners {
		if !owner.IsZero() {
			events = append(events, "own:"+owner.String())
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
	if output.BlockHeight > 0 {
		ms := make([]byte, 12) // height(4) + idx(8)
		binary.BigEndian.PutUint32(ms[0:4], output.BlockHeight)
		binary.BigEndian.PutUint64(ms[4:12], output.BlockIdx)
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
		if err := s.Store.ZAdd(ctx, keyEvent(event), store.ScoredMember{
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
	opBytes := outpointBytes(op)
	hashKey := keyOutHash(op)

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
		if err := s.Store.ZAdd(ctx, keyEvent(event), store.ScoredMember{
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

// SaveDeps saves dependency txids for an output in a specific topic
func (s *OutputStore) SaveDeps(ctx context.Context, op *transaction.Outpoint, topic string, txids []*chainhash.Hash) error {
	if len(txids) == 0 {
		return nil
	}

	// Store as concatenated 32-byte txids
	data := make([]byte, 32*len(txids))
	for i, txid := range txids {
		copy(data[i*32:], txid[:])
	}

	return s.Store.HSet(ctx, keyOutHash(op), []byte(fldDeps+topic), data)
}

// SaveInputsConsumed saves the inputs consumed by this output for a topic
func (s *OutputStore) SaveInputsConsumed(ctx context.Context, op *transaction.Outpoint, topic string, inputs []*transaction.Outpoint) error {
	if len(inputs) == 0 {
		return nil
	}

	// Store as concatenated 36-byte outpoints
	data := make([]byte, 36*len(inputs))
	for i, input := range inputs {
		copy(data[i*36:], outpointBytes(input))
	}

	return s.Store.HSet(ctx, keyOutHash(op), []byte(fldInputs+topic), data)
}

// === Spend Operations ===

// SaveSpend marks an output as spent and updates spent indexes
func (s *OutputStore) SaveSpend(ctx context.Context, op *transaction.Outpoint, spendTxid *chainhash.Hash, score float64) error {
	opBytes := outpointBytes(op)

	// Store spend txid in bulk lookup hash
	if err := s.Store.HSet(ctx, hashSpnd, opBytes, spendTxid[:]); err != nil {
		return err
	}

	// Get events for this output to update spent indexes
	eventsBytes, err := s.Store.HGet(ctx, keyOutHash(op), []byte(fldEvent))
	if err != nil && err != store.ErrKeyNotFound {
		return err
	}

	if len(eventsBytes) > 0 {
		var events []string
		if err := json.Unmarshal(eventsBytes, &events); err == nil {
			for _, event := range events {
				s.Store.ZAdd(ctx, keyEventSpnd(event), store.ScoredMember{
					Member: opBytes,
					Score:  score,
				})
			}
		}
	}

	return nil
}

// GetSpend returns the spending txid for an outpoint (nil if unspent)
func (s *OutputStore) GetSpend(ctx context.Context, op *transaction.Outpoint) (*chainhash.Hash, error) {
	spendBytes, err := s.Store.HGet(ctx, hashSpnd, outpointBytes(op))
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
		fields[i] = outpointBytes(op)
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
	value, err := s.Store.HGet(ctx, hashSats, outpointBytes(op))
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
		fields[i] = outpointBytes(op)
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

// Search performs a multi-key search with optional spent filtering
func (s *OutputStore) Search(ctx context.Context, cfg *OutputSearchCfg) ([]store.ScoredMember, error) {
	// Prefix all keys with z:
	prefixedCfg := cfg.SearchCfg
	prefixedCfg.Keys = make([][]byte, len(cfg.Keys))
	for i, k := range cfg.Keys {
		prefixedCfg.Keys[i] = keyEvent(string(k))
	}

	results, err := s.Store.Search(ctx, &prefixedCfg)
	if err != nil {
		return nil, err
	}

	if cfg.FilterSpent {
		return s.filterSpent(ctx, results)
	}
	return results, nil
}

// SearchOutputs searches and loads full output data
func (s *OutputStore) SearchOutputs(ctx context.Context, cfg *OutputSearchCfg) ([]*IndexedOutput, error) {
	results, err := s.Search(ctx, cfg)
	if err != nil {
		return nil, err
	}

	ops := make([]*transaction.Outpoint, len(results))
	for i, r := range results {
		ops[i] = outpointFromBytes(r.Member)
	}
	return s.loadOutputs(ctx, ops, cfg)
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
		ops[i] = outpointFromBytes(r.Member)
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
		ops[i] = outpointFromBytes(r.Member)
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
	outputs, err := s.loadOutputs(ctx, []*transaction.Outpoint{op}, cfg)
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
	results, err := s.Store.Scan(ctx, keyTxidPrefix(txid), 0)
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

	return s.loadOutputs(ctx, ops, cfg)
}

// loadOutputs loads multiple outputs with their data based on cfg
func (s *OutputStore) loadOutputs(ctx context.Context, ops []*transaction.Outpoint, cfg *OutputSearchCfg) ([]*IndexedOutput, error) {
	if len(ops) == 0 {
		return nil, nil
	}

	outputs := make([]*IndexedOutput, len(ops))

	for i, op := range ops {
		if op == nil {
			continue
		}

		hashKey := keyOutHash(op)

		// Get all hash fields for this output
		fields, err := s.Store.HGetAll(ctx, hashKey)
		if err != nil {
			return nil, err
		}
		if len(fields) == 0 {
			continue
		}

		output := &IndexedOutput{
			Data: make(map[string]any),
		}
		output.Outpoint = *op

		// Parse merkle state
		if ms, ok := fields[fldMerkle]; ok && len(ms) >= 12 {
			output.BlockHeight = binary.BigEndian.Uint32(ms[0:4])
			output.BlockIdx = binary.BigEndian.Uint64(ms[4:12])
		}

		// Parse events
		if ev, ok := fields[fldEvent]; ok {
			json.Unmarshal(ev, &output.Events)
		}

		// Parse tag data based on cfg.IncludeTags
		if cfg != nil && len(cfg.IncludeTags) > 0 {
			for _, tag := range cfg.IncludeTags {
				if data, ok := fields[fldData+tag]; ok {
					var tagData any
					if err := json.Unmarshal(data, &tagData); err == nil {
						output.Data[tag] = tagData
					}
				}
			}
		} else if cfg == nil {
			// Load all tag data if no cfg provided
			for field, data := range fields {
				if len(field) > len(fldData) && field[:len(fldData)] == fldData {
					tag := field[len(fldData):]
					var tagData any
					if err := json.Unmarshal(data, &tagData); err == nil {
						output.Data[tag] = tagData
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
	eventsBytes, err := s.Store.HGet(ctx, keyOutHash(op), []byte(fldEvent))
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

// === Dependencies ===

// GetDeps returns dependency txids for an output in a specific topic
func (s *OutputStore) GetDeps(ctx context.Context, op *transaction.Outpoint, topic string) ([]*chainhash.Hash, error) {
	data, err := s.Store.HGet(ctx, keyOutHash(op), []byte(fldDeps+topic))
	if err == store.ErrKeyNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if len(data)%32 != 0 {
		return nil, nil
	}

	txids := make([]*chainhash.Hash, len(data)/32)
	for i := range txids {
		txids[i] = &chainhash.Hash{}
		copy(txids[i][:], data[i*32:(i+1)*32])
	}
	return txids, nil
}

// GetInputsConsumed returns the inputs consumed by this output for a topic
func (s *OutputStore) GetInputsConsumed(ctx context.Context, op *transaction.Outpoint, topic string) ([]*transaction.Outpoint, error) {
	data, err := s.Store.HGet(ctx, keyOutHash(op), []byte(fldInputs+topic))
	if err == store.ErrKeyNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if len(data)%36 != 0 {
		return nil, nil
	}

	inputs := make([]*transaction.Outpoint, len(data)/36)
	for i := range inputs {
		inputs[i] = outpointFromBytes(data[i*36 : (i+1)*36])
	}
	return inputs, nil
}

// === Topic Operations ===

// AddToTopic adds an output to a topic sorted set
func (s *OutputStore) AddToTopic(ctx context.Context, op *transaction.Outpoint, topic string, score float64) error {
	return s.Store.ZAdd(ctx, keyTopicOut(topic), store.ScoredMember{
		Member: outpointBytes(op),
		Score:  score,
	})
}

// AddTxToTopic adds a txid to a topic's applied transactions
func (s *OutputStore) AddTxToTopic(ctx context.Context, txid *chainhash.Hash, topic string, score float64) error {
	return s.Store.ZAdd(ctx, keyTopicTx(topic), store.ScoredMember{
		Member: txid[:],
		Score:  score,
	})
}

// IsTxInTopic checks if a txid is in a topic's applied transactions
func (s *OutputStore) IsTxInTopic(ctx context.Context, txid *chainhash.Hash, topic string) (bool, error) {
	_, err := s.Store.ZScore(ctx, keyTopicTx(topic), txid[:])
	if err == store.ErrKeyNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// === Rollback ===

// Rollback removes all data for a transaction
func (s *OutputStore) Rollback(ctx context.Context, txid *chainhash.Hash) error {
	outputs, err := s.LoadOutputsByTxid(ctx, txid, nil)
	if err != nil {
		return err
	}

	for _, output := range outputs {
		if output == nil {
			continue
		}

		op := &output.Outpoint
		opBytes := outpointBytes(op)

		// Get events to remove from sorted sets
		events, _ := s.GetEvents(ctx, op)
		for _, event := range events {
			s.Store.ZRem(ctx, keyEvent(event), opBytes)
			s.Store.ZRem(ctx, keyEventSpnd(event), opBytes)
		}

		// Delete all hash fields via scan (we need to delete the hash key itself)
		// Since we're using HSet, we need to get all fields and delete them
		hashKey := keyOutHash(op)
		fields, _ := s.Store.HGetAll(ctx, hashKey)
		for field := range fields {
			s.Store.HDel(ctx, hashKey, []byte(field))
		}

		// Remove from bulk lookup hashes
		s.Store.HDel(ctx, hashSpnd, opBytes)
		s.Store.HDel(ctx, hashSats, opBytes)
	}

	return nil
}

// === Logging (for indexer) ===

// Log adds a member to a sorted set with score
func (s *OutputStore) Log(ctx context.Context, key string, member []byte, score float64) error {
	return s.Store.ZAdd(ctx, keyEvent(key), store.ScoredMember{
		Member: member,
		Score:  score,
	})
}

// LogScore returns the score for a member in a sorted set
func (s *OutputStore) LogScore(ctx context.Context, key string, member []byte) (float64, error) {
	return s.Store.ZScore(ctx, keyEvent(key), member)
}

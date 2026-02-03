package lookup

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"

	"github.com/b-open-io/1sat-stack/pkg/parse"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bitcoin-sv/go-templates/template/bsv21"
	"github.com/bitcoin-sv/go-templates/template/cosign"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/overlay/lookup"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/template/p2pkh"
)

// BSV21Lookup implements the LookupService interface for BSV21
// Uses dedicated ZSets for topic-specific lookups with authoritative UTXO tracking
type BSV21Lookup struct {
	storage   *txo.OutputStore
	mintCache sync.Map // Cache of mint tokens by tokenId
}

// NewBSV21Lookup creates a new BSV21 lookup service
func NewBSV21Lookup(storage *txo.OutputStore) *BSV21Lookup {
	return &BSV21Lookup{
		storage: storage,
	}
}

// KeyBSV21 generates a ZSet key for BSV21 unspent lookups: tm_{tokenId}:{lockType}:{address}
func KeyBSV21(tokenId, lockType, address string) []byte {
	return []byte(fmt.Sprintf("tm_%s:%s:%s", tokenId, lockType, address))
}

// KeyBSV21History generates a ZSet key for BSV21 history lookups: tm_{tokenId}:{lockType}:{address}:h
// Unlike the main ZSet, entries are never removed (for historical queries)
func KeyBSV21History(tokenId, lockType, address string) []byte {
	return []byte(fmt.Sprintf("tm_%s:%s:%s:h", tokenId, lockType, address))
}

// OutputAdmittedByTopic is called when an output is admitted to a topic
// Adds the output to topic-specific ZSets for efficient lookups
func (l *BSV21Lookup) OutputAdmittedByTopic(ctx context.Context, payload *engine.OutputAdmittedByTopic) error {
	_, tx, txid, err := transaction.ParseBeef(payload.AtomicBEEF)
	if err != nil {
		return err
	}

	outpoint := &transaction.Outpoint{
		Txid:  *txid,
		Index: payload.OutputIndex,
	}

	// Decode BSV21 data
	b := bsv21.Decode(tx.Outputs[int(payload.OutputIndex)].LockingScript)
	if b == nil {
		return nil
	}

	// For deploy operations, set the ID to the ordinal string
	if b.Op == string(bsv21.OpDeployMint) || b.Op == string(bsv21.OpDeployAuth) {
		b.Id = outpoint.OrdinalString()
	}

	// Extract score from transaction (block height if confirmed, timestamp if not)
	score := types.ScoreFromTx(tx, txid)
	opBytes := outpoint.Bytes()

	// Helper to add to both UTXO and history ZSets
	member := store.ScoredMember{Member: opBytes, Score: score}
	addToZSets := func(tokenId, lockType, address string) error {
		// Add to authoritative UTXO set (will be removed on spend)
		if err := l.storage.Store.ZAdd(ctx, KeyBSV21(tokenId, lockType, address), member); err != nil {
			return err
		}
		// Add to history set (never removed)
		return l.storage.Store.ZAdd(ctx, KeyBSV21History(tokenId, lockType, address), member)
	}

	// Extract lock type and address from suffix script, add to appropriate ZSets
	// Only index p2pkh and cosign - other lock types handled by general indexer
	suffix := script.NewFromBytes(b.Insc.ScriptSuffix)
	if p := p2pkh.Decode(suffix, true); p != nil {
		if err := addToZSets(b.Id, "p2pkh", p.AddressString); err != nil {
			return err
		}
	} else if c := cosign.Decode(suffix); c != nil {
		if err := addToZSets(b.Id, "cos", c.Address); err != nil {
			return err
		}
	}

	return nil
}

// GetDocumentation returns documentation for this lookup service
func (l *BSV21Lookup) GetDocumentation() string {
	return "BSV21 Lookup Service"
}

// GetMetaData returns metadata for this lookup service
func (l *BSV21Lookup) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{
		Name: "BSV21",
	}
}

// OutputSpent is called when a previously-admitted UTXO is spent
// Removes the output from topic-specific ZSets (authoritative UTXO tracking)
func (l *BSV21Lookup) OutputSpent(ctx context.Context, payload *engine.OutputSpent) error {
	_, tx, _, err := transaction.ParseBeef(payload.SpendingAtomicBEEF)
	if err != nil {
		return err
	}

	if int(payload.InputIndex) >= len(tx.Inputs) {
		return nil
	}

	input := tx.Inputs[payload.InputIndex]
	if input.SourceTransaction == nil {
		return nil
	}

	if int(payload.Outpoint.Index) >= len(input.SourceTransaction.Outputs) {
		return nil
	}

	spentOutput := input.SourceTransaction.Outputs[payload.Outpoint.Index]

	// Decode BSV21 data from spent output
	b := bsv21.Decode(spentOutput.LockingScript)
	if b == nil {
		return nil
	}

	opBytes := payload.Outpoint.Bytes()

	// Remove from the appropriate ZSet based on lock type
	// Only handle p2pkh and cosign - other lock types handled by general indexer
	suffix := script.NewFromBytes(b.Insc.ScriptSuffix)
	if p := p2pkh.Decode(suffix, true); p != nil {
		l.storage.Store.ZRem(ctx, KeyBSV21(b.Id, "p2pkh", p.AddressString), opBytes)
	} else if c := cosign.Decode(suffix); c != nil {
		l.storage.Store.ZRem(ctx, KeyBSV21(b.Id, "cos", c.Address), opBytes)
	}

	return nil
}

// OutputNoLongerRetainedInHistory is called when historical retention is no longer required
func (l *BSV21Lookup) OutputNoLongerRetainedInHistory(ctx context.Context, outpoint *transaction.Outpoint, topic string) error {
	return nil
}

// OutputEvicted permanently removes the UTXO from all indices
func (l *BSV21Lookup) OutputEvicted(ctx context.Context, outpoint *transaction.Outpoint) error {
	return nil
}

// OutputBlockHeightUpdated is called when a transaction's block height is updated
func (l *BSV21Lookup) OutputBlockHeightUpdated(ctx context.Context, txid *chainhash.Hash, blockHeight uint32, blockIndex uint64) error {
	return nil
}

// Lookup handles generic lookup queries
func (l *BSV21Lookup) Lookup(ctx context.Context, question *lookup.LookupQuestion) (*lookup.LookupAnswer, error) {
	return &lookup.LookupAnswer{
		Type: lookup.AnswerTypeFormula,
	}, nil
}

// SearchUTXOs searches for unspent outputs in a topic-specific ZSet
// Returns outpoints from the ZSet (already filtered for unspent since ZRem is called on spend)
func (l *BSV21Lookup) SearchUTXOs(ctx context.Context, tokenId, lockType, address string, cfg *store.SearchCfg) ([]*transaction.Outpoint, error) {
	key := KeyBSV21(tokenId, lockType, address)

	searchCfg := &store.SearchCfg{
		Keys:    [][]byte{key},
		Limit:   cfg.Limit,
		Reverse: cfg.Reverse,
	}
	if cfg.From != nil {
		searchCfg.From = cfg.From
	}
	if cfg.To != nil {
		searchCfg.To = cfg.To
	}

	results, err := l.storage.Store.Search(ctx, searchCfg)
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

// SearchMultiUTXOs searches for unspent outputs across multiple addresses
func (l *BSV21Lookup) SearchMultiUTXOs(ctx context.Context, tokenId, lockType string, addresses []string, cfg *store.SearchCfg) ([]*transaction.Outpoint, error) {
	keys := make([][]byte, len(addresses))
	for i, addr := range addresses {
		keys[i] = KeyBSV21(tokenId, lockType, addr)
	}

	searchCfg := &store.SearchCfg{
		Keys:     keys,
		Limit:    cfg.Limit,
		Reverse:  cfg.Reverse,
		JoinType: store.JoinUnion,
	}
	if cfg.From != nil {
		searchCfg.From = cfg.From
	}
	if cfg.To != nil {
		searchCfg.To = cfg.To
	}

	results, err := l.storage.Store.Search(ctx, searchCfg)
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

// SearchHistory searches for all outputs (including spent) in the history ZSet
func (l *BSV21Lookup) SearchHistory(ctx context.Context, tokenId, lockType, address string, cfg *store.SearchCfg) ([]*transaction.Outpoint, error) {
	key := KeyBSV21History(tokenId, lockType, address)

	searchCfg := &store.SearchCfg{
		Keys:    [][]byte{key},
		Limit:   cfg.Limit,
		Reverse: cfg.Reverse,
	}
	if cfg.From != nil {
		searchCfg.From = cfg.From
	}
	if cfg.To != nil {
		searchCfg.To = cfg.To
	}

	results, err := l.storage.Store.Search(ctx, searchCfg)
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

// SearchMultiHistory searches history across multiple addresses
func (l *BSV21Lookup) SearchMultiHistory(ctx context.Context, tokenId, lockType string, addresses []string, cfg *store.SearchCfg) ([]*transaction.Outpoint, error) {
	keys := make([][]byte, len(addresses))
	for i, addr := range addresses {
		keys[i] = KeyBSV21History(tokenId, lockType, addr)
	}

	searchCfg := &store.SearchCfg{
		Keys:     keys,
		Limit:    cfg.Limit,
		Reverse:  cfg.Reverse,
		JoinType: store.JoinUnion,
	}
	if cfg.From != nil {
		searchCfg.From = cfg.From
	}
	if cfg.To != nil {
		searchCfg.To = cfg.To
	}

	results, err := l.storage.Store.Search(ctx, searchCfg)
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

// GetBalance calculates the total balance of BSV21 tokens for a given address
// Uses the authoritative ZSet (already unspent) and loads amounts from main store
func (l *BSV21Lookup) GetBalance(ctx context.Context, tokenId, lockType, address string) (uint64, int, error) {
	outpoints, err := l.SearchUTXOs(ctx, tokenId, lockType, address, &store.SearchCfg{})
	if err != nil {
		return 0, 0, err
	}

	return l.sumAmounts(ctx, outpoints)
}

// GetMultiBalance calculates the total balance across multiple addresses
func (l *BSV21Lookup) GetMultiBalance(ctx context.Context, tokenId, lockType string, addresses []string) (uint64, int, error) {
	outpoints, err := l.SearchMultiUTXOs(ctx, tokenId, lockType, addresses, &store.SearchCfg{})
	if err != nil {
		return 0, 0, err
	}

	return l.sumAmounts(ctx, outpoints)
}

// sumAmounts loads BSV21 amounts from main store and sums them
func (l *BSV21Lookup) sumAmounts(ctx context.Context, outpoints []*transaction.Outpoint) (uint64, int, error) {
	if len(outpoints) == 0 {
		return 0, 0, nil
	}

	cfg := &txo.OutputSearchCfg{
		IncludeTags: []string{"bsv21"},
	}

	outputs, err := l.storage.LoadOutputs(ctx, outpoints, cfg)
	if err != nil {
		return 0, 0, err
	}

	var totalBalance uint64
	count := 0

	for _, output := range outputs {
		if output == nil || output.Data == nil {
			continue
		}

		dataMap, ok := output.Data["bsv21"]
		if !ok {
			continue
		}

		bsv21Data, ok := dataMap.(map[string]any)
		if !ok {
			continue
		}

		amtStr, ok := bsv21Data["amt"].(string)
		if !ok {
			continue
		}

		amt, err := strconv.ParseUint(amtStr, 10, 64)
		if err != nil {
			continue
		}

		totalBalance += amt
		count++
	}

	return totalBalance, count, nil
}

// GetToken returns the parsed BSV21 deploy data for a specific token
func (l *BSV21Lookup) GetToken(ctx context.Context, outpoint *transaction.Outpoint) (*parse.BSV21, error) {
	tokenId := outpoint.OrdinalString()

	// Check cache first
	if cached, ok := l.mintCache.Load(tokenId); ok {
		return cached.(*parse.BSV21), nil
	}

	// Load output data for this outpoint
	cfg := &txo.OutputSearchCfg{
		IncludeTags: []string{"bsv21"},
	}
	output, err := l.storage.LoadOutput(ctx, outpoint, cfg)
	if err != nil {
		return nil, err
	}

	if output == nil {
		return nil, fmt.Errorf("token not found")
	}

	bsv21DataRaw, ok := output.Data["bsv21"]
	if !ok {
		return nil, fmt.Errorf("token not found")
	}

	// Re-marshal and unmarshal to hydrate into the typed struct
	dataBytes, err := json.Marshal(bsv21DataRaw)
	if err != nil {
		return nil, fmt.Errorf("invalid token data: %w", err)
	}

	token := &parse.BSV21{}
	if err := json.Unmarshal(dataBytes, token); err != nil {
		return nil, fmt.Errorf("invalid token data: %w", err)
	}

	// Ensure this is a deploy operation
	if token.Op != string(bsv21.OpDeployMint) && token.Op != string(bsv21.OpDeployAuth) {
		return nil, fmt.Errorf("outpoint is not a deploy transaction (op=%s)", token.Op)
	}

	// Set the ID to the canonical outpoint string
	token.Id = tokenId

	// Cache the result
	l.mintCache.Store(tokenId, token)

	return token, nil
}

// LoadOutputs loads full output data for a list of outpoints
func (l *BSV21Lookup) LoadOutputs(ctx context.Context, outpoints []*transaction.Outpoint) ([]*txo.IndexedOutput, error) {
	cfg := &txo.OutputSearchCfg{
		IncludeTags: []string{"bsv21"},
	}
	return l.storage.LoadOutputs(ctx, outpoints, cfg)
}

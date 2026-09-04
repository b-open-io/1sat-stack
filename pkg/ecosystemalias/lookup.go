package ecosystemalias

import (
	"context"
	"fmt"

	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	overlaylookup "github.com/bsv-blockchain/go-sdk/overlay/lookup"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// OutputLoader is the narrow part of overlay storage needed to turn stored
// claim outpoints into standard BRC-24 output-list entries.
type OutputLoader interface {
	FindOutput(ctx context.Context, outpoint *transaction.Outpoint, topic *string, spent *bool, includeBEEF bool) (*engine.Output, error)
	LoadAncillaryBeef(ctx context.Context, output *engine.Output) error
}

// LookupService implements ls_ecosystemalias.
type LookupService struct {
	store   ClaimStore
	outputs OutputLoader
}

// NewLookupService creates the ecosystem-alias lookup service.
func NewLookupService(store ClaimStore, outputs OutputLoader) *LookupService {
	return &LookupService{store: store, outputs: outputs}
}

// OutputAdmittedByTopic validates and stores a newly admitted claim. A
// structurally reliable BRC-74 level-zero txid leaf supplies the explicit
// confirmation placement; otherwise the claim remains unconfirmed until an
// OutputBlockHeightUpdated notification supplies placement coordinates.
func (l *LookupService) OutputAdmittedByTopic(ctx context.Context, payload *engine.OutputAdmittedByTopic) error {
	if payload == nil {
		return fmt.Errorf("ecosystem-alias admission payload must not be nil")
	}
	if payload.Topic != TopicName {
		return nil
	}
	if l.store == nil {
		return fmt.Errorf("ecosystem-alias claim store is not configured")
	}

	beef, txid, err := transaction.NewBeefFromAtomicBytes(payload.AtomicBEEF)
	if err != nil {
		return fmt.Errorf("failed to parse ecosystem-alias Atomic BEEF: %w", err)
	}
	tx := beef.FindTransactionByHash(txid)
	if tx == nil {
		return fmt.Errorf("ecosystem-alias Atomic BEEF does not contain subject transaction %s", txid.String())
	}
	if uint64(payload.OutputIndex) >= uint64(len(tx.Outputs)) {
		return fmt.Errorf("ecosystem-alias output index %d is out of range for transaction %s with %d outputs", payload.OutputIndex, txid.String(), len(tx.Outputs))
	}

	claim, err := Decode(tx.Outputs[payload.OutputIndex].LockingScript, tx.Outputs[payload.OutputIndex].Satoshis)
	if err != nil {
		return fmt.Errorf("failed to decode ecosystem-alias output %s.%d: %w", txid.String(), payload.OutputIndex, err)
	}
	confirmed, height, blockIndex := merklePlacement(tx.MerklePath, txid)
	return l.store.UpsertClaim(ctx, &StoredClaim{
		Outpoint: transaction.Outpoint{
			Txid:  *txid,
			Index: payload.OutputIndex,
		},
		Alias:       claim.Alias,
		Domain:      claim.Domain,
		Confirmed:   confirmed,
		BlockHeight: height,
		BlockIndex:  blockIndex,
	})
}

// merklePlacement only trusts a unique level-zero leaf explicitly marked as
// a relevant txid. A present but ambiguous/incomplete path is treated as
// unconfirmed instead of inventing an ordering score.
func merklePlacement(path *transaction.MerklePath, txid *chainhash.Hash) (confirmed bool, height uint32, blockIndex uint64) {
	if path == nil || txid == nil || len(path.Path) == 0 {
		return false, 0, 0
	}

	found := false
	for _, leaf := range path.Path[0] {
		if leaf == nil || leaf.Hash == nil || leaf.Txid == nil || !*leaf.Txid || !leaf.Hash.Equal(*txid) {
			continue
		}
		if found {
			return false, 0, 0
		}
		found = true
		blockIndex = leaf.Offset
	}
	if !found {
		return false, 0, 0
	}
	return true, path.BlockHeight, blockIndex
}

// OutputSpent records the spender so the claim is immediately excluded from
// subsequent queries.
func (l *LookupService) OutputSpent(ctx context.Context, payload *engine.OutputSpent) error {
	if payload == nil {
		return fmt.Errorf("ecosystem-alias spend payload must not be nil")
	}
	if payload.Topic != TopicName {
		return nil
	}
	if l.store == nil {
		return fmt.Errorf("ecosystem-alias claim store is not configured")
	}
	return l.store.MarkSpent(ctx, payload.Outpoint, payload.SpendingTxid)
}

// OutputNoLongerRetainedInHistory removes state once this exact topic no
// longer retains the output.
func (l *LookupService) OutputNoLongerRetainedInHistory(ctx context.Context, outpoint *transaction.Outpoint, topic string) error {
	if topic != TopicName {
		return nil
	}
	if l.store == nil {
		return fmt.Errorf("ecosystem-alias claim store is not configured")
	}
	return l.store.DeleteOutpoint(ctx, outpoint)
}

// OutputEvicted has no topic in the engine callback. Check the exact
// ecosystem-alias topic before deleting: if that topic still retains the
// outpoint, the eviction belonged to another topic and must be ignored.
func (l *LookupService) OutputEvicted(ctx context.Context, outpoint *transaction.Outpoint) error {
	if outpoint == nil {
		return fmt.Errorf("ecosystem-alias evicted outpoint must not be nil")
	}
	if l.store == nil {
		return fmt.Errorf("ecosystem-alias claim store is not configured")
	}
	if l.outputs == nil {
		return fmt.Errorf("ecosystem-alias output loader is not configured")
	}
	topic := TopicName
	output, err := l.outputs.FindOutput(ctx, outpoint, &topic, nil, false)
	if err != nil {
		return fmt.Errorf("failed to check ecosystem-alias eviction for %s: %w", outpointString(outpoint), err)
	}
	if output != nil {
		return nil
	}
	return l.store.DeleteOutpoint(ctx, outpoint)
}

// OutputBlockHeightUpdated stores explicit placement. The engine callback has
// no topic or confirmation boolean, so height zero is the engine's unconfirmed
// signal; the supplied coordinates are preserved exactly.
func (l *LookupService) OutputBlockHeightUpdated(ctx context.Context, txid *chainhash.Hash, blockHeight uint32, blockIndex uint64) error {
	if l.store == nil {
		return fmt.Errorf("ecosystem-alias claim store is not configured")
	}
	return l.store.UpdatePlacementByTxid(ctx, txid, blockHeight != 0, blockHeight, blockIndex)
}

// TransactionRolledBack removes claims created by the rolled-back transaction
// and restores claims it spent. The hook is structural until the engine adds
// it to LookupService.
func (l *LookupService) TransactionRolledBack(ctx context.Context, txid *chainhash.Hash, topic string) error {
	if topic != TopicName {
		return nil
	}
	if txid == nil {
		return fmt.Errorf("ecosystem-alias rollback transaction ID must not be nil")
	}
	if l.store == nil {
		return fmt.Errorf("ecosystem-alias claim store is not configured")
	}
	return l.store.RollbackTransaction(ctx, txid)
}

// Lookup returns direct Atomic BEEF output-list entries in ClaimStore order.
func (l *LookupService) Lookup(ctx context.Context, question *overlaylookup.LookupQuestion) (*overlaylookup.LookupAnswer, error) {
	if question == nil {
		return nil, fmt.Errorf("ecosystem-alias lookup question must not be nil")
	}
	if question.Service != LookupName {
		return nil, fmt.Errorf("unsupported ecosystem-alias lookup service %q", question.Service)
	}
	if l.store == nil {
		return nil, fmt.Errorf("ecosystem-alias claim store is not configured")
	}
	if l.outputs == nil {
		return nil, fmt.Errorf("ecosystem-alias output loader is not configured")
	}

	query, err := DecodeQuery(question.Query)
	if err != nil {
		return nil, err
	}
	var cursor *Cursor
	if query.Cursor != nil {
		bound, err := BindCursor(*query.Cursor, query)
		if err != nil {
			return nil, err
		}
		cursor = &bound
	}

	claims, err := l.store.QueryClaims(ctx, query, cursor, int(query.PageLimit()))
	if err != nil {
		return nil, err
	}
	items := make([]*overlaylookup.OutputListItem, 0, len(claims))
	for i := range claims {
		item, err := l.hydrate(ctx, &claims[i].Outpoint)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}

	return &overlaylookup.LookupAnswer{
		Type:    overlaylookup.AnswerTypeOutputList,
		Outputs: items,
	}, nil
}

func (l *LookupService) hydrate(ctx context.Context, outpoint *transaction.Outpoint) (*overlaylookup.OutputListItem, error) {
	if outpoint == nil {
		return nil, fmt.Errorf("ecosystem-alias candidate outpoint must not be nil")
	}
	topic := TopicName
	unspent := false
	output, err := l.outputs.FindOutput(ctx, outpoint, &topic, &unspent, true)
	if err != nil {
		return nil, fmt.Errorf("failed to load ecosystem-alias output %s: %w", outpointString(outpoint), err)
	}
	if output == nil {
		return nil, fmt.Errorf("stored ecosystem-alias candidate %s is not an unspent %s output", outpointString(outpoint), TopicName)
	}
	if output.Outpoint != *outpoint || output.Topic != TopicName || output.Spent {
		return nil, fmt.Errorf("stored ecosystem-alias candidate %s hydrated as a different or spent output", outpointString(outpoint))
	}
	if output.Beef == nil {
		return nil, fmt.Errorf("stored ecosystem-alias candidate %s has no BEEF", outpointString(outpoint))
	}
	// Output loaders may deduplicate BEEF pointers. Ancillary merging mutates
	// BEEF, so isolate this request before loading or serializing the graph.
	output.Beef = output.Beef.Clone()
	if err := l.outputs.LoadAncillaryBeef(ctx, output); err != nil {
		return nil, fmt.Errorf("failed to load ancillary BEEF for ecosystem-alias output %s: %w", outpointString(outpoint), err)
	}
	if output.Beef == nil {
		return nil, fmt.Errorf("stored ecosystem-alias candidate %s has no BEEF after ancillary hydration", outpointString(outpoint))
	}
	tx := output.Beef.FindTransactionByHash(&outpoint.Txid)
	if tx == nil {
		return nil, fmt.Errorf("BEEF for ecosystem-alias candidate %s does not contain its transaction", outpointString(outpoint))
	}
	if uint64(outpoint.Index) >= uint64(len(tx.Outputs)) {
		return nil, fmt.Errorf("ecosystem-alias candidate %s output index is out of range", outpointString(outpoint))
	}
	if !output.Beef.IsValid(false) {
		return nil, fmt.Errorf("BEEF for ecosystem-alias candidate %s has incomplete or invalid ancestry", outpointString(outpoint))
	}

	atomic, err := output.Beef.AtomicBytes(&outpoint.Txid)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize Atomic BEEF for ecosystem-alias output %s: %w", outpointString(outpoint), err)
	}
	return &overlaylookup.OutputListItem{Beef: atomic, OutputIndex: outpoint.Index}, nil
}

func outpointString(outpoint *transaction.Outpoint) string {
	if outpoint == nil {
		return "<nil>"
	}
	return outpoint.String()
}

// GetDocumentation returns human-readable lookup documentation.
func (l *LookupService) GetDocumentation() string {
	return "BRC-169 ecosystem-alias lookup by alias, domain, or findAll"
}

// GetMetaData returns lookup service metadata.
func (l *LookupService) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{
		Name:        LookupName,
		Description: "BRC-169 ecosystem-alias claims",
		Version:     ProtocolVersion,
	}
}

var _ engine.LookupService = (*LookupService)(nil)

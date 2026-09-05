package ecosystemalias

import (
	"context"
	"fmt"
	"sort"

	overlaystorage "github.com/b-open-io/1sat-stack/pkg/overlay/storage"
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	overlaylookup "github.com/bsv-blockchain/go-sdk/overlay/lookup"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

const (
	eventAliasPrefix  = "alias:"
	eventDomainPrefix = "domain:"
)

// LookupService implements ls_ecosystemalias using overlay events.
type LookupService struct {
	store overlaystorage.TopicStorage
}

// NewLookupService creates the ecosystem-alias lookup service.
func NewLookupService(store overlaystorage.TopicStorage) *LookupService {
	return &LookupService{store: store}
}

// OutputAdmittedByTopic indexes a valid claim under alias and domain event keys.
func (l *LookupService) OutputAdmittedByTopic(ctx context.Context, payload *engine.OutputAdmittedByTopic) error {
	if payload == nil {
		return fmt.Errorf("ecosystem-alias admission payload must not be nil")
	}
	if payload.Topic != TopicName {
		return nil
	}
	if l.store == nil {
		return fmt.Errorf("ecosystem-alias event store is not configured")
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
		return fmt.Errorf("ecosystem-alias output index %d is out of range", payload.OutputIndex)
	}

	out := tx.Outputs[payload.OutputIndex]
	claim, err := Decode(out.LockingScript, out.Satoshis)
	if err != nil {
		return fmt.Errorf("failed to decode ecosystem-alias output %s.%d: %w", txid.String(), payload.OutputIndex, err)
	}

	op := &transaction.Outpoint{Txid: *txid, Index: payload.OutputIndex}
	score := types.ScoreFromTx(tx, txid)
	if err := l.store.SaveEvent(ctx, eventAliasPrefix+claim.Alias, op, score); err != nil {
		return err
	}
	return l.store.SaveEvent(ctx, eventDomainPrefix+claim.Domain, op, score)
}

// OutputSpent is a no-op: spends are recorded on outputs.spend_txid.
func (l *LookupService) OutputSpent(context.Context, *engine.OutputSpent) error {
	return nil
}

// OutputNoLongerRetainedInHistory is a no-op; eviction deletes the output and events.
func (l *LookupService) OutputNoLongerRetainedInHistory(context.Context, *transaction.Outpoint, string) error {
	return nil
}

// OutputEvicted is a no-op; topic storage deletes events with the output.
func (l *LookupService) OutputEvicted(context.Context, *transaction.Outpoint) error {
	return nil
}

// OutputBlockHeightUpdated restamps event scores for the transaction to HeightScore.
func (l *LookupService) OutputBlockHeightUpdated(ctx context.Context, txid *chainhash.Hash, blockHeight uint32, blockIndex uint64) error {
	if l.store == nil {
		return fmt.Errorf("ecosystem-alias event store is not configured")
	}
	if txid == nil {
		return fmt.Errorf("ecosystem-alias block-height txid must not be nil")
	}
	return l.store.UpdateEventsForTxid(ctx, txid, types.HeightScore(blockHeight, blockIndex))
}

// Lookup returns formulas for unspent matching claims, ordered by event score.
func (l *LookupService) Lookup(ctx context.Context, question *overlaylookup.LookupQuestion) (*overlaylookup.LookupAnswer, error) {
	if question == nil {
		return nil, fmt.Errorf("ecosystem-alias lookup question must not be nil")
	}
	if question.Service != LookupName {
		return nil, fmt.Errorf("unsupported ecosystem-alias lookup service %q", question.Service)
	}
	if l.store == nil {
		return nil, fmt.Errorf("ecosystem-alias event store is not configured")
	}

	query, err := DecodeQuery(question.Query)
	if err != nil {
		return nil, err
	}

	var recs []overlaystorage.OutputRecord
	switch query.Mode() {
	case ModeAlias:
		recs, err = l.store.FindByEvent(ctx, eventAliasPrefix+*query.Alias, nil)
	case ModeDomain:
		recs, err = l.store.FindByEvent(ctx, eventDomainPrefix+*query.Domain, nil)
	default:
		return nil, fail(CodeInvalidCombination, "query must have exactly one of alias or domain")
	}
	if err != nil {
		return nil, err
	}

	unspent := recs[:0]
	for i := range recs {
		if recs[i].SpendTxid != nil {
			continue
		}
		unspent = append(unspent, recs[i])
	}

	sort.SliceStable(unspent, func(i, j int) bool {
		return CompareLookup(Placement{Score: unspent[i].Score, Vout: unspent[i].Outpoint.Index}, Placement{Score: unspent[j].Score, Vout: unspent[j].Outpoint.Index}) < 0
	})

	if uint64(query.PageSkip()) >= uint64(len(unspent)) {
		unspent = nil
	} else {
		unspent = unspent[query.PageSkip():]
	}
	limit := int(query.PageLimit())
	if limit < len(unspent) {
		unspent = unspent[:limit]
	}

	formulas := make([]overlaylookup.LookupFormula, 0, len(unspent))
	for i := range unspent {
		op := unspent[i].Outpoint
		formulas = append(formulas, overlaylookup.LookupFormula{Outpoint: &op})
	}
	return &overlaylookup.LookupAnswer{
		Type:     overlaylookup.AnswerTypeFormula,
		Formulas: formulas,
	}, nil
}

func (l *LookupService) GetDocumentation() string {
	return "BRC-169 ecosystem-alias lookup by alias or domain"
}

func (l *LookupService) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{
		Name:        LookupName,
		Description: "BRC-169 ecosystem-alias claims",
		Version:     ProtocolVersion,
	}
}

var _ engine.LookupService = (*LookupService)(nil)

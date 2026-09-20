package gib

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	gibtpl "github.com/b-open-io/1sat-stack/pkg/template/gib"
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/overlay/lookup"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// LookupService indexes commit heads and their spend chains.
type LookupService struct {
	store  *Store
	logger *slog.Logger
	meta   MetaFetcher
}

// SetMetaFetcher enables `.gib` enrichment (name, description, default
// branch) at admission and on demand.
func (l *LookupService) SetMetaFetcher(f MetaFetcher) { l.meta = f }

// FillMeta fetches and stores `.gib` for a head that has none. Returns true
// when metadata was found.
func (l *LookupService) FillMeta(ctx context.Context, rec *HeadRecord) bool {
	if rec == nil || rec.Meta != nil {
		return rec != nil && rec.Meta != nil
	}
	m := l.fetchMeta(ctx, rec.Origin)
	if m == nil {
		return false
	}
	rec.Meta = m
	if err := l.store.UpsertHead(ctx, rec); err != nil {
		l.logger.Warn("gib: store .gib metadata", "outpoint", rec.Outpoint, "error", err)
	}
	return true
}

var _ engine.LookupService = (*LookupService)(nil)

// NewLookupService creates the gib lookup service.
func NewLookupService(store *Store, logger *slog.Logger) *LookupService {
	if logger == nil {
		logger = slog.Default()
	}
	return &LookupService{store: store, logger: logger}
}

// Query is the BRC-24 lookup query for ls_gib. All filters are optional;
// outpoint short-circuits the rest.
type Query struct {
	Outpoint     string `json:"outpoint,omitempty"`
	Origin       string `json:"origin,omitempty"`
	Branch       string `json:"branch,omitempty"`
	Identity     string `json:"identity,omitempty"`
	Sha          string `json:"sha,omitempty"`
	IncludeSpent bool   `json:"includeSpent,omitempty"`
	Limit        int    `json:"limit,omitempty"`
	Skip         int    `json:"skip,omitempty"`
}

type spentHead struct {
	outpoint *transaction.Outpoint
	head     *gibtpl.Head
	score    float64
}

// OutputAdmittedByTopic stores the admitted head, links it to the head it
// spent (same origin and branch), and records the spend of every gib input
// in the transaction so history is complete even if the engine never saw the
// predecessor.
func (l *LookupService) OutputAdmittedByTopic(ctx context.Context, payload *engine.OutputAdmittedByTopic) error {
	if payload == nil || payload.Topic != TopicName {
		return nil
	}
	beef, txid, err := transaction.NewBeefFromAtomicBytes(payload.AtomicBEEF)
	if err != nil {
		return fmt.Errorf("gib: parse atomic BEEF: %w", err)
	}
	// FindTransactionForSigning links each input's source transaction from
	// the BEEF so the spent heads can be decoded.
	tx := beef.FindTransactionForSigningByHash(txid)
	if tx == nil {
		return fmt.Errorf("gib: atomic BEEF does not contain %s", txid.String())
	}
	if int(payload.OutputIndex) >= len(tx.Outputs) {
		return fmt.Errorf("gib: output index %d out of range for %s", payload.OutputIndex, txid.String())
	}
	out := tx.Outputs[payload.OutputIndex]
	head, err := gibtpl.Decode(out.LockingScript, out.Satoshis)
	if err != nil {
		l.logger.Debug("admitted output is not a gib head", "txid", txid.String(), "vout", payload.OutputIndex, "error", err)
		return nil
	}

	score := types.ScoreFromTx(tx, txid)
	op := &transaction.Outpoint{Txid: *txid, Index: payload.OutputIndex}
	rec := recordFromHead(op, head, score)
	rec.Meta = l.fetchMeta(ctx, head.Origin)

	spent := gibInputs(tx)
	for _, in := range spent {
		if in.head.Origin == head.Origin && in.head.Branch == head.Branch {
			rec.Prev = in.outpoint.OrdinalString()
			break
		}
	}
	if err := l.store.UpsertHead(ctx, rec); err != nil {
		return fmt.Errorf("gib: upsert head %s: %w", rec.Outpoint, err)
	}
	if _, err := l.recordSpends(ctx, tx, txid, spent, score); err != nil {
		return err
	}
	return nil
}

// RecordSpends scans a transaction's inputs for commit heads and records
// each as spent, naming the successor head (same origin and branch) created
// by the transaction when there is one. Independent of the overlay engine:
// the head need not have been admitted first. Inputs must carry their
// source transactions (a full BEEF).
func (l *LookupService) RecordSpends(ctx context.Context, tx *transaction.Transaction, txid *chainhash.Hash) (int, error) {
	return l.recordSpends(ctx, tx, txid, gibInputs(tx), types.ScoreFromTx(tx, txid))
}

func (l *LookupService) recordSpends(ctx context.Context, tx *transaction.Transaction, txid *chainhash.Hash, spent []spentHead, spendScore float64) (int, error) {
	if len(spent) == 0 {
		return 0, nil
	}
	successors := map[string]string{} // origin+"\x00"+branch -> outpoint
	for vout, out := range tx.Outputs {
		if out == nil {
			continue
		}
		if h, err := gibtpl.Decode(out.LockingScript, out.Satoshis); err == nil {
			key := h.Origin + "\x00" + h.Branch
			if _, seen := successors[key]; !seen {
				successors[key] = (&transaction.Outpoint{Txid: *txid, Index: uint32(vout)}).OrdinalString()
			}
		}
	}
	for _, in := range spent {
		prev := recordFromHead(in.outpoint, in.head, in.score)
		prev.Spend = &Spend{Txid: txid.String(), Score: spendScore, Next: successors[in.head.Origin+"\x00"+in.head.Branch]}
		if err := l.store.UpsertHead(ctx, prev); err != nil {
			return 0, fmt.Errorf("gib: record spend of %s: %w", prev.Outpoint, err)
		}
	}
	return len(spent), nil
}

// OutputSpent records a spend the engine noticed. When the spending BEEF
// carries the head's transaction the row is upserted from it, so a spend
// seen before its admission still lands.
func (l *LookupService) OutputSpent(ctx context.Context, payload *engine.OutputSpent) error {
	if payload == nil || payload.Topic != TopicName || payload.Outpoint == nil || payload.SpendingTxid == nil {
		return nil
	}
	spendTxid := payload.SpendingTxid.String()
	next := ""
	var spendScore float64
	var prevRec *HeadRecord

	if payload.SpendingAtomicBEEF != nil {
		if beef, tx, txid, err := transaction.ParseBeef(payload.SpendingAtomicBEEF); err == nil {
			spendScore = types.ScoreFromTx(tx, txid)
			var prevHead *gibtpl.Head
			if srcTx := beef.FindTransaction(payload.Outpoint.Txid.String()); srcTx != nil &&
				int(payload.Outpoint.Index) < len(srcTx.Outputs) {
				src := srcTx.Outputs[payload.Outpoint.Index]
				if head, err := gibtpl.Decode(src.LockingScript, src.Satoshis); err == nil {
					prevHead = head
					prevRec = recordFromHead(payload.Outpoint, head, types.ScoreFromTx(srcTx, &payload.Outpoint.Txid))
				}
			}
			if prevHead != nil {
				for vout, out := range tx.Outputs {
					if h, err := gibtpl.Decode(out.LockingScript, out.Satoshis); err == nil &&
						h.Origin == prevHead.Origin && h.Branch == prevHead.Branch {
						next = (&transaction.Outpoint{Txid: *txid, Index: uint32(vout)}).OrdinalString()
						break
					}
				}
			}
		}
	}

	if prevRec != nil {
		prevRec.Spend = &Spend{Txid: spendTxid, Next: next, Score: spendScore}
		return l.store.UpsertHead(ctx, prevRec)
	}
	_, err := l.store.MarkSpent(ctx, payload.Outpoint.OrdinalString(), spendTxid, next, spendScore)
	return err
}

// OutputNoLongerRetainedInHistory is a no-op: history lives in gib_heads.
func (l *LookupService) OutputNoLongerRetainedInHistory(context.Context, *transaction.Outpoint, string) error {
	return nil
}

// OutputEvicted removes a head the engine dropped (reorg / rollback).
func (l *LookupService) OutputEvicted(ctx context.Context, outpoint *transaction.Outpoint) error {
	if outpoint == nil {
		return nil
	}
	return l.store.DeleteHead(ctx, outpoint.OrdinalString())
}

// OutputBlockHeightUpdated restamps scores once the transaction is mined.
func (l *LookupService) OutputBlockHeightUpdated(ctx context.Context, txid *chainhash.Hash, blockHeight uint32, blockIndex uint64) error {
	if txid == nil {
		return nil
	}
	return l.store.UpdateScoreForTxid(ctx, txid.String(), types.HeightScore(blockHeight, blockIndex))
}

// Lookup answers BRC-24 questions with output-list formulas for matching
// heads (current heads only unless includeSpent is set).
func (l *LookupService) Lookup(ctx context.Context, question *lookup.LookupQuestion) (*lookup.LookupAnswer, error) {
	if question == nil {
		return nil, fmt.Errorf("gib: lookup question must not be nil")
	}
	if question.Service != LookupName {
		return nil, fmt.Errorf("gib: unsupported lookup service %q", question.Service)
	}
	var q Query
	if len(question.Query) > 0 {
		if err := json.Unmarshal(question.Query, &q); err != nil {
			return nil, fmt.Errorf("gib: invalid query: %w", err)
		}
	}

	var recs []HeadRecord
	if q.Outpoint != "" {
		op, err := transaction.OutpointFromString(q.Outpoint)
		if err != nil {
			return nil, fmt.Errorf("gib: invalid outpoint: %w", err)
		}
		rec, err := l.store.GetHead(ctx, op.OrdinalString())
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if rec != nil {
			recs = []HeadRecord{*rec}
		}
	} else {
		limit := clampLimit(q.Limit)
		skip := max(q.Skip, 0)
		all, err := l.store.ListHeads(ctx, HeadFilter{
			Origin:    q.Origin,
			Branch:    q.Branch,
			Identity:  q.Identity,
			CommitSha: q.Sha,
			Unspent:   !q.IncludeSpent,
			Limit:     min(skip+limit, MaxLimit),
			Rev:       true,
		})
		if err != nil {
			return nil, err
		}
		if skip < len(all) {
			recs = all[skip:]
		}
	}

	formulas := make([]lookup.LookupFormula, 0, len(recs))
	for i := range recs {
		op, err := transaction.OutpointFromString(recs[i].Outpoint)
		if err != nil {
			continue
		}
		formulas = append(formulas, lookup.LookupFormula{Outpoint: op})
	}
	return &lookup.LookupAnswer{Type: lookup.AnswerTypeFormula, Formulas: formulas}, nil
}

// GetDocumentation returns documentation for this lookup service.
func (l *LookupService) GetDocumentation() string {
	return "gib commit heads by outpoint, origin, branch, or identity"
}

// GetMetaData returns metadata for the lookup service.
func (l *LookupService) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{
		Name:        LookupName,
		Description: "gib on-chain git branch pointers (commit heads)",
		Version:     ProtocolVersion,
	}
}

func recordFromHead(op *transaction.Outpoint, head *gibtpl.Head, score float64) *HeadRecord {
	return &HeadRecord{
		Outpoint: op.OrdinalString(),
		Txid:     op.Txid.String(),
		Vout:     op.Index,
		Origin:   head.Origin,
		Branch:   head.Branch,
		Root:     head.Root,
		Identity: head.Identity,
		Commit:   head.Commit,
		Score:    score,
	}
}

// gibInputs decodes every input whose source output is a commit head. Inputs
// need their source transactions (a full BEEF).
func gibInputs(tx *transaction.Transaction) []spentHead {
	var found []spentHead
	for _, input := range tx.Inputs {
		if input == nil || input.SourceTXID == nil {
			continue
		}
		src := input.SourceTxOutput()
		if src == nil {
			continue
		}
		head, err := gibtpl.Decode(src.LockingScript, src.Satoshis)
		if err != nil {
			continue
		}
		found = append(found, spentHead{
			outpoint: &transaction.Outpoint{Txid: *input.SourceTXID, Index: input.SourceTxOutIndex},
			head:     head,
			score:    types.ScoreFromTx(input.SourceTransaction, input.SourceTXID),
		})
	}
	return found
}

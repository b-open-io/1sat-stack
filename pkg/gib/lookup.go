package gib

import (
	"context"
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
	beef   BeefLoader
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
	// A push submits the content transactions alongside the head, so the
	// BEEF is not always atomic: ParseBeef takes either, and picks the same
	// subject transaction the engine did.
	beef, _, txid, err := transaction.ParseBeef(payload.AtomicBEEF)
	if err != nil {
		return fmt.Errorf("gib: parse submitted BEEF: %w", err)
	}
	if beef == nil || txid == nil {
		return fmt.Errorf("gib: submitted BEEF has no subject transaction")
	}
	// FindTransactionForSigning links each input's source transaction from
	// the BEEF so the spent heads can be decoded.
	tx := beef.FindTransactionForSigningByHash(txid)
	if tx == nil {
		return fmt.Errorf("gib: submitted BEEF does not contain %s", txid.String())
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

	// The commits come from the root's `.git` store, read out of the same
	// submission the topic manager admitted the head from. A head that got
	// past admission has one; if this read fails the head is still indexed,
	// because the branch pointer is true whatever the content says.
	push, err := ReadPush(beef, head, txid)
	if err != nil {
		l.logger.Warn("gib: admitted head without a readable push",
			"outpoint", rec.Outpoint, "root", head.Root, "error", err)
	} else {
		rec.Commit = tipCommit(push)
	}

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
	if push != nil {
		if err := l.store.UpsertCommits(ctx, commitRecords(rec.Outpoint, push, score)); err != nil {
			return fmt.Errorf("gib: index commits of %s: %w", rec.Outpoint, err)
		}
	}
	if _, err := l.recordSpends(ctx, tx, txid, spent, score); err != nil {
		return err
	}
	return nil
}

// tipCommit is what a head record carries in place of the commit that used
// to be inscribed on it: the tip commit object of the root's `.git` store.
// When the push cited that object instead of republishing it, only the sha
// is known — which is still the answer to "what does this head publish".
func tipCommit(push *Push) *gibtpl.Commit {
	if push == nil {
		return nil
	}
	if push.Tip != nil {
		return push.Tip
	}
	if push.TipSha == "" {
		return nil
	}
	return &gibtpl.Commit{SHA: push.TipSha, Parents: []string{}}
}

// commitRecords turns a push's object store into index rows: one per
// commit it names, held or only cited.
func commitRecords(outpoint string, push *Push, score float64) []CommitRecord {
	recs := make([]CommitRecord, 0, len(push.Objects))
	for _, obj := range push.Objects {
		recs = append(recs, CommitRecord{
			Sha:      obj.Sha,
			Outpoint: outpoint,
			Ref:      obj.Ref.OrdinalString(),
			Held:     obj.Held(),
			Commit:   obj.Commit,
			Score:    score,
		})
	}
	return recs
}

// recordSpends records every commit head a submitted transaction takes as an
// input as spent, naming the successor head (same origin and branch) the
// transaction creates when there is one. This is the spend the engine
// observes at admission — a push taking the branch's previous head — and it
// is what orders a branch's history. Inputs must carry their source
// transactions (a full BEEF).
//
// Nothing outside admission writes a spend any more: the overlay learns a
// head was spent when it is handed the transaction that spent it, and never
// by watching the chain.
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

// Lookup answers BRC-24 questions. Every question names its type; the three
// are branches, headsSince and txs, and each builds its own answer here (see
// lookup_sync.go).
//
// There was a fourth, the typeless `heads` query, which answered with
// formulas for matching heads. It is gone. A formula answer has never
// reached a client from this stack: the engine hydrates one with
// Storage.FindOutput(ctx, outpoint, nil, nil, true) — a nil topic — and this
// stack's EngineAdapter rejects that with "topic is required", so the
// question failed in production however it was asked. Its one real use was
// enumerating a repository, which `branches` now does properly. Removing it
// turns an answer that always failed into a refusal that says so; the same
// filters are served over REST by /1sat/gib/heads, which works.
func (l *LookupService) Lookup(ctx context.Context, question *lookup.LookupQuestion) (*lookup.LookupAnswer, error) {
	if question == nil {
		return nil, fmt.Errorf("gib: lookup question must not be nil")
	}
	if question.Service != LookupName {
		return nil, fmt.Errorf("gib: unsupported lookup service %q", question.Service)
	}
	switch t := queryType(question.Query); t {
	case QueryTypeBranches:
		return l.answerBranches(ctx, question.Query)
	case QueryTypeHeadsSince:
		return l.answerHeadsSince(ctx, question.Query)
	case QueryTypeTxs:
		return l.answerTxs(ctx, question.Query)
	case "":
		return nil, fmt.Errorf("gib: a lookup query must name its type: %q, %q or %q",
			QueryTypeBranches, QueryTypeHeadsSince, QueryTypeTxs)
	default:
		return nil, fmt.Errorf("gib: unknown query type %q; the types are %q, %q and %q",
			t, QueryTypeBranches, QueryTypeHeadsSince, QueryTypeTxs)
	}
}

// GetDocumentation returns documentation for this lookup service.
func (l *LookupService) GetDocumentation() string {
	return `gib repository sync: {"type":"branches"} enumerates a ` +
		"repository's branches with each one's tip head and the default " +
		`branch to clone from; {"type":"headsSince"} walks one branch ` +
		"forward from a head the client already has, as an output list of " +
		`heads with their BEEF; {"type":"txs"} returns whole transactions ` +
		"by txid as one merged BEEF"
}

// GetMetaData returns metadata for the lookup service.
func (l *LookupService) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{
		Name:        LookupName,
		Description: "gib on-chain git branch pointers (commit heads)",
		Version:     ProtocolVersion,
	}
}

// recordFromHead is the head as its token alone tells it. The commit is not
// in the token any more, so it is filled in by the caller that has the
// submission to read the root's `.git` store from; the spend paths, which
// see only the spent output, leave it empty and the store keeps whatever
// admission already indexed.
func recordFromHead(op *transaction.Outpoint, head *gibtpl.Head, score float64) *HeadRecord {
	return &HeadRecord{
		Outpoint:     op.OrdinalString(),
		Txid:         op.Txid.String(),
		Vout:         op.Index,
		Origin:       head.Origin,
		Branch:       head.Branch,
		Root:         head.Root,
		Identity:     head.Identity,
		BranchedFrom: head.BranchedFrom,
		Score:        score,
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

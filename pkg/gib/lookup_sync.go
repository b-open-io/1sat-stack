package gib

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay/lookup"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// The sync lookups. A client that knows only the overlay endpoint a domain
// declares in its BRC-180 manifest can follow a branch and fetch the
// transactions its trees cite, without the host's REST root.
//
// headsSince answers with an output list, built here rather than returned as
// formulas: the engine hydrates a formula with
// Storage.FindOutput(ctx, outpoint, nil, nil, true) — a nil topic — and this
// stack's EngineAdapter.FindOutput rejects that with "FindOutput: topic is
// required", so a formula answer fails in production. See
// TestFormulaHydrationStillRejectsNilTopic. txs answers freeform with one
// merged BEEF, because it asks for transactions rather than outputs.
const (
	// QueryTypeHeads is the original head query (outpoint / repository
	// origin / branch / identity / sha). It is also the empty default, so
	// clients written before the sync queries are unaffected.
	QueryTypeHeads = "heads"
	// QueryTypeHeadsSince asks for one branch's heads from a point forward.
	QueryTypeHeadsSince = "headsSince"
	// QueryTypeTxs asks for whole transactions by txid. Its answer is
	// freeform — one merged BEEF — not an output list.
	QueryTypeTxs = "txs"

	// MaxHeadsSince caps one headsSince page. A client resumes by setting
	// `since` to the last outpoint it received.
	MaxHeadsSince = MaxLimit
	// MaxTxids caps one txs request. The answer carries whole transactions,
	// far heavier than an outpoint, so this is deliberately lower than
	// MaxLimit. A push writes many outputs in one transaction, so 50
	// transactions cover a large tree fetch; over the cap the request is
	// rejected rather than silently truncated.
	MaxTxids = 50

	// CodeUnknownSince marks a `since` outpoint this overlay does not hold
	// for the requested branch. It is reported in the answer's result rather
	// than as a Go error because the overlay HTTP layer collapses every
	// lookup error to an opaque 500 (see the BRC-24 error handler in
	// go-overlay-services), which a client cannot tell from any other
	// failure. An empty output list with no code means "nothing new".
	CodeUnknownSince = "unknown-since"
	// CodeMissingBeef marks a page cut short because an indexed head's
	// transaction is no longer in the BEEF store. Resending the same `since`
	// returns the same gap, so the client stops and repairs (gib recover)
	// rather than paging forever.
	CodeMissingBeef = "missing-beef"
)

// BeefLoader supplies the BEEF for a transaction this overlay holds. The
// stack's shared *beef.Storage satisfies it.
type BeefLoader interface {
	LoadBeef(ctx context.Context, txid *chainhash.Hash) (*transaction.Beef, error)
}

// SetBeefLoader wires the BEEF source the sync lookups answer from. Without
// it both sync queries fail: the module cannot hand out transactions it has
// no way to read.
func (l *LookupService) SetBeefLoader(b BeefLoader) { l.beef = b }

// HeadsSinceQuery asks for one branch's heads from a point forward, oldest
// first. Origin is the repository origin: the outpoint of the genesis
// `ordfs/dir` root that identifies the repository. Since is exclusive — the
// head it names is not returned — so a client resumes by passing the last
// outpoint it received; empty means from the branch's first head. Identity
// is optional; empty returns every publisher's heads on the branch, and the
// returned heads are then a subsequence of the spend chain rather than a
// contiguous run.
type HeadsSinceQuery struct {
	Type     string `json:"type"`
	Origin   string `json:"origin"`
	Branch   string `json:"branch"`
	Identity string `json:"identity,omitempty"`
	Since    string `json:"since,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

// HeadsSinceResult is the `result` beside a headsSince output list.
// Outpoints is index-aligned with the outputs and ordered oldest first.
type HeadsSinceResult struct {
	Query     string   `json:"query"`
	Origin    string   `json:"origin"`
	Branch    string   `json:"branch"`
	Identity  string   `json:"identity,omitempty"`
	Since     string   `json:"since,omitempty"`
	Outpoints []string `json:"outpoints"`
	// More is true when the page stopped short of the branch tip.
	More bool `json:"more"`
	// Code is empty on success, CodeUnknownSince when `since` names a head
	// this overlay does not hold for the branch, CodeMissingBeef when the
	// page stopped at a head whose transaction is no longer readable.
	Code string `json:"code,omitempty"`
}

// TxsQuery asks for whole transactions by txid: the unit of exchange, since
// one push writes many outputs in one transaction.
type TxsQuery struct {
	Type  string   `json:"type"`
	Txids []string `json:"txids"`
}

// TxsResult is the freeform answer to a txs query: one BEEF carrying every
// requested transaction this overlay holds, with their proofs. The answer is
// freeform, not an output list, because the request is for transactions and
// not for outputs — an output list would have to give each entry a
// meaningless output index. Merging also carries shared ancestry once
// instead of repeating it per transaction.
//
// Transactions the overlay does not hold are simply absent: the client
// parses the BEEF and sees for itself what came back. Holding none of them
// is an empty BEEF V2 (six bytes, zero transactions), which is a valid
// answer and parses normally — a failure is an HTTP error with no result at
// all, never an empty BEEF.
type TxsResult struct {
	Query string `json:"query"`
	// Beef is BEEF V2, base64 in JSON.
	Beef []byte `json:"beef"`
}

// queryType peeks at the discriminator. A malformed body is left to the
// chosen branch to report.
func queryType(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return ""
	}
	return envelope.Type
}

// answerHeadsSince walks a branch forward from `since` to the tip.
func (l *LookupService) answerHeadsSince(ctx context.Context, raw json.RawMessage) (*lookup.LookupAnswer, error) {
	var q HeadsSinceQuery
	if err := json.Unmarshal(raw, &q); err != nil {
		return nil, fmt.Errorf("gib: invalid headsSince query: %w", err)
	}
	if l.beef == nil {
		return nil, fmt.Errorf("gib: BEEF loader is not configured")
	}
	if q.Origin == "" {
		return nil, fmt.Errorf("gib: headsSince requires a repository origin")
	}
	origin, err := parseOutpointParam(q.Origin)
	if err != nil {
		return nil, fmt.Errorf("gib: invalid repository origin: %w", err)
	}
	if q.Branch == "" {
		return nil, fmt.Errorf("gib: headsSince requires a branch")
	}
	identity := ""
	if q.Identity != "" {
		if identity, err = parseIdentityParam(q.Identity); err != nil {
			return nil, fmt.Errorf("gib: invalid identity: %w", err)
		}
	}

	result := &HeadsSinceResult{
		Query:     QueryTypeHeadsSince,
		Origin:    origin,
		Branch:    q.Branch,
		Identity:  identity,
		Outpoints: []string{},
	}

	var after *BranchCursor
	if q.Since != "" {
		since, err := parseOutpointParam(q.Since)
		if err != nil {
			return nil, fmt.Errorf("gib: invalid since outpoint: %w", err)
		}
		result.Since = since
		rec, err := l.store.GetHead(ctx, since)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		// An unknown `since` must not silently reread the whole branch: the
		// client would take heads it already has for new work.
		if rec == nil || rec.Origin != origin || rec.Branch != q.Branch ||
			(identity != "" && rec.Identity != identity) {
			result.Code = CodeUnknownSince
			return &lookup.LookupAnswer{
				Type:    lookup.AnswerTypeOutputList,
				Outputs: []*lookup.OutputListItem{},
				Result:  result,
			}, nil
		}
		after = &BranchCursor{Score: rec.Score, Vout: rec.Vout}
	}

	// clampLimit caps at MaxLimit, which MaxHeadsSince is defined as.
	limit := clampLimit(q.Limit)
	// One extra row tells us whether the page stopped short of the tip.
	recs, err := l.store.ListBranchHeadsAfter(ctx, origin, q.Branch, identity, after, limit+1)
	if err != nil {
		return nil, err
	}
	if len(recs) > limit {
		recs = recs[:limit]
		result.More = true
	}

	fetch := l.newBeefFetcher()
	outputs := make([]*lookup.OutputListItem, 0, len(recs))
	for i := range recs {
		txid, err := chainhash.NewHashFromHex(recs[i].Txid)
		if err != nil {
			return nil, fmt.Errorf("gib: head %s has an invalid txid: %w", recs[i].Outpoint, err)
		}
		beefBytes := fetch(ctx, txid)
		if beefBytes == nil {
			// An indexed head whose BEEF is gone would silently shorten the
			// chain, which the client cannot detect. Stop at the gap and
			// name it, so the client repairs instead of paging forever.
			l.logger.Warn("gib: no BEEF for indexed head", "outpoint", recs[i].Outpoint)
			result.More = true
			result.Code = CodeMissingBeef
			break
		}
		outputs = append(outputs, &lookup.OutputListItem{Beef: beefBytes, OutputIndex: recs[i].Vout})
		result.Outpoints = append(result.Outpoints, recs[i].Outpoint)
	}

	return &lookup.LookupAnswer{Type: lookup.AnswerTypeOutputList, Outputs: outputs, Result: result}, nil
}

// answerTxs returns whole transactions, deduplicated at the txid, as one
// merged BEEF.
func (l *LookupService) answerTxs(ctx context.Context, raw json.RawMessage) (*lookup.LookupAnswer, error) {
	var q TxsQuery
	if err := json.Unmarshal(raw, &q); err != nil {
		return nil, fmt.Errorf("gib: invalid txs query: %w", err)
	}
	if l.beef == nil {
		return nil, fmt.Errorf("gib: BEEF loader is not configured")
	}
	if len(q.Txids) == 0 {
		return nil, fmt.Errorf("gib: txs requires at least one txid")
	}
	// Reject rather than truncate: a truncated answer looks like a complete
	// one, and the client would take the dropped transactions for absent.
	if len(q.Txids) > MaxTxids {
		return nil, fmt.Errorf("gib: %d txids requested, at most %d per request", len(q.Txids), MaxTxids)
	}

	// Deduplicate at the txid — the unit of exchange, since one push writes
	// many outputs in one transaction — keeping the requested order.
	seen := make(map[chainhash.Hash]struct{}, len(q.Txids))
	hashes := make([]*chainhash.Hash, 0, len(q.Txids))
	for _, want := range q.Txids {
		txid, err := chainhash.NewHashFromHex(strings.ToLower(strings.TrimSpace(want)))
		if err != nil {
			return nil, fmt.Errorf("gib: invalid txid %q: %w", want, err)
		}
		if _, dup := seen[*txid]; dup {
			continue
		}
		seen[*txid] = struct{}{}
		hashes = append(hashes, txid)
	}

	merged := transaction.NewBeef()
	for _, txid := range hashes {
		bf, err := l.beef.LoadBeef(ctx, txid)
		if err != nil || bf == nil {
			// Absent, not fatal: the client sees the gap in the BEEF and
			// asks elsewhere (or runs `gib recover`) for what this overlay
			// never saw.
			l.logger.Debug("gib: BEEF not available", "txid", txid.String(), "error", err)
			continue
		}
		// Merge through bytes. The BEEF store may hand the same *Beef to two
		// concurrent requests, and merging splices the source's BUMP and
		// transaction pointers into the destination, where a later merge can
		// mutate them. A fresh parse keeps this answer's BEEF its own.
		individual, err := bf.Bytes()
		if err == nil {
			err = merged.MergeBeefBytes(individual)
		}
		if err != nil {
			// A transaction that will not serialize or merge is one this
			// overlay cannot hand over; treat it as absent rather than
			// failing the whole request.
			l.logger.Warn("gib: merge BEEF", "txid", txid.String(), "error", err)
		}
	}

	beefBytes, err := merged.Bytes()
	if err != nil {
		return nil, fmt.Errorf("gib: serialize merged BEEF: %w", err)
	}
	return &lookup.LookupAnswer{
		Type:   lookup.AnswerTypeFreeform,
		Result: &TxsResult{Query: QueryTypeTxs, Beef: beefBytes},
	}, nil
}

// newBeefFetcher returns a per-request atomic-BEEF loader that reads each
// txid at most once. A transaction the overlay does not hold yields nil.
func (l *LookupService) newBeefFetcher() func(context.Context, *chainhash.Hash) []byte {
	cache := map[chainhash.Hash][]byte{}
	return func(ctx context.Context, txid *chainhash.Hash) []byte {
		if cached, ok := cache[*txid]; ok {
			return cached
		}
		var out []byte
		bf, err := l.beef.LoadBeef(ctx, txid)
		switch {
		case err != nil:
			l.logger.Debug("gib: BEEF not available", "txid", txid.String(), "error", err)
		case bf == nil:
			l.logger.Debug("gib: BEEF not available", "txid", txid.String())
		default:
			if out, err = bf.AtomicBytes(txid); err != nil {
				l.logger.Debug("gib: serialize BEEF", "txid", txid.String(), "error", err)
				out = nil
			}
		}
		cache[*txid] = out
		return out
	}
}

package gib

import (
	"encoding/hex"
	"encoding/json"
	"testing"

	overlaystorage "github.com/b-open-io/1sat-stack/pkg/overlay/storage"
	gibtpl "github.com/b-open-io/1sat-stack/pkg/template/gib"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	overlaylookup "github.com/bsv-blockchain/go-sdk/overlay/lookup"
	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

const (
	testOrigin  = "c657be5a7dacd7bb7343d92b7195d1366dbecd3ec31874576189efd28eee007c_0"
	testOrigin2 = "494bc4ec00000000000000000000000000000000000000000000000000005e5e_0"
	// Two outpoints no test transaction ever carries: a root nothing
	// published, for the paths that must refuse or report "not found".
	testRoot1  = "e6f27ce723b2923e93227ecef64b6cecf9464bd0c6ba71a66502aa20c5de82a1_3"
	testRoot2  = "83c55ad839d8bca1042909663b6a1d7468e7dfd71c67cd43d52363a254359138_1"
	testCommit = "tree 4b825dc642cb6eb9a060e54bf8d69288fbee4904\nauthor A <a@x> 1700000000 +0000\ncommitter A <a@x> 1700000000 +0000\n\nfirst\n"
)

// commitObject builds a git commit object: the message tells it apart, the
// parents chain it. commitObject("first") is testCommit.
func commitObject(message string, parents ...string) string {
	out := "tree 4b825dc642cb6eb9a060e54bf8d69288fbee4904\n"
	for _, p := range parents {
		out += "parent " + p + "\n"
	}
	return out + "author A <a@x> 1700000000 +0000\ncommitter A <a@x> 1700000000 +0000\n\n" + message + "\n"
}

func sha(commit string) string { return gibtpl.ObjectID("commit", []byte(commit)) }

type fixture struct {
	t        *testing.T
	store    *Store
	svc      *LookupService
	identity *ec.PublicKey
	lockKey  *ec.PublicKey
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	factory, err := overlaystorage.NewSQLiteFactory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = factory.Close() })
	ts, err := factory.Topic(TopicName)
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(ts.DB(), ts.TopicID(), nil)
	id, _ := ec.PrivateKeyFromHex("0000000000000000000000000000000000000000000000000000000000000002")
	lk, _ := ec.PrivateKeyFromHex("0000000000000000000000000000000000000000000000000000000000000003")
	return &fixture{t: t, store: store, svc: NewLookupService(store, nil), identity: id.PubKey(), lockKey: lk.PubKey()}
}

func (f *fixture) identityHex() string { return hex.EncodeToString(f.identity.Compressed()) }

func (f *fixture) headScript(origin, branch, root, branchedFrom string) *script.Script {
	f.t.Helper()
	fields, err := gibtpl.Fields(origin, branch, root, f.identity, branchedFrom)
	if err != nil {
		f.t.Fatal(err)
	}
	s, err := gibtpl.LockingScript(f.lockKey, fields)
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}

// mintTx creates a head with no gib inputs.
func (f *fixture) mintTx(origin, branch, root string) *transaction.Transaction {
	tx := transaction.NewTransaction()
	tx.AddOutput(&transaction.TransactionOutput{Satoshis: 1, LockingScript: f.headScript(origin, branch, root, "")})
	return tx
}

// spendTx spends output 0 of prev and adds the given head outputs
// (nil script = no successor, i.e. a burn).
func (f *fixture) spendTx(prev *transaction.Transaction, outputs ...*script.Script) *transaction.Transaction {
	tx := transaction.NewTransaction()
	tx.AddInput(&transaction.TransactionInput{
		SourceTXID:        prev.TxID(),
		SourceTxOutIndex:  0,
		SourceTransaction: prev,
	})
	for _, s := range outputs {
		tx.AddOutput(&transaction.TransactionOutput{Satoshis: 1, LockingScript: s})
	}
	// A burn still needs some output to be a valid transaction.
	if len(outputs) == 0 {
		tx.AddOutput(&transaction.TransactionOutput{Satoshis: 0, LockingScript: &script.Script{script.OpFALSE, script.OpRETURN}})
	}
	return tx
}

func atomicBeef(t *testing.T, txs ...*transaction.Transaction) []byte {
	t.Helper()
	beef := transaction.NewBeef()
	for _, tx := range txs {
		if _, err := beef.MergeTransaction(tx); err != nil {
			t.Fatal(err)
		}
	}
	b, err := beef.AtomicBytes(txs[len(txs)-1].TxID())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// admit drives one admission: the submission is every transaction given,
// with the last as its subject — the head transaction, preceded by the
// content transactions holding the push it publishes.
func (f *fixture) admit(vout uint32, txs ...*transaction.Transaction) {
	f.t.Helper()
	if err := f.svc.OutputAdmittedByTopic(f.t.Context(), &engine.OutputAdmittedByTopic{
		Topic:       TopicName,
		OutputIndex: vout,
		AtomicBEEF:  submissionBeef(f.t, txs...),
	}); err != nil {
		f.t.Fatal(err)
	}
}

func op(tx *transaction.Transaction, vout uint32) string {
	return (&transaction.Outpoint{Txid: *tx.TxID(), Index: vout}).OrdinalString()
}

// A head is admitted only with the push it publishes: the root transaction
// in the submission, an `ordfs/dir` at that outpoint, and a `.git` store in
// it. Every transaction that reading relied on comes back as an ancillary
// txid, so it is kept with the head.
func TestTopicManagerAdmitsHeadsWithTheirPush(t *testing.T) {
	f := newFixture(t)
	content := publish(t, 0x21, []string{testCommit}, nil, testCommit)

	tx := f.mintTx(testOrigin, "main", content.root)
	tx.AddOutput(&transaction.TransactionOutput{Satoshis: 1, LockingScript: &script.Script{script.OpTRUE}})
	tx.AddOutput(&transaction.TransactionOutput{Satoshis: 0, LockingScript: f.headScript(testOrigin, "zero-sat", content.root, "")})
	tx.AddOutput(&transaction.TransactionOutput{Satoshis: 1, LockingScript: f.headScript(testOrigin, "dev", content.root, "")})
	// A head whose root is nowhere in the submission: refused while its
	// siblings are admitted.
	tx.AddOutput(&transaction.TransactionOutput{Satoshis: 1, LockingScript: f.headScript(testOrigin, "ghost", testRoot1, "")})

	beef := transaction.NewBeef()
	for _, m := range []*transaction.Transaction{content.tx, tx} {
		if _, err := beef.MergeTransaction(m); err != nil {
			t.Fatal(err)
		}
	}
	got, err := (&TopicManager{}).IdentifyAdmissibleOutputs(t.Context(), beef, tx.TxID(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.OutputsToAdmit) != 2 || got.OutputsToAdmit[0] != 0 || got.OutputsToAdmit[1] != 3 {
		t.Fatalf("admitted %v, want [0 3]", got.OutputsToAdmit)
	}
	if len(got.AncillaryTxids) != 1 || *got.AncillaryTxids[0] != *content.tx.TxID() {
		t.Fatalf("ancillary = %v, want the content transaction once", got.AncillaryTxids)
	}
	if got.CoinsToRetain != nil {
		t.Fatalf("retained %v, want none", got.CoinsToRetain)
	}
	if _, err := (&TopicManager{}).IdentifyAdmissibleOutputs(t.Context(), beef, &chainhash.Hash{}, nil); err == nil {
		t.Fatal("expected error for missing transaction")
	}
}

func TestMintPushAndHistory(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()

	first := publish(t, 0x01, []string{testCommit}, nil, testCommit)
	mint := f.mintTx(testOrigin, "main", first.root)
	f.admit(0, first.tx, mint)

	head, err := f.store.GetHead(ctx, op(mint, 0))
	if err != nil {
		t.Fatal(err)
	}
	if head.Origin != testOrigin || head.Branch != "main" || head.Root != first.root || head.Identity != f.identityHex() {
		t.Fatalf("head = %+v", head)
	}
	// The commit is no longer on the token: it is the tip of the root's
	// `.git` store, read out of the submission.
	if head.Commit == nil || head.Commit.SHA != sha(testCommit) || head.Commit.Message != "first\n" {
		t.Fatalf("head commit = %+v", head.Commit)
	}
	if head.Prev != "" || head.Spend != nil || head.BranchedFrom != "" {
		t.Fatalf("head = %+v", head)
	}

	// Push: spend the mint, new root, citing the commit already on chain.
	second := commitObject("second", sha(testCommit))
	next := publish(t, 0x02, []string{second}, map[string]string{sha(testCommit): first.objects[sha(testCommit)]}, second)
	push := f.spendTx(mint, f.headScript(testOrigin, "main", next.root, ""))
	f.admit(0, next.tx, mint, push)

	pushed, err := f.store.GetHead(ctx, op(push, 0))
	if err != nil {
		t.Fatal(err)
	}
	if pushed.Prev != op(mint, 0) || pushed.Root != next.root || pushed.Spend != nil {
		t.Fatalf("next = %+v", pushed)
	}
	if pushed.Commit == nil || pushed.Commit.SHA != sha(second) {
		t.Fatalf("next commit = %+v", pushed.Commit)
	}
	prev, err := f.store.GetHead(ctx, op(mint, 0))
	if err != nil {
		t.Fatal(err)
	}
	if prev.Spend == nil || prev.Spend.Txid != push.TxID().String() || prev.Spend.Next != op(push, 0) {
		t.Fatalf("prev spend = %+v", prev.Spend)
	}
	// The spend path rewrites the row from the token alone; the commit it
	// indexed at admission must survive that.
	if prev.Commit == nil || prev.Commit.SHA != sha(testCommit) {
		t.Fatalf("prev commit after spend = %+v", prev.Commit)
	}

	current, err := f.store.ListHeads(ctx, HeadFilter{Origin: testOrigin, Unspent: true, Rev: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(current) != 1 || current[0].Outpoint != op(push, 0) {
		t.Fatalf("current heads = %+v", current)
	}
	history, err := f.store.ListHeads(ctx, HeadFilter{Origin: testOrigin, Branch: "main", Rev: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].Outpoint != op(push, 0) || history[1].Outpoint != op(mint, 0) {
		t.Fatalf("history = %+v", history)
	}

	repos, err := f.store.ListRepos(ctx, "", 0, 10, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 || repos[0].Origin != testOrigin || repos[0].Owner != f.identityHex() ||
		repos[0].FirstOutpoint != op(mint, 0) || repos[0].Heads != 2 || repos[0].Branches != 1 {
		t.Fatalf("repos = %+v", repos)
	}
	byIdentity, err := f.store.ListRepos(ctx, f.identityHex(), 0, 10, true)
	if err != nil || len(byIdentity) != 1 {
		t.Fatalf("repos by identity = %+v err=%v", byIdentity, err)
	}
	other, _ := ec.PrivateKeyFromHex("0000000000000000000000000000000000000000000000000000000000000009")
	none, err := f.store.ListRepos(ctx, hex.EncodeToString(other.PubKey().Compressed()), 0, 10, true)
	if err != nil || len(none) != 0 {
		t.Fatalf("repos for stranger = %+v err=%v", none, err)
	}
}

// The commits a push publishes are indexed from its `.git` store, held or
// only named: one hop verified, every hop recorded.
func TestCommitsIndexedFromTheObjectStore(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()

	second := commitObject("second", sha(testCommit))
	first := publish(t, 0x03, []string{testCommit}, nil, testCommit)
	mint := f.mintTx(testOrigin, "main", first.root)
	f.admit(0, first.tx, mint)

	// The next push republishes nothing but its own commit: the first is
	// cited at the outpoint that already holds it.
	next := publish(t, 0x04, []string{second}, map[string]string{sha(testCommit): first.objects[sha(testCommit)]}, second)
	push := f.spendTx(mint, f.headScript(testOrigin, "main", next.root, ""))
	// Deliberately without the first content transaction: the overlay does
	// not hold that hop, and must not need it.
	f.admit(0, next.tx, mint, push)

	held, err := f.store.GetCommit(ctx, sha(second))
	if err != nil {
		t.Fatal(err)
	}
	if !held.Held || held.Commit == nil || held.Commit.Message != "second\n" || held.Outpoint != op(push, 0) {
		t.Fatalf("second commit = %+v", held)
	}
	if len(held.Commit.Parents) != 1 || held.Commit.Parents[0] != sha(testCommit) {
		t.Fatalf("second parents = %v", held.Commit.Parents)
	}
	// The first commit is held because its own push carried it; the second
	// push's citation of it does not downgrade that.
	cited, err := f.store.GetCommit(ctx, sha(testCommit))
	if err != nil {
		t.Fatal(err)
	}
	if !cited.Held || cited.Outpoint != op(mint, 0) || cited.Ref != first.objects[sha(testCommit)] {
		t.Fatalf("first commit = %+v", cited)
	}

	// A push that only ever cites a commit records the hop without the body.
	third := commitObject("third", sha(second))
	stranger := commitObject("stranger")
	// An outpoint in a transaction nothing in this submission carries: the
	// hop the overlay names without holding.
	strangerRef := testRoot1
	far := publish(t, 0x05, []string{third}, map[string]string{
		sha(second):     next.objects[sha(second)],
		sha(stranger):   strangerRef,
		sha(testCommit): first.objects[sha(testCommit)],
	}, third)
	push2 := f.spendTx(push, f.headScript(testOrigin, "main", far.root, ""))
	f.admit(0, far.tx, push, push2)

	hole, err := f.store.GetCommit(ctx, sha(stranger))
	if err != nil {
		t.Fatal(err)
	}
	if hole.Held || hole.Commit != nil || hole.Ref != strangerRef || hole.Outpoint != op(push2, 0) {
		t.Fatalf("unheld commit = %+v", hole)
	}

	// A commit is content-addressed, so a row belongs to the head that first
	// published or named it: the last push adds the two the index had never
	// seen, and does not take the others over.
	named, err := f.store.ListCommitsForHead(ctx, op(push2, 0), 10)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, rec := range named {
		got[rec.Sha] = rec.Held
	}
	if len(named) != 2 || !got[sha(third)] || got[sha(stranger)] {
		t.Fatalf("commits credited to the last push = %+v", named)
	}
}

func TestSpendBeforeAdmitAndBurn(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()

	first := publish(t, 0x06, []string{testCommit}, nil, testCommit)
	second := commitObject("second", sha(testCommit))
	next := publish(t, 0x07, []string{second}, nil, second)

	mint := f.mintTx(testOrigin, "main", first.root)
	push := f.spendTx(mint, f.headScript(testOrigin, "main", next.root, ""))

	// The push arrives first: admission of the successor records the
	// predecessor (from the BEEF) as spent.
	f.admit(0, next.tx, mint, push)
	prev, err := f.store.GetHead(ctx, op(mint, 0))
	if err != nil {
		t.Fatal(err)
	}
	if prev.Spend == nil || prev.Spend.Next != op(push, 0) {
		t.Fatalf("prev = %+v", prev)
	}
	// Nothing read the predecessor's own push yet, so it has no commit.
	if prev.Commit != nil {
		t.Fatalf("prev commit before its own admission = %+v", prev.Commit)
	}
	// Late admission of the mint fills the commit in and must not clear the
	// spend.
	f.admit(0, first.tx, mint)
	prev, _ = f.store.GetHead(ctx, op(mint, 0))
	if prev.Spend == nil || prev.Spend.Next != op(push, 0) {
		t.Fatalf("prev after late admit = %+v", prev)
	}
	if prev.Commit == nil || prev.Commit.SHA != sha(testCommit) {
		t.Fatalf("prev commit after late admit = %+v", prev.Commit)
	}

	// Burn: spend the push head with no successor. Nothing is admitted, so
	// the engine's OutputSpent (or SpendSync.RecordSpends) is the only path.
	burn := f.spendTx(push)
	if err := f.svc.OutputSpent(ctx, &engine.OutputSpent{
		Outpoint:           &transaction.Outpoint{Txid: *push.TxID(), Index: 0},
		Topic:              TopicName,
		SpendingTxid:       burn.TxID(),
		SpendingAtomicBEEF: atomicBeef(t, mint, push, burn),
	}); err != nil {
		t.Fatal(err)
	}
	burned, err := f.store.GetHead(ctx, op(push, 0))
	if err != nil {
		t.Fatal(err)
	}
	if burned.Spend == nil || burned.Spend.Txid != burn.TxID().String() || burned.Spend.Next != "" {
		t.Fatalf("burned = %+v", burned.Spend)
	}
	current, _ := f.store.ListHeads(ctx, HeadFilter{Origin: testOrigin, Unspent: true})
	if len(current) != 0 {
		t.Fatalf("current after burn = %+v", current)
	}

	// RecordSpends is idempotent and independent of admission.
	beef, txid, _ := transaction.NewBeefFromAtomicBytes(atomicBeef(t, mint, push, burn))
	n, err := f.svc.RecordSpends(ctx, beef.FindTransactionForSigningByHash(txid), txid)
	if err != nil || n != 1 {
		t.Fatalf("RecordSpends = %d, %v", n, err)
	}
}

func TestBlockHeightRestampAndEviction(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	content := publish(t, 0x08, []string{testCommit}, nil, testCommit)
	mint := f.mintTx(testOrigin, "main", content.root)
	f.admit(0, content.tx, mint)

	before, _ := f.store.GetHead(ctx, op(mint, 0))
	if before.Height != 0 || before.Score < 1e9 {
		t.Fatalf("mempool score = %v height=%d", before.Score, before.Height)
	}
	if err := f.svc.OutputBlockHeightUpdated(ctx, mint.TxID(), 900000, 7); err != nil {
		t.Fatal(err)
	}
	after, _ := f.store.GetHead(ctx, op(mint, 0))
	if after.Height != 900000 || after.Score != 900000.000000007 {
		t.Fatalf("mined score = %v height=%d", after.Score, after.Height)
	}
	// A replayed admission (mempool score) must not undo the mined score.
	f.admit(0, content.tx, mint)
	again, _ := f.store.GetHead(ctx, op(mint, 0))
	if again.Height != 900000 {
		t.Fatalf("score after replay = %v", again.Score)
	}

	if err := f.svc.OutputEvicted(ctx, &transaction.Outpoint{Txid: *mint.TxID(), Index: 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.GetHead(ctx, op(mint, 0)); err == nil {
		t.Fatal("expected head to be evicted")
	}
	// A transaction the chain unmade published nothing.
	if _, err := f.store.GetCommit(ctx, sha(testCommit)); err == nil {
		t.Fatal("expected the evicted head's commits to go with it")
	}
}

func TestLookupQueries(t *testing.T) {
	f := newFixture(t)
	first := publish(t, 0x09, []string{testCommit}, nil, testCommit)
	mint := f.mintTx(testOrigin, "main", first.root)
	f.admit(0, first.tx, mint)

	second := commitObject("second", sha(testCommit))
	next := publish(t, 0x0a, []string{second}, nil, second)
	push := f.spendTx(mint, f.headScript(testOrigin, "main", next.root, ""))
	f.admit(0, next.tx, mint, push)

	forked := publish(t, 0x0b, []string{testCommit}, nil, testCommit)
	other := f.mintTx(testOrigin2, "main", forked.root)
	f.admit(0, forked.tx, other)

	ask := func(query string) []string {
		t.Helper()
		ans, err := f.svc.Lookup(t.Context(), &overlaylookup.LookupQuestion{Service: LookupName, Query: json.RawMessage(query)})
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, formula := range ans.Formulas {
			out = append(out, formula.Outpoint.OrdinalString())
		}
		return out
	}

	if got := ask(`{}`); len(got) != 2 {
		t.Fatalf("all current = %v", got)
	}
	if got := ask(`{"origin":"` + testOrigin + `"}`); len(got) != 1 || got[0] != op(push, 0) {
		t.Fatalf("by origin = %v", got)
	}
	if got := ask(`{"origin":"` + testOrigin + `","includeSpent":true}`); len(got) != 2 {
		t.Fatalf("with spent = %v", got)
	}
	if got := ask(`{"outpoint":"` + op(mint, 0) + `"}`); len(got) != 1 || got[0] != op(mint, 0) {
		t.Fatalf("by outpoint = %v", got)
	}
	if got := ask(`{"sha":"` + sha(second) + `","includeSpent":true}`); len(got) != 1 || got[0] != op(push, 0) {
		t.Fatalf("by sha = %v", got)
	}
	if got := ask(`{"identity":"` + f.identityHex() + `","limit":1}`); len(got) != 1 {
		t.Fatalf("by identity limit 1 = %v", got)
	}
	if _, err := f.svc.Lookup(t.Context(), &overlaylookup.LookupQuestion{Service: "ls_other"}); err == nil {
		t.Fatal("expected error for wrong service")
	}
}

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
	testRoot1   = "e6f27ce723b2923e93227ecef64b6cecf9464bd0c6ba71a66502aa20c5de82a1_3"
	testRoot2   = "83c55ad839d8bca1042909663b6a1d7468e7dfd71c67cd43d52363a254359138_1"
	testCommit  = "tree 4b825dc642cb6eb9a060e54bf8d69288fbee4904\nauthor A <a@x> 1700000000 +0000\ncommitter A <a@x> 1700000000 +0000\n\nfirst\n"
)

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

func (f *fixture) headScript(origin, branch, root string, commit string) *script.Script {
	f.t.Helper()
	fields, err := gibtpl.Fields(origin, branch, root, f.identity)
	if err != nil {
		f.t.Fatal(err)
	}
	s, err := gibtpl.LockingScript(f.lockKey, fields, []byte(commit), "application/x-git-commit")
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}

// mintTx creates a head with no gib inputs.
func (f *fixture) mintTx(origin, branch, root string) *transaction.Transaction {
	tx := transaction.NewTransaction()
	tx.AddOutput(&transaction.TransactionOutput{Satoshis: 1, LockingScript: f.headScript(origin, branch, root, testCommit)})
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

func (f *fixture) admit(vout uint32, txs ...*transaction.Transaction) {
	f.t.Helper()
	if err := f.svc.OutputAdmittedByTopic(f.t.Context(), &engine.OutputAdmittedByTopic{
		Topic:       TopicName,
		OutputIndex: vout,
		AtomicBEEF:  atomicBeef(f.t, txs...),
	}); err != nil {
		f.t.Fatal(err)
	}
}

func op(tx *transaction.Transaction, vout uint32) string {
	return (&transaction.Outpoint{Txid: *tx.TxID(), Index: vout}).OrdinalString()
}

func TestTopicManagerAdmitsHeads(t *testing.T) {
	f := newFixture(t)
	tx := f.mintTx(testOrigin, "main", testRoot1)
	tx.AddOutput(&transaction.TransactionOutput{Satoshis: 1, LockingScript: &script.Script{script.OpTRUE}})
	tx.AddOutput(&transaction.TransactionOutput{Satoshis: 0, LockingScript: f.headScript(testOrigin, "zero-sat", testRoot1, "")})
	tx.AddOutput(&transaction.TransactionOutput{Satoshis: 1, LockingScript: f.headScript(testOrigin, "dev", testRoot1, "")})

	beef := transaction.NewBeef()
	if _, err := beef.MergeTransaction(tx); err != nil {
		t.Fatal(err)
	}
	got, err := (&TopicManager{}).IdentifyAdmissibleOutputs(t.Context(), beef, tx.TxID(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.OutputsToAdmit) != 2 || got.OutputsToAdmit[0] != 0 || got.OutputsToAdmit[1] != 3 {
		t.Fatalf("admitted %v, want [0 3]", got.OutputsToAdmit)
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

	mint := f.mintTx(testOrigin, "main", testRoot1)
	f.admit(0, mint)

	head, err := f.store.GetHead(ctx, op(mint, 0))
	if err != nil {
		t.Fatal(err)
	}
	if head.Origin != testOrigin || head.Branch != "main" || head.Root != testRoot1 || head.Identity != f.identityHex() {
		t.Fatalf("head = %+v", head)
	}
	if head.Commit == nil || head.Commit.Message != "first\n" || head.Prev != "" || head.Spend != nil {
		t.Fatalf("head = %+v commit=%+v", head, head.Commit)
	}

	// Push: spend the mint, new root.
	push := f.spendTx(mint, f.headScript(testOrigin, "main", testRoot2, testCommit))
	f.admit(0, mint, push)

	next, err := f.store.GetHead(ctx, op(push, 0))
	if err != nil {
		t.Fatal(err)
	}
	if next.Prev != op(mint, 0) || next.Root != testRoot2 || next.Spend != nil {
		t.Fatalf("next = %+v", next)
	}
	prev, err := f.store.GetHead(ctx, op(mint, 0))
	if err != nil {
		t.Fatal(err)
	}
	if prev.Spend == nil || prev.Spend.Txid != push.TxID().String() || prev.Spend.Next != op(push, 0) {
		t.Fatalf("prev spend = %+v", prev.Spend)
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

func TestSpendBeforeAdmitAndBurn(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()

	mint := f.mintTx(testOrigin, "main", testRoot1)
	push := f.spendTx(mint, f.headScript(testOrigin, "main", testRoot2, testCommit))

	// The push arrives first: admission of the successor records the
	// predecessor (from the BEEF) as spent.
	f.admit(0, mint, push)
	prev, err := f.store.GetHead(ctx, op(mint, 0))
	if err != nil {
		t.Fatal(err)
	}
	if prev.Spend == nil || prev.Spend.Next != op(push, 0) || prev.Commit == nil {
		t.Fatalf("prev = %+v", prev)
	}
	// Late admission of the mint must not clear the spend.
	f.admit(0, mint)
	prev, _ = f.store.GetHead(ctx, op(mint, 0))
	if prev.Spend == nil || prev.Spend.Next != op(push, 0) {
		t.Fatalf("prev after late admit = %+v", prev)
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
	mint := f.mintTx(testOrigin, "main", testRoot1)
	f.admit(0, mint)

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
	f.admit(0, mint)
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
}

func TestLookupQueries(t *testing.T) {
	f := newFixture(t)
	mint := f.mintTx(testOrigin, "main", testRoot1)
	f.admit(0, mint)
	push := f.spendTx(mint, f.headScript(testOrigin, "main", testRoot2, testCommit))
	f.admit(0, mint, push)
	other := f.mintTx(testOrigin2, "main", testRoot1)
	f.admit(0, other)

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
	if got := ask(`{"identity":"` + f.identityHex() + `","limit":1}`); len(got) != 1 {
		t.Fatalf("by identity limit 1 = %v", got)
	}
	if _, err := f.svc.Lookup(t.Context(), &overlaylookup.LookupQuestion{Service: "ls_other"}); err == nil {
		t.Fatal("expected error for wrong service")
	}
}

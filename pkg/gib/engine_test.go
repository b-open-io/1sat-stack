package gib

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/b-open-io/1sat-stack/pkg/overlay"
	overlaystorage "github.com/b-open-io/1sat-stack/pkg/overlay/storage"
	gibtpl "github.com/b-open-io/1sat-stack/pkg/template/gib"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	sdkoverlay "github.com/bsv-blockchain/go-sdk/overlay"
	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/spv"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/chaintracker"
	sighash "github.com/bsv-blockchain/go-sdk/transaction/sighash"
	"github.com/bsv-blockchain/go-sdk/transaction/template/p2pkh"
	"github.com/gofiber/fiber/v2"
	"github.com/spf13/viper"
)

// The engine round trip: real transactions (signed, SPV-verifiable) submitted
// through the module engine must land in gib_heads with the spend chain
// linked, exactly as the OverlaySync worker drives it in production. A push
// submits its content transactions alongside the head, because admission
// reads the push out of the submission and nothing else.

type party struct {
	key    *ec.PrivateKey
	lock   *script.Script
	unlock *p2pkh.P2PKH
}

func newParty(t *testing.T, seed byte) party {
	t.Helper()
	key, _ := ec.PrivateKeyFromBytes(bytes.Repeat([]byte{seed}, 32))
	addr, err := script.NewAddressFromPublicKey(key.PubKey(), true)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := p2pkh.Lock(addr)
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := p2pkh.Unlock(key, nil)
	if err != nil {
		t.Fatal(err)
	}
	return party{key, lock, unlock}
}

// prove attaches a single-leaf merkle path so SPV treats tx as mined; the
// test chain tracker accepts any root.
func prove(tx *transaction.Transaction, height uint32) {
	isTxid := true
	tx.MerklePath = transaction.NewMerklePath(height, [][]*transaction.PathElement{{
		{Offset: 0, Hash: tx.TxID(), Txid: &isTxid},
	}})
}

func fundingTx(t *testing.T, p party, sats ...uint64) *transaction.Transaction {
	t.Helper()
	tx := transaction.NewTransaction()
	if err := tx.AddInputFrom("0000000000000000000000000000000000000000000000000000000000000001", 0, "76a914000000000000000000000000000000000000000088ac", 1, nil); err != nil {
		t.Fatal(err)
	}
	tx.Inputs[0].UnlockingScript = &script.Script{}
	for _, s := range sats {
		tx.AddOutput(&transaction.TransactionOutput{Satoshis: s, LockingScript: p.lock})
	}
	return tx
}

// signHeadInput signs a lock-before PushDrop input (<pubkey> OP_CHECKSIG ...)
// with the locking key: the unlocking script is just the signature.
func signHeadInput(t *testing.T, tx *transaction.Transaction, vin uint32, key *ec.PrivateKey) {
	t.Helper()
	flag := sighash.AllForkID
	hash, err := tx.CalcInputSignatureHash(vin, flag)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := key.Sign(hash)
	if err != nil {
		t.Fatal(err)
	}
	unlock := &script.Script{}
	if err := unlock.AppendPushData(append(sig.Serialize(), byte(flag))); err != nil {
		t.Fatal(err)
	}
	tx.Inputs[vin].UnlockingScript = unlock
}

// submitTx submits tx as the subject of one BEEF, preceded by the content
// transactions the push it publishes lives in.
func submitTx(t *testing.T, eng *engine.Engine, tx *transaction.Transaction, content ...*transaction.Transaction) sdkoverlay.Steak {
	t.Helper()
	steak, err := eng.Submit(t.Context(), sdkoverlay.TaggedBEEF{
		Beef:   submissionBeef(t, append(append([]*transaction.Transaction{}, content...), tx)...),
		Topics: []string{TopicName},
	}, engine.SubmitModeHistorical, nil)
	if err != nil {
		t.Fatalf("submit %s: %v", tx.TxID(), err)
	}
	return steak
}

// engineEnv is a module engine with a publisher holding enough funding
// outputs to mint several heads.
type engineEnv struct {
	t        *testing.T
	svc      *Services
	party    party
	lockKey  *ec.PrivateKey
	identity *ec.PublicKey
	funding  *transaction.Transaction
	spent    uint32
}

func newEngineEnv(t *testing.T, seed byte) *engineEnv {
	t.Helper()
	factory, err := overlaystorage.NewSQLiteFactory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = factory.Close() })
	v := viper.New()
	var cfg Config
	cfg.SetDefaults(v, "")
	v.Set("mode", ModeEmbedded)
	if err := v.Unmarshal(&cfg); err != nil {
		t.Fatal(err)
	}
	svc, err := cfg.Initialize(t.Context(), nil, &overlay.ModuleDeps{
		Factory:      factory.Factory(),
		ChainTracker: chaintracker.ChainTracker(&spv.GullibleHeadersClient{}),
	})
	if err != nil {
		t.Fatal(err)
	}
	identity, _ := ec.PrivateKeyFromHex("0000000000000000000000000000000000000000000000000000000000000002")
	p := newParty(t, seed)
	funding := fundingTx(t, p, 5000, 5000, 5000, 5000, 5000, 5000, 5000, 5000)
	prove(funding, 900000)
	return &engineEnv{
		t: t, svc: svc, party: p,
		lockKey:  newParty(t, seed+1).key,
		identity: identity.PubKey(),
		funding:  funding,
	}
}

func (e *engineEnv) headScript(branch, root, branchedFrom string) *script.Script {
	e.t.Helper()
	fields, err := gibtpl.Fields(testOrigin, branch, root, e.identity, branchedFrom)
	if err != nil {
		e.t.Fatal(err)
	}
	s, err := gibtpl.LockingScript(e.lockKey.PubKey(), fields)
	if err != nil {
		e.t.Fatal(err)
	}
	return s
}

// headTx mints a head, optionally spending the branch's previous one.
func (e *engineEnv) headTx(branch, root, branchedFrom string, prev *transaction.Transaction) *transaction.Transaction {
	e.t.Helper()
	tx := transaction.NewTransaction()
	if prev != nil {
		tx.AddInputFromTx(prev, 0, nil)
	}
	tx.AddInputFromTx(e.funding, e.spent, e.party.unlock)
	e.spent++
	tx.AddOutput(&transaction.TransactionOutput{Satoshis: 1, LockingScript: e.headScript(branch, root, branchedFrom)})
	tx.AddOutput(&transaction.TransactionOutput{Satoshis: 4000, LockingScript: e.party.lock})
	if err := tx.Sign(); err != nil {
		e.t.Fatal(err)
	}
	if prev != nil {
		signHeadInput(e.t, tx, 0, e.lockKey)
	}
	return tx
}

func admitted(steak sdkoverlay.Steak) []uint32 {
	if s := steak[TopicName]; s != nil {
		return s.OutputsToAdmit
	}
	return nil
}

func TestEngineRoundTrip(t *testing.T) {
	e := newEngineEnv(t, 0x01)
	svc, ctx := e.svc, t.Context()

	// Mint: content transaction plus a head pointing at its root.
	first := publish(t, 0x41, []string{testCommit}, nil, testCommit)
	mint := e.headTx("main", first.root, "", nil)

	steak := submitTx(t, svc.Engine, mint, first.tx)
	if got := admitted(steak); len(got) != 1 || got[0] != 0 {
		t.Fatalf("mint admission = %+v, want output 0", steak[TopicName])
	}
	// The content transaction the head was judged against is retained with
	// it, instead of being discarded once the submission is over.
	anc := steak[TopicName].AncillaryTxids
	if len(anc) != 1 || *anc[0] != *first.tx.TxID() {
		t.Fatalf("ancillary txids = %v, want [%s]", anc, first.tx.TxID())
	}
	minted, err := svc.Store.GetHead(ctx, op(mint, 0))
	if err != nil {
		t.Fatal(err)
	}
	if minted.Branch != "main" || minted.Root != first.root || minted.Spend != nil {
		t.Fatalf("minted head = %+v", minted)
	}
	if minted.Commit == nil || minted.Commit.SHA != sha(testCommit) || minted.Commit.Message != "first\n" {
		t.Fatalf("minted commit = %+v", minted.Commit)
	}

	// Push: head + change → new head + change, citing the commit already
	// published instead of writing it again.
	second := commitObject("second", sha(testCommit))
	next := publish(t, 0x42, []string{second}, map[string]string{sha(testCommit): first.objects[sha(testCommit)]}, second)
	push := e.headTx("main", next.root, "", mint)

	steak = submitTx(t, svc.Engine, push, next.tx)
	if got := admitted(steak); len(got) != 1 {
		t.Fatalf("push admission = %+v", steak[TopicName])
	}
	pushed, _ := svc.Store.GetHead(ctx, op(push, 0))
	if pushed == nil || pushed.Prev != op(mint, 0) || pushed.Root != next.root {
		t.Fatalf("pushed head = %+v", pushed)
	}
	if pushed.Commit == nil || pushed.Commit.SHA != sha(second) {
		t.Fatalf("pushed commit = %+v", pushed.Commit)
	}
	minted, _ = svc.Store.GetHead(ctx, op(mint, 0))
	if minted.Spend == nil || minted.Spend.Txid != push.TxID().String() || minted.Spend.Next != op(push, 0) {
		t.Fatalf("minted spend = %+v", minted.Spend)
	}

	// A publisher stops extending the branch by spending its own head, which
	// creates no successor. The overlay learns of it only because the client
	// hands it the spending transaction: nothing watches the chain for one.
	// The branch's history stays exactly as it was — this only takes the
	// head off the list of current ones.
	burn := transaction.NewTransaction()
	burn.AddInputFromTx(push, 0, nil)
	burn.AddInputFromTx(push, 1, e.party.unlock)
	burn.AddOutput(&transaction.TransactionOutput{Satoshis: 2000, LockingScript: e.party.lock})
	if err := burn.Sign(); err != nil {
		t.Fatal(err)
	}
	signHeadInput(t, burn, 0, e.lockKey)
	submitTx(t, svc.Engine, burn)
	burned, _ := svc.Store.GetHead(ctx, op(push, 0))
	if burned.Spend == nil || burned.Spend.Txid != burn.TxID().String() || burned.Spend.Next != "" {
		t.Fatalf("burned spend = %+v", burned.Spend)
	}
	// The history is not retracted: both heads are still served.
	history, err := svc.Store.ListHeads(ctx, HeadFilter{Origin: testOrigin, Branch: "main", Rev: true})
	if err != nil || len(history) != 2 {
		t.Fatalf("history after the burn = %+v, %v", history, err)
	}

	// REST view of the same state.
	app := fiber.New()
	svc.Routes.Register(app.Group("/gib"))
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/gib/repo/"+testOrigin, nil))
	if err != nil {
		t.Fatal(err)
	}
	var repo RepoResponse
	if err := json.NewDecoder(resp.Body).Decode(&repo); err != nil {
		t.Fatal(err)
	}
	if repo.Heads != 2 || repo.Branches != 1 || len(repo.HeadsList) != 0 {
		t.Fatalf("repo = %+v", repo)
	}
	resp, _ = app.Test(httptest.NewRequest(http.MethodGet, "/gib/repo/"+testOrigin+"/branch/main", nil))
	var branch BranchResponse
	if err := json.NewDecoder(resp.Body).Decode(&branch); err != nil {
		t.Fatal(err)
	}
	if branch.Head != nil || len(branch.History) != 2 || branch.History[0].Outpoint != op(push, 0) {
		t.Fatalf("branch = %+v", branch)
	}
}

// Admission through a real engine, case by case: what a submission must
// carry for a head to be admitted, and what it deliberately need not.
func TestEngineAdmissionRules(t *testing.T) {
	t.Run("root transaction absent", func(t *testing.T) {
		e := newEngineEnv(t, 0x11)
		content := publish(t, 0x51, []string{testCommit}, nil, testCommit)
		head := e.headTx("main", content.root, "", nil)
		// Everything is well formed; the content transaction is simply not
		// in the submission, so nothing verifies the root.
		steak := submitTx(t, e.svc.Engine, head)
		if got := admitted(steak); len(got) != 0 {
			t.Fatalf("admitted %v, want none", got)
		}
		if _, err := e.svc.Store.GetHead(t.Context(), op(head, 0)); err == nil {
			t.Fatal("refused head was indexed")
		}
	})

	t.Run("root without a .git store", func(t *testing.T) {
		e := newEngineEnv(t, 0x21)
		tree := publishRootOnly(t, 0x52)
		head := e.headTx("main", tree.root, "", nil)
		steak := submitTx(t, e.svc.Engine, head, tree.tx)
		if got := admitted(steak); len(got) != 0 {
			t.Fatalf("admitted %v, want none", got)
		}
	})

	t.Run("root that is not a directory", func(t *testing.T) {
		e := newEngineEnv(t, 0x31)
		content := publish(t, 0x53, []string{testCommit}, nil, testCommit)
		// Output 0 of a content transaction is a commit object, not a root.
		head := e.headTx("main", op(content.tx, 0), "", nil)
		steak := submitTx(t, e.svc.Engine, head, content.tx)
		if got := admitted(steak); len(got) != 0 {
			t.Fatalf("admitted %v, want none", got)
		}
	})

	t.Run("branched-from head absent", func(t *testing.T) {
		e := newEngineEnv(t, 0x41)
		first := publish(t, 0x54, []string{testCommit}, nil, testCommit)
		mint := e.headTx("main", first.root, "", nil)
		submitTx(t, e.svc.Engine, mint, first.tx)

		branchTip := commitObject("on a branch", sha(testCommit))
		branch := publish(t, 0x55, []string{branchTip},
			map[string]string{sha(testCommit): first.objects[sha(testCommit)]}, branchTip)
		head := e.headTx("feature/x", branch.root, op(mint, 0), nil)

		// The head it forked from is not in this submission, even though the
		// overlay already holds it: admission reads the submission alone.
		steak := submitTx(t, e.svc.Engine, head, branch.tx)
		if got := admitted(steak); len(got) != 0 {
			t.Fatalf("admitted %v, want none", got)
		}

		// With the forked-from head present it is admitted, and both its
		// transactions are retained.
		head = e.headTx("feature/x", branch.root, op(mint, 0), nil)
		steak = submitTx(t, e.svc.Engine, head, branch.tx, mint)
		if got := admitted(steak); len(got) != 1 {
			t.Fatalf("admitted %v, want output 0", got)
		}
		anc := map[string]bool{}
		for _, h := range steak[TopicName].AncillaryTxids {
			anc[h.String()] = true
		}
		if !anc[branch.tx.TxID().String()] || !anc[mint.TxID().String()] || len(anc) != 2 {
			t.Fatalf("ancillary txids = %v", anc)
		}
		rec, err := e.svc.Store.GetHead(t.Context(), op(head, 0))
		if err != nil {
			t.Fatal(err)
		}
		if rec.BranchedFrom != op(mint, 0) {
			t.Fatalf("branched from = %q, want %s", rec.BranchedFrom, op(mint, 0))
		}
	})

	// The overlay is not asked to hold the whole history. A branch whose
	// `.git` cites commit objects in a transaction nothing in the
	// submission carries is admitted, and the hop is recorded.
	t.Run("a hole in the history is recorded, not refused", func(t *testing.T) {
		e := newEngineEnv(t, 0x51)
		ctx := t.Context()
		first := publish(t, 0x56, []string{testCommit}, nil, testCommit)
		mint := e.headTx("main", first.root, "", nil)
		submitTx(t, e.svc.Engine, mint, first.tx)

		branchTip := commitObject("on a branch", sha(testCommit))
		branch := publish(t, 0x57, []string{branchTip},
			map[string]string{sha(testCommit): first.objects[sha(testCommit)]}, branchTip)
		head := e.headTx("feature/x", branch.root, op(mint, 0), nil)

		// The forked-from head is here; the transaction holding the cited
		// commit object is not.
		steak := submitTx(t, e.svc.Engine, head, branch.tx, mint)
		if got := admitted(steak); len(got) != 1 || got[0] != 0 {
			t.Fatalf("admitted %v, want output 0", got)
		}
		for _, h := range steak[TopicName].AncillaryTxids {
			if *h == *first.tx.TxID() {
				t.Fatal("the cited commit object's transaction was claimed as ancillary")
			}
		}

		// The commit the branch published is held; the one it cited is
		// named, with the outpoint that holds it.
		held, err := e.svc.Store.GetCommit(ctx, sha(branchTip))
		if err != nil {
			t.Fatal(err)
		}
		if !held.Held || held.Commit == nil || held.Commit.Message != "on a branch\n" {
			t.Fatalf("branch tip = %+v", held)
		}
		cited, err := e.svc.Store.GetCommit(ctx, sha(testCommit))
		if err != nil {
			t.Fatal(err)
		}
		// This overlay does hold it — its own push published it — and the
		// citation must not have overwritten where it lives.
		if cited.Ref != first.objects[sha(testCommit)] {
			t.Fatalf("cited commit ref = %q, want %s", cited.Ref, first.objects[sha(testCommit)])
		}
	})

	// The same hole in an overlay that never saw the earlier push: the
	// commit is recorded as named-but-not-held.
	t.Run("a commit only ever cited is recorded unheld", func(t *testing.T) {
		e := newEngineEnv(t, 0x61)
		absent := commitObject("never submitted here")
		tip := commitObject("built on it", sha(absent))
		content := publish(t, 0x58, []string{tip},
			map[string]string{sha(absent): testRoot1}, tip)
		head := e.headTx("main", content.root, "", nil)
		if got := admitted(submitTx(t, e.svc.Engine, head, content.tx)); len(got) != 1 {
			t.Fatalf("admitted %v, want output 0", got)
		}
		rec, err := e.svc.Store.GetCommit(t.Context(), sha(absent))
		if err != nil {
			t.Fatal(err)
		}
		if rec.Held || rec.Commit != nil || rec.Ref != testRoot1 {
			t.Fatalf("unheld commit = %+v", rec)
		}
	})

	// A `.git` entry the submission carries must be the object its name
	// claims: the store is keyed by sha, so a name is proof of content.
	t.Run("a mislabelled commit object is refused", func(t *testing.T) {
		e := newEngineEnv(t, 0x71)
		content := publish(t, 0x59, []string{testCommit}, nil, testCommit)
		// Rewrite the commit object output, leaving the store naming the
		// old sha.
		content.tx.Outputs[0] = dataOutput(t, []byte(commitObject("tampered")), GitCommitContentType)
		content.tx.MerklePath = nil
		prove(content.tx, 900000)
		head := e.headTx("main", op(content.tx, 3), "", nil)
		if got := admitted(submitTx(t, e.svc.Engine, head, content.tx)); len(got) != 0 {
			t.Fatalf("admitted %v, want none", got)
		}
	})
}

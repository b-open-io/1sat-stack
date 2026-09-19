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
// linked, exactly as the OverlaySync worker drives it in production.

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

func submitTx(t *testing.T, eng *engine.Engine, tx *transaction.Transaction) sdkoverlay.Steak {
	t.Helper()
	atomic, err := tx.AtomicBEEF(false)
	if err != nil {
		t.Fatal(err)
	}
	steak, err := eng.Submit(t.Context(), sdkoverlay.TaggedBEEF{Beef: atomic, Topics: []string{TopicName}}, engine.SubmitModeHistorical, nil)
	if err != nil {
		t.Fatalf("submit %s: %v", tx.TxID(), err)
	}
	return steak
}

func TestEngineRoundTrip(t *testing.T) {
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
	ctx := t.Context()

	publisher := newParty(t, 0x01)
	identity, _ := ec.PrivateKeyFromHex("0000000000000000000000000000000000000000000000000000000000000002")
	lockKey := newParty(t, 0x03).key
	headScript := func(branch, root string) *script.Script {
		fields, err := gibtpl.Fields(testOrigin, branch, root, identity.PubKey())
		if err != nil {
			t.Fatal(err)
		}
		s, err := gibtpl.LockingScript(lockKey.PubKey(), fields, []byte(testCommit), "application/x-git-commit")
		if err != nil {
			t.Fatal(err)
		}
		return s
	}

	funding := fundingTx(t, publisher, 5000, 5000)
	prove(funding, 900000)

	// Mint: funding → head (1 sat) + change.
	mint := transaction.NewTransaction()
	mint.AddInputFromTx(funding, 0, publisher.unlock)
	mint.AddOutput(&transaction.TransactionOutput{Satoshis: 1, LockingScript: headScript("main", testRoot1)})
	mint.AddOutput(&transaction.TransactionOutput{Satoshis: 4000, LockingScript: publisher.lock})
	if err := mint.Sign(); err != nil {
		t.Fatal(err)
	}

	steak := submitTx(t, svc.Engine, mint)
	if s := steak[TopicName]; s == nil || len(s.OutputsToAdmit) != 1 || s.OutputsToAdmit[0] != 0 {
		t.Fatalf("mint admission = %+v, want output 0", steak[TopicName])
	}
	minted, err := svc.Store.GetHead(ctx, op(mint, 0))
	if err != nil {
		t.Fatal(err)
	}
	if minted.Branch != "main" || minted.Root != testRoot1 || minted.Commit == nil || minted.Spend != nil {
		t.Fatalf("minted head = %+v", minted)
	}

	// Push: head + change → new head + change.
	push := transaction.NewTransaction()
	push.AddInputFromTx(mint, 0, nil)
	push.AddInputFromTx(mint, 1, publisher.unlock)
	push.AddOutput(&transaction.TransactionOutput{Satoshis: 1, LockingScript: headScript("main", testRoot2)})
	push.AddOutput(&transaction.TransactionOutput{Satoshis: 3000, LockingScript: publisher.lock})
	if err := push.Sign(); err != nil {
		t.Fatal(err)
	}
	signHeadInput(t, push, 0, lockKey)

	steak = submitTx(t, svc.Engine, push)
	if s := steak[TopicName]; s == nil || len(s.OutputsToAdmit) != 1 {
		t.Fatalf("push admission = %+v", steak[TopicName])
	}
	pushed, _ := svc.Store.GetHead(ctx, op(push, 0))
	if pushed == nil || pushed.Prev != op(mint, 0) || pushed.Root != testRoot2 {
		t.Fatalf("pushed head = %+v", pushed)
	}
	minted, _ = svc.Store.GetHead(ctx, op(mint, 0))
	if minted.Spend == nil || minted.Spend.Txid != push.TxID().String() || minted.Spend.Next != op(push, 0) {
		t.Fatalf("minted spend = %+v", minted.Spend)
	}

	// Delete: spend the head with no successor. The engine sees no admissible
	// output; production also feeds spend events to SpendSync.RecordSpends.
	burn := transaction.NewTransaction()
	burn.AddInputFromTx(push, 0, nil)
	burn.AddInputFromTx(push, 1, publisher.unlock)
	burn.AddOutput(&transaction.TransactionOutput{Satoshis: 2000, LockingScript: publisher.lock})
	if err := burn.Sign(); err != nil {
		t.Fatal(err)
	}
	signHeadInput(t, burn, 0, lockKey)
	submitTx(t, svc.Engine, burn)
	if n, err := svc.Lookup.RecordSpends(ctx, burn, burn.TxID()); err != nil || n != 1 {
		t.Fatalf("RecordSpends = %d, %v", n, err)
	}
	burned, _ := svc.Store.GetHead(ctx, op(push, 0))
	if burned.Spend == nil || burned.Spend.Txid != burn.TxID().String() || burned.Spend.Next != "" {
		t.Fatalf("burned spend = %+v", burned.Spend)
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

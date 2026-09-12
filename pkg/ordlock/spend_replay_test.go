package ordlock

import (
	"bytes"
	"testing"

	"github.com/b-open-io/1sat-stack/pkg/overlay"
	overlaystorage "github.com/b-open-io/1sat-stack/pkg/overlay/storage"
	template "github.com/b-open-io/1sat-stack/pkg/template/ordlock"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	sdkoverlay "github.com/bsv-blockchain/go-sdk/overlay"
	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/script/interpreter"
	"github.com/bsv-blockchain/go-sdk/spv"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/chaintracker"
	sighash "github.com/bsv-blockchain/go-sdk/transaction/sighash"
	"github.com/bsv-blockchain/go-sdk/transaction/template/p2pkh"
	"github.com/spf13/viper"
)

// v2PurchaseFlag is the sighash the canonical purchase preimage is built
// under: SINGLE|ANYONECANPAY|FORKID. SIGHASH_SINGLE binds listing input i to
// the complete output i, which the contract requires to equal its payout.
const v2PurchaseFlag = sighash.Flag(0xc3)

type fixtureParty struct {
	key    *ec.PrivateKey
	addr   *script.Address
	lock   *script.Script
	unlock *p2pkh.P2PKH
}

func newFixtureParty(t *testing.T, seed byte) fixtureParty {
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
	return fixtureParty{key, addr, lock, unlock}
}

// v2Fixture is a deterministic listing + purchase pair in the canonical batch
// layout, standing in for the mainnet transactions of the earlier v2 draft.
//
//	purchase inputs:  0 front funding (F=1500) · 1 listing (1 sat) · 2 fee funding
//	purchase outputs: 0 cushion (F-P=500)     · 1 seller payout (P=1000) · 2 buyer ordinal (1 sat) · 3 fee change
//
// Front funding before the listing reserves output 0 so the payout lands at
// the listing's own index (SIGHASH_SINGLE), while first-sat ordering carries
// the listed satoshi into output 2. Fee funding trails the listing so it
// cannot shift that mapping.
type v2Fixture struct {
	listingParent, listing, buyFunding, purchase *transaction.Transaction
}

func dummyFundedTx(t *testing.T, outputs ...*transaction.TransactionOutput) *transaction.Transaction {
	t.Helper()
	tx := transaction.NewTransaction()
	// The parent is attached to the BEEF with a merkle path, so its own input
	// is never script-verified; any well-formed outpoint will do.
	if err := tx.AddInputFrom("0000000000000000000000000000000000000000000000000000000000000001", 0, "76a914000000000000000000000000000000000000000088ac", 1, nil); err != nil {
		t.Fatal(err)
	}
	tx.Inputs[0].UnlockingScript = &script.Script{}
	for _, o := range outputs {
		tx.AddOutput(o)
	}
	return tx
}

func buildV2Fixture(t *testing.T) *v2Fixture {
	t.Helper()
	seller, buyer := newFixtureParty(t, 0x01), newFixtureParty(t, 0x02)

	payout := &transaction.TransactionOutput{Satoshis: 1000, LockingScript: seller.lock}
	listingLock := fillV2(t, seller.addr.PublicKeyHash, payout.Bytes())

	listingParent := dummyFundedTx(t,
		&transaction.TransactionOutput{Satoshis: 1, LockingScript: seller.lock},
		&transaction.TransactionOutput{Satoshis: 5000, LockingScript: seller.lock},
	)
	listing := transaction.NewTransaction()
	listing.AddInputFromTx(listingParent, 0, seller.unlock)
	listing.AddInputFromTx(listingParent, 1, seller.unlock)
	listing.AddOutput(&transaction.TransactionOutput{Satoshis: 1, LockingScript: listingLock})
	listing.AddOutput(&transaction.TransactionOutput{Satoshis: 4000, LockingScript: seller.lock})
	if err := listing.Sign(); err != nil {
		t.Fatal(err)
	}

	buyFunding := dummyFundedTx(t,
		&transaction.TransactionOutput{Satoshis: 1500, LockingScript: buyer.lock},
		&transaction.TransactionOutput{Satoshis: 2000, LockingScript: buyer.lock},
	)
	purchase := transaction.NewTransaction()
	purchase.AddInputFromTx(buyFunding, 0, buyer.unlock) // 0 front funding
	purchase.AddInputFromTx(listing, 0, nil)             // 1 listing
	purchase.AddInputFromTx(buyFunding, 1, buyer.unlock) // 2 fee funding
	purchase.AddOutput(&transaction.TransactionOutput{Satoshis: 500, LockingScript: buyer.lock})
	purchase.AddOutput(payout)
	purchase.AddOutput(&transaction.TransactionOutput{Satoshis: 1, LockingScript: buyer.lock})
	purchase.AddOutput(&transaction.TransactionOutput{Satoshis: 1900, LockingScript: buyer.lock})
	if err := purchase.Sign(); err != nil {
		t.Fatal(err)
	}
	purchase.Inputs[1].UnlockingScript = v2PurchaseUnlock(t, purchase, 1)

	return &v2Fixture{listingParent, listing, buyFunding, purchase}
}

// v2PurchaseUnlock builds `<preimage> OP_0` for the listing spent at vin,
// hashing the scriptCode after the contract's OP_CODESEPARATOR.
func v2PurchaseUnlock(t *testing.T, tx *transaction.Transaction, vin int) *script.Script {
	t.Helper()
	src := tx.Inputs[vin].SourceTxOutput()
	lock := src.LockingScript
	subscript := script.NewFromBytes((*lock)[template.OrdLockV2CodeSeparatorIndex+1:])
	src.LockingScript = subscript
	preimage, err := tx.CalcInputPreimage(uint32(vin), v2PurchaseFlag)
	src.LockingScript = lock
	if err != nil {
		t.Fatal(err)
	}
	unlock := &script.Script{}
	if err := unlock.AppendPushData(preimage); err != nil {
		t.Fatal(err)
	}
	if err := unlock.AppendOpcodes(script.Op0); err != nil {
		t.Fatal(err)
	}
	return unlock
}

// prove attaches a single-leaf merkle path so the BEEF treats tx as a proven
// root; the test's chain tracker accepts any root.
func prove(tx *transaction.Transaction, height uint32) {
	isTxid := true
	tx.MerklePath = transaction.NewMerklePath(height, [][]*transaction.PathElement{{
		{Offset: 0, Hash: tx.TxID(), Txid: &isTxid},
	}})
}

func submit(t *testing.T, eng *engine.Engine, tx *transaction.Transaction) sdkoverlay.Steak {
	t.Helper()
	atomic, err := tx.AtomicBEEF(false)
	if err != nil {
		t.Fatal(err)
	}
	steak, err := eng.Submit(t.Context(), sdkoverlay.TaggedBEEF{Beef: atomic, Topics: []string{TopicNameV2}}, engine.SubmitModeHistorical, nil)
	if err != nil {
		t.Fatalf("submit %s: %v", tx.TxID(), err)
	}
	return steak
}

// A purchase submitted through the real module engine must flip the listing
// from active to sale in the market table (lookup OutputSpent → MarkSpent).
func TestV2PurchaseMarksListingSold(t *testing.T) {
	factory, err := overlaystorage.NewSQLiteFactory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = factory.Close() })
	v := viper.New()
	v.Set("mode", ModeEmbedded)
	var cfg Config
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

	fx := buildV2Fixture(t)
	prove(fx.listingParent, 965095)
	prove(fx.buyFunding, 966400)

	steak := submit(t, svc.Engine, fx.listing)
	if s := steak[TopicNameV2]; s == nil || len(s.OutputsToAdmit) != 1 || s.OutputsToAdmit[0] != 0 {
		t.Fatalf("listing admission = %+v, want output 0", steak[TopicNameV2])
	}
	active, err := svc.OrdLockV2.SearchListings(t.Context(), "active", "", "", 10, 0, true)
	if err != nil || len(active) != 1 {
		t.Fatalf("active listings after listing = %d (%v), want 1", len(active), err)
	}

	steak = submit(t, svc.Engine, fx.purchase)
	if s := steak[TopicNameV2]; s == nil || len(s.CoinsToRetain) != 1 || s.CoinsToRetain[0] != 1 {
		t.Fatalf("purchase admission = %+v, want input 1 retained", steak[TopicNameV2])
	}
	active, err = svc.OrdLockV2.SearchListings(t.Context(), "active", "", "", 10, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	sold, err := svc.OrdLockV2.SearchListings(t.Context(), "sale", "", "", 10, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 || len(sold) != 1 {
		t.Fatalf("after purchase: active=%d sold=%d, want 0/1", len(active), len(sold))
	}
}

// The purchase must validate under BSV consensus (Chronicle active, so
// OP_2MUL is enabled). The overlay engine runs go-sdk spv.Verify on every
// Submit; before go-sdk PR #360 that verifier omitted the Chronicle flag and
// rejected v2 purchases with "attempt to execute disabled opcode OP_2MUL".
// This pins the fixed behaviour so a go-sdk downgrade is caught here.
func TestV2PurchaseScriptsUnderSpvVerify(t *testing.T) {
	fx := buildV2Fixture(t)
	prove(fx.listingParent, 965095)
	prove(fx.buyFunding, 966400)

	// consensus rules (what the network applies)
	if err := interpreter.NewEngine().Execute(
		interpreter.WithTx(fx.purchase, 1, fx.listing.Outputs[0]),
		interpreter.WithForkID(), interpreter.WithAfterGenesis(), interpreter.WithAfterChronicle(),
	); err != nil {
		t.Fatalf("purchase input 1 must validate with Chronicle flags: %v", err)
	}

	// what the overlay engine runs
	ok, err := spv.Verify(t.Context(), fx.purchase, &spv.GullibleHeadersClient{}, nil)
	if err != nil || !ok {
		t.Fatalf("spv.Verify must accept the purchase (go-sdk must include Chronicle in spv.Verify): ok=%v err=%v", ok, err)
	}
}

// A purchase that pays the seller at the wrong index (the ordinal-first
// layout of the withdrawn v2 draft) must be rejected by the canonical
// contract: SIGHASH_SINGLE commits to output 1 for listing input 1.
func TestV2PurchaseRejectsPayoutAtWrongIndex(t *testing.T) {
	fx := buildV2Fixture(t)
	tx := fx.purchase
	tx.Outputs[0], tx.Outputs[1] = tx.Outputs[1], tx.Outputs[0]
	tx.Inputs[1].UnlockingScript = v2PurchaseUnlock(t, tx, 1)
	if err := interpreter.NewEngine().Execute(
		interpreter.WithTx(tx, 1, fx.listing.Outputs[0]),
		interpreter.WithForkID(), interpreter.WithAfterGenesis(), interpreter.WithAfterChronicle(),
	); err == nil {
		t.Fatal("payout at output 0 must not satisfy listing input 1")
	}
}

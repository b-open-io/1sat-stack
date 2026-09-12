package ordlock

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/b-open-io/1sat-stack/pkg/overlay"
	overlaystorage "github.com/b-open-io/1sat-stack/pkg/overlay/storage"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	sdkoverlay "github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/script/interpreter"
	"github.com/bsv-blockchain/go-sdk/spv"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/chaintracker"
	"github.com/spf13/viper"
)

// Mainnet fixtures from the 2026-09-12 live test: a v2 listing (Pixel Foxes
// #144078, 1000 sats) and its purchase, plus each one's funding parent.
const (
	fxListingParent = "3291ce0ad5773503e0704244c7b039d52172ac126b977f53706079d60c5218c9"
	fxListing       = "d9f450fb3b39d26cac51e75ae3f65fc387022d530fcf98f11ef266d94e46c3be"
	fxBuyFunding    = "aa4998096fcd0e1e3642a4a2a820a4717b672beaae551df40b12d557fe5625c1"
	fxPurchase      = "c3f3b272fa12be613483b0a13e90dc7ebf368168bc5459f5dead9982b4d10003"
)

func loadFixtureTx(t *testing.T, txid string) *transaction.Transaction {
	t.Helper()
	h, err := os.ReadFile(filepath.Join("testdata", txid+".hex"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := hex.DecodeString(string(h))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := transaction.NewTransactionFromBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	if tx.TxID().String() != txid {
		t.Fatalf("fixture %s parsed as %s", txid, tx.TxID())
	}
	return tx
}

// prove attaches a single-leaf merkle path so the BEEF treats tx as a proven
// root; the test's chain tracker accepts any root.
func prove(tx *transaction.Transaction, height uint32) {
	isTxid := true
	tx.MerklePath = transaction.NewMerklePath(height, [][]*transaction.PathElement{{
		{Offset: 0, Hash: tx.TxID(), Txid: &isTxid},
	}})
}

// link sets SourceTransaction on every input of tx that spends one of parents.
func link(tx *transaction.Transaction, parents ...*transaction.Transaction) {
	for _, in := range tx.Inputs {
		for _, p := range parents {
			if in.SourceTXID.Equal(*p.TxID()) {
				in.SourceTransaction = p
			}
		}
	}
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

	listingParent := loadFixtureTx(t, fxListingParent)
	listing := loadFixtureTx(t, fxListing)
	buyFunding := loadFixtureTx(t, fxBuyFunding)
	purchase := loadFixtureTx(t, fxPurchase)
	prove(listingParent, 965095)
	prove(buyFunding, 966400)
	link(listing, listingParent)
	link(purchase, listing, buyFunding)

	steak := submit(t, svc.Engine, listing)
	if s := steak[TopicNameV2]; s == nil || len(s.OutputsToAdmit) != 1 || s.OutputsToAdmit[0] != 0 {
		t.Fatalf("listing admission = %+v, want output 0", steak[TopicNameV2])
	}
	active, err := svc.OrdLockV2.SearchListings(t.Context(), "active", "", "", 10, 0, true)
	if err != nil || len(active) != 1 {
		t.Fatalf("active listings after listing = %d (%v), want 1", len(active), err)
	}

	steak = submit(t, svc.Engine, purchase)
	if s := steak[TopicNameV2]; s == nil || len(s.CoinsToRetain) != 1 || s.CoinsToRetain[0] != 0 {
		t.Fatalf("purchase admission = %+v, want input 0 retained", steak[TopicNameV2])
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
// rejected this transaction with "attempt to execute disabled opcode OP_2MUL".
// This pins the fixed behaviour so a go-sdk downgrade is caught here.
func TestV2PurchaseScriptsUnderSpvVerify(t *testing.T) {
	listingParent := loadFixtureTx(t, fxListingParent)
	listing := loadFixtureTx(t, fxListing)
	buyFunding := loadFixtureTx(t, fxBuyFunding)
	purchase := loadFixtureTx(t, fxPurchase)
	prove(listingParent, 965095)
	prove(buyFunding, 966400)
	link(listing, listingParent)
	link(purchase, listing, buyFunding)

	// consensus rules (what the network applied when it accepted the tx)
	if err := interpreter.NewEngine().Execute(
		interpreter.WithTx(purchase, 0, listing.Outputs[0]),
		interpreter.WithForkID(), interpreter.WithAfterGenesis(), interpreter.WithAfterChronicle(),
	); err != nil {
		t.Fatalf("purchase input 0 must validate with Chronicle flags: %v", err)
	}

	// what the overlay engine runs
	ok, err := spv.Verify(t.Context(), purchase, &spv.GullibleHeadersClient{}, nil)
	if err != nil || !ok {
		t.Fatalf("spv.Verify must accept the purchase (go-sdk must include Chronicle in spv.Verify): ok=%v err=%v", ok, err)
	}
}

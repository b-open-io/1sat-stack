package ordlock

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/b-open-io/1sat-stack/pkg/overlay"
	overlaystorage "github.com/b-open-io/1sat-stack/pkg/overlay/storage"
	template "github.com/b-open-io/1sat-stack/pkg/template/ordlock"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/gofiber/fiber/v2"
	"github.com/spf13/viper"
)

func v2TestScript(t *testing.T) *script.Script {
	t.Helper()
	p2pkh, err := script.NewFromHex("76a914" + "2222222222222222222222222222222222222222" + "88ac")
	if err != nil {
		t.Fatal(err)
	}
	payout := &transaction.TransactionOutput{Satoshis: 1000, LockingScript: p2pkh}
	args := [][]byte{bytes.Repeat([]byte{0x11}, 20), payout.Bytes()}
	scr := script.NewFromBytes(nil)
	from := 0
	for _, slot := range template.OrdLockV2Slots {
		*scr = append(*scr, template.OrdLockV2Template[from:slot.ByteOffset]...)
		if err := scr.AppendPushData(args[slot.ParamIndex]); err != nil {
			t.Fatal(err)
		}
		from = slot.ByteOffset + 1
	}
	*scr = append(*scr, template.OrdLockV2Template[from:]...)
	return scr
}

func TestV2AdmissionAndRetentionRequireCompleteListings(t *testing.T) {
	valid := v2TestScript(t)
	prior := transaction.NewTransaction()
	prior.Outputs = []*transaction.TransactionOutput{
		{Satoshis: 1, LockingScript: valid},
		{Satoshis: 1, LockingScript: script.NewFromBytes(template.OrdLockV2Prefix)},
		{Satoshis: 1, LockingScript: script.NewFromBytes(append(bytes.Clone(*valid), 0x00))},
		{Satoshis: 2, LockingScript: valid},
		{Satoshis: 1, LockingScript: script.NewFromBytes(template.OrdLockPrefix)},
	}
	spend := transaction.NewTransaction()
	spend.Outputs = prior.Outputs
	for i := range prior.Outputs {
		spend.AddInputFromTx(prior, uint32(i), nil)
	}
	beef := transaction.NewBeef()
	if _, err := beef.MergeTransaction(spend); err != nil {
		t.Fatal(err)
	}
	tm := &TopicManagerV2{}
	admit, err := tm.IdentifyAdmissibleOutputs(t.Context(), beef, spend.TxID(), []uint32{0, 1, 2, 3, 4})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(admit.OutputsToAdmit, []uint32{0}) || !slices.Equal(admit.CoinsToRetain, []uint32{0}) {
		t.Fatalf("admittance = %+v, want only output/input 0", admit)
	}
	needed, err := tm.IdentifyNeededInputs(t.Context(), beef, spend.TxID())
	if err != nil {
		t.Fatal(err)
	}
	want := transaction.Outpoint{Txid: *prior.TxID(), Index: 0}
	if len(needed) != 1 || *needed[0] != want {
		t.Fatalf("needed inputs = %v, want %s", needed, want.String())
	}
}

func TestV2MarketPreservesDeprecatedStorage(t *testing.T) {
	factory, err := overlaystorage.NewSQLiteFactory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = factory.Close() })
	legacyStorage, err := factory.Topic("tm_ordlock")
	if err != nil {
		t.Fatal(err)
	}
	legacy := New(legacyStorage.DB(), legacyStorage.TopicID(), nil, nil)
	oldOutpoint := transaction.Outpoint{}
	if err := legacy.UpsertListing(t.Context(), &oldOutpoint, &listingData{origin: &oldOutpoint, price: 1000, seller: "1LegacyOwner"}, 1); err != nil {
		t.Fatal(err)
	}

	// Persisted v1 sync settings must not register a retired topic.
	v := viper.New()
	v.Set("mode", ModeEmbedded)
	v.Set("routes.enabled", true)
	v.Set("sync.enabled", true)
	v.Set("sync.subscription_id", "retired-subscription")
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		t.Fatal(err)
	}
	svc, err := cfg.Initialize(t.Context(), nil, &overlay.ModuleDeps{Factory: factory.Factory()})
	if err != nil {
		t.Fatal(err)
	}
	managers := svc.Engine.ListTopicManagers()
	if len(managers) != 1 || managers[TopicNameV2] == nil {
		t.Fatalf("topic managers = %v, want only %s", managers, TopicNameV2)
	}

	listingTx := transaction.NewTransaction()
	listingTx.Outputs = []*transaction.TransactionOutput{{Satoshis: 1, LockingScript: v2TestScript(t)}}
	atomic, err := listingTx.AtomicBEEF(true)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.LookupV2.OutputAdmittedByTopic(t.Context(), &engine.OutputAdmittedByTopic{AtomicBEEF: atomic, OutputIndex: 0}); err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	svc.Routes.Register(app.Group("/market"))
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/market/listings", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got []txo.IndexedOutputResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	want := transaction.Outpoint{Txid: *listingTx.TxID(), Index: 0}
	if len(got) != 1 || got[0].Outpoint != want.String() {
		t.Fatalf("market listings = %+v, want only %s", got, want.String())
	}
	if _, err := legacy.GetListing(t.Context(), oldOutpoint.Bytes()); err != nil {
		t.Fatalf("retained deprecated listing: %v", err)
	}
}

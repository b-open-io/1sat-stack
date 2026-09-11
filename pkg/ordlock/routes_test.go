package ordlock

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/gofiber/fiber/v2"
	_ "github.com/mattn/go-sqlite3"
)

func testOutpoint(seed byte) *transaction.Outpoint {
	var txid chainhash.Hash
	txid[0] = seed
	return &transaction.Outpoint{Txid: txid, Index: 0}
}

func testServices(t *testing.T) (*OrdLock, *fiber.App) {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ol := New(db, 0, nil, slog.Default())
	app := fiber.New()
	NewRoutes(ol, slog.Default()).Register(app.Group("/market"))
	return ol, app
}

func insertListing(t *testing.T, ol *OrdLock, op, origin *transaction.Outpoint, seller, name string, score float64) {
	t.Helper()
	err := ol.UpsertListing(context.Background(), op, &listingData{
		origin:      origin,
		name:        name,
		contentType: "application/op-ns",
		price:       1000,
		seller:      seller,
	}, score)
	if err != nil {
		t.Fatal(err)
	}
}

func TestPublicListingsOmitDeprecated(t *testing.T) {
	ol, app := testServices(t)
	op := testOutpoint(1)
	origin := testOutpoint(2)
	insertListing(t, ol, op, origin, "1SellerAddressxxxxxxxxxxxxxxxxx", "alice.1sat", 10)

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/market/listings?type=application/op-ns", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var results []any
	if err := json.Unmarshal(body, &results); err != nil {
		t.Fatalf("body %s: %v", body, err)
	}
	if len(results) != 0 {
		t.Fatalf("public listings = %#v, want empty", results)
	}
}

func TestPublicOriginLookupOmitsDeprecated(t *testing.T) {
	ol, app := testServices(t)
	op := testOutpoint(3)
	origin := testOutpoint(4)
	insertListing(t, ol, op, origin, "1SellerAddressxxxxxxxxxxxxxxxxx", "bob.1sat", 11)

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/market/origin/"+origin.String(), nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, want 404", resp.StatusCode)
	}

	req := httptest.NewRequest(http.MethodPost, "/market/origins", bytes.NewBufferString(`["`+origin.String()+`"]`))
	req.Header.Set("Content-Type", "application/json")
	bulk, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer bulk.Body.Close()
	if bulk.StatusCode != http.StatusOK {
		t.Fatalf("bulk status %d", bulk.StatusCode)
	}
	var got map[string]any
	if err := json.NewDecoder(bulk.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("bulk origins = %#v, want empty", got)
	}
}

func TestOutpointLookupStillReturnsListing(t *testing.T) {
	ol, app := testServices(t)
	op := testOutpoint(5)
	origin := testOutpoint(6)
	seller := "1SellerForCancelxxxxxxxxxxxxxxxx"
	insertListing(t, ol, op, origin, seller, "carol.1sat", 12)

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/market/listing/"+op.String(), nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["outpoint"] != op.String() {
		t.Fatalf("outpoint = %v, want %s", got["outpoint"], op.String())
	}
	data, _ := got["data"].(map[string]any)
	listing, _ := data["ordlock"].(map[string]any)
	if listing["seller"] != seller {
		t.Fatalf("seller = %v, want %s", listing["seller"], seller)
	}
}

func TestOwnerLookupStillReturnsListing(t *testing.T) {
	ol, app := testServices(t)
	op := testOutpoint(7)
	origin := testOutpoint(8)
	seller := "1OwnerLookupAddressxxxxxxxxxxxxx"
	other := testOutpoint(9)
	insertListing(t, ol, op, origin, seller, "dave.1sat", 13)
	insertListing(t, ol, other, testOutpoint(10), "1SomeoneElsexxxxxxxxxxxxxxxxxxxx", "eve.1sat", 14)

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/market/listings/owner/"+seller, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var results []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("owner listings = %#v, want 1", results)
	}
	if results[0]["outpoint"] != op.String() {
		t.Fatalf("outpoint = %v, want %s", results[0]["outpoint"], op.String())
	}
}

func TestGetListingByOriginStillResolvesInternally(t *testing.T) {
	ol, _ := testServices(t)
	op := testOutpoint(11)
	origin := testOutpoint(12)
	insertListing(t, ol, op, origin, "1InternalOriginxxxxxxxxxxxxxxxxx", "frank.1sat", 15)

	got, err := ol.GetListingByOrigin(context.Background(), origin)
	if err != nil {
		t.Fatal(err)
	}
	if got.Outpoint.String() != op.String() {
		t.Fatalf("internal origin lookup = %s, want %s", got.Outpoint.String(), op.String())
	}
}

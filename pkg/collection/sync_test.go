package collection

import (
	"testing"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

func TestRouteCollection(t *testing.T) {
	txid, _ := chainhash.NewHashFromHex("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	item := &transaction.Outpoint{Txid: *txid, Index: 2}

	discovery, id := routeCollection(&MapFields{SubType: SubTypeCollection}, item)
	if !discovery || id != "" {
		t.Fatalf("root: discovery=%v id=%q", discovery, id)
	}

	discovery, id = routeCollection(&MapFields{
		SubType:      SubTypeCollectionItem,
		CollectionID: "_0",
	}, item)
	if discovery || id != txid.String()+"_0" {
		t.Fatalf("relative item: discovery=%v id=%q", discovery, id)
	}

	abs := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb_1"
	discovery, id = routeCollection(&MapFields{
		SubType:      SubTypeCollectionItem,
		CollectionID: abs,
	}, item)
	if discovery || id != abs {
		t.Fatalf("absolute item: discovery=%v id=%q", discovery, id)
	}

	discovery, id = routeCollection(nil, item)
	if discovery || id != "" {
		t.Fatalf("nil: discovery=%v id=%q", discovery, id)
	}
}

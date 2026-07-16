package collection

import (
	"path/filepath"
	"testing"

	overlaystorage "github.com/b-open-io/1sat-stack/pkg/overlay/storage"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

func TestLookupListCollectionsAndItems(t *testing.T) {
	dir := t.TempDir()
	factory := func(topic string) (overlaystorage.TopicStorage, error) {
		return overlaystorage.NewSQLiteStorage(filepath.Join(dir, topic+".db"))
	}

	lookup := NewLookupService(factory)
	ctx := t.Context()

	ts, err := lookup.db(DiscoveryTopic)
	if err != nil {
		t.Fatal(err)
	}
	txid, _ := chainhash.NewHashFromHex("1111111111111111111111111111111111111111111111111111111111111111")
	col := &transaction.Outpoint{Txid: *txid, Index: 0}
	_, err = ts.DB().ExecContext(ctx, `
		INSERT INTO collection_entries(outpoint, collection_id, name, signer, content_type, mint_number, rank, map_json, score)
		VALUES (?, ?, 'Demo', '1SignerAddrxxxxxxxxxxxxxxxxx', 'image/png', NULL, NULL, NULL, 1.0)
	`, col.Bytes(), col.OrdinalString())
	if err != nil {
		t.Fatal(err)
	}

	cols, err := lookup.ListCollections(ctx, 10, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) != 1 || cols[0].Name != "Demo" || cols[0].Signer == "" {
		t.Fatalf("collections: %+v", cols)
	}

	got, err := lookup.GetCollection(ctx, col.OrdinalString())
	if err != nil || got == nil || got.CollectionID != col.OrdinalString() {
		t.Fatalf("GetCollection: %+v err=%v", got, err)
	}

	itemTxid, _ := chainhash.NewHashFromHex("2222222222222222222222222222222222222222222222222222222222222222")
	item := &transaction.Outpoint{Txid: *itemTxid, Index: 0}
	mts, err := lookup.db(ItemTopic(col.OrdinalString()))
	if err != nil {
		t.Fatal(err)
	}
	mint := 1
	_, err = mts.DB().ExecContext(ctx, `
		INSERT INTO collection_entries(outpoint, collection_id, name, signer, content_type, mint_number, rank, map_json, score)
		VALUES (?, ?, 'Item', '1SignerAddrxxxxxxxxxxxxxxxxx', 'image/png', ?, NULL, NULL, 2.0)
	`, item.Bytes(), col.OrdinalString(), mint)
	if err != nil {
		t.Fatal(err)
	}

	items, err := lookup.ListItems(ctx, col.OrdinalString(), 10, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Name != "Item" {
		t.Fatalf("items: %+v", items)
	}

	one, err := lookup.GetItem(ctx, col.OrdinalString(), item.OrdinalString())
	if err != nil || one == nil {
		t.Fatalf("GetItem: %v %+v", err, one)
	}
}

package collection

import (
	"path/filepath"
	"testing"

	overlaystorage "github.com/b-open-io/1sat-stack/pkg/overlay/storage"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

func TestLookupListRootsAndMembers(t *testing.T) {
	dir := t.TempDir()
	factory := func(topic string) (overlaystorage.TopicStorage, error) {
		return overlaystorage.NewSQLiteStorage(filepath.Join(dir, topic+".db"))
	}

	lookup := NewLookupService(factory)
	ctx := t.Context()

	// Direct DB insert to avoid BEEF/SIGMA plumbing for index query tests
	ts, err := lookup.db(DiscoveryTopic)
	if err != nil {
		t.Fatal(err)
	}
	txid, _ := chainhash.NewHashFromHex("1111111111111111111111111111111111111111111111111111111111111111")
	root := &transaction.Outpoint{Txid: *txid, Index: 0}
	_, err = ts.DB().ExecContext(ctx, `
		INSERT INTO collection_entries(outpoint, kind, collection_id, name, signer, content_type, mint_number, rank, map_json, score)
		VALUES (?, 'root', ?, 'Demo', '1SignerAddrxxxxxxxxxxxxxxxxx', 'image/png', NULL, NULL, NULL, 1.0)
	`, root.Bytes(), root.OrdinalString())
	if err != nil {
		t.Fatal(err)
	}

	roots, err := lookup.ListRoots(ctx, 10, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 1 || roots[0].Name != "Demo" || roots[0].Signer == "" {
		t.Fatalf("roots: %+v", roots)
	}

	got, err := lookup.GetRoot(ctx, root.OrdinalString())
	if err != nil || got == nil || got.CollectionID != root.OrdinalString() {
		t.Fatalf("GetRoot: %+v err=%v", got, err)
	}

	// members topic
	memberTxid, _ := chainhash.NewHashFromHex("2222222222222222222222222222222222222222222222222222222222222222")
	member := &transaction.Outpoint{Txid: *memberTxid, Index: 0}
	mts, err := lookup.db(MemberTopic(root.OrdinalString()))
	if err != nil {
		t.Fatal(err)
	}
	mint := 1
	_, err = mts.DB().ExecContext(ctx, `
		INSERT INTO collection_entries(outpoint, kind, collection_id, name, signer, content_type, mint_number, rank, map_json, score)
		VALUES (?, 'member', ?, 'Item', '1SignerAddrxxxxxxxxxxxxxxxxx', 'image/png', ?, NULL, NULL, 2.0)
	`, member.Bytes(), root.OrdinalString(), mint)
	if err != nil {
		t.Fatal(err)
	}

	members, err := lookup.ListMembers(ctx, root.OrdinalString(), 10, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0].Name != "Item" {
		t.Fatalf("members: %+v", members)
	}

	one, err := lookup.GetMember(ctx, root.OrdinalString(), member.OrdinalString())
	if err != nil || one == nil {
		t.Fatalf("GetMember: %v %+v", err, one)
	}
}

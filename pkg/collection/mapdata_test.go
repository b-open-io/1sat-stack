package collection

import (
	"testing"

	"github.com/b-open-io/1sat-stack/pkg/template/bitcom"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

func mapLockingScript(fields ...string) *script.Script {
	// Bitcom MAP lives after OP_RETURN: prefix + SET + k/v pairs
	s := &script.Script{}
	_ = s.AppendOpcodes(script.OpRETURN)
	_ = s.AppendPushData([]byte(bitcom.MapPrefix))
	for _, v := range fields {
		_ = s.AppendPushData([]byte(v))
	}
	return s
}

func TestDecodeMapFieldsCollectionItem(t *testing.T) {
	scr := mapLockingScript(
		string(bitcom.MapCmdSet),
		"type", "ord",
		"subType", "collectionItem",
		"name", "Item 1",
		"subTypeData", `{"collectionId":"_0","mintNumber":3}`,
	)
	fields := DecodeMapFields(scr)
	if fields == nil {
		t.Fatal("expected fields")
	}
	if fields.SubType != SubTypeCollectionItem {
		t.Fatalf("subType: got %q", fields.SubType)
	}
	if fields.CollectionID != "_0" {
		t.Fatalf("collectionId: got %q", fields.CollectionID)
	}
	if fields.MintNumber == nil || *fields.MintNumber != 3 {
		t.Fatalf("mintNumber: got %v", fields.MintNumber)
	}
	if fields.Name != "Item 1" {
		t.Fatalf("name: got %q", fields.Name)
	}
}

func TestDecodeMapFieldsCollectionRoot(t *testing.T) {
	scr := mapLockingScript(
		string(bitcom.MapCmdSet),
		"type", "ord",
		"subType", "collection",
		"name", "Roots",
	)
	fields := DecodeMapFields(scr)
	if fields == nil || fields.SubType != SubTypeCollection {
		t.Fatalf("got %+v", fields)
	}
	if fields.CollectionID != "" {
		t.Fatalf("root should not have collectionId, got %q", fields.CollectionID)
	}
}

func TestItemTopicHelpers(t *testing.T) {
	id := "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899_0"
	topic := ItemTopic(id)
	if topic != "tm_col_"+id {
		t.Fatalf("topic: %q", topic)
	}
	if CollectionIDFromTopic(topic) != id {
		t.Fatalf("roundtrip id: %q", CollectionIDFromTopic(topic))
	}
	if CollectionIDFromTopic(DiscoveryTopic) != "" {
		t.Fatal("discovery is not an item topic")
	}
	if !IsDiscoveryTopic(DiscoveryTopic) || !IsItemTopic(topic) {
		t.Fatal("expected discovery/item topic helpers")
	}
}

func TestNormalizeCollectionIDViaPackage(t *testing.T) {
	txid, _ := chainhash.NewHashFromHex("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	op := &transaction.Outpoint{Txid: *txid, Index: 2}
	got := NormalizeCollectionID("_1", op)
	want := txid.String() + "_1"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDiscoveryRejectsWithoutSigma(t *testing.T) {
	txid, _ := chainhash.NewHashFromHex("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	tx := transaction.NewTransaction()
	tx.Outputs = []*transaction.TransactionOutput{{
		Satoshis:      1,
		LockingScript: mapLockingScript(string(bitcom.MapCmdSet), "type", "ord", "subType", "collection", "name", "X"),
	}}
	beef := transaction.NewBeef()
	if _, err := beef.MergeTransaction(tx); err != nil {
		t.Fatalf("MergeTransaction: %v", err)
	}
	hash := tx.TxID()
	if hash == nil {
		t.Fatal("no txid")
	}
	_ = txid

	tm := NewDiscoveryTopicManager(nil)
	admit, err := tm.IdentifyAdmissibleOutputs(t.Context(), beef, hash, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(admit.OutputsToAdmit) != 0 {
		t.Fatalf("expected no admit without SIGMA, got %v", admit.OutputsToAdmit)
	}
}

func TestItemRejectsWrongCollection(t *testing.T) {
	wantID := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb_0"
	tx := transaction.NewTransaction()
	tx.Outputs = []*transaction.TransactionOutput{{
		Satoshis: 1,
		LockingScript: mapLockingScript(
			string(bitcom.MapCmdSet),
			"type", "ord",
			"subType", "collectionItem",
			"subTypeData", `{"collectionId":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc_0"}`,
		),
	}}
	beef := transaction.NewBeef()
	if _, err := beef.MergeTransaction(tx); err != nil {
		t.Fatalf("MergeTransaction: %v", err)
	}
	hash := tx.TxID()
	tm := NewItemTopicManager(wantID, nil)
	admit, err := tm.IdentifyAdmissibleOutputs(t.Context(), beef, hash, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(admit.OutputsToAdmit) != 0 {
		t.Fatalf("expected no admit for wrong collectionId")
	}
}

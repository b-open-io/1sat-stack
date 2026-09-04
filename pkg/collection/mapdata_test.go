package collection

import (
	"os"
	"strings"
	"testing"

	"github.com/b-open-io/1sat-stack/pkg/template/bitcom"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

func TestDecodeMapFieldsIndependentDisplayMetadata(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  *int
	}{
		{"integer", `42`, intPointer(42)},
		{"legacy decimal string", `"42"`, intPointer(42)},
		{"zero", `0`, intPointer(0)},
		{"string zero", `"0"`, intPointer(0)},
		{"unsafe integer", `9007199254740992`, nil},
		{"unsafe integer string", `"9007199254740992"`, nil},
		{"fraction", `1.5`, nil},
		{"negative", `-1`, nil},
		{"negative string", `"-1"`, nil},
		{"overflow", `999999999999999999999999999999`, nil},
		{"overflow string", `"999999999999999999999999999999"`, nil},
		{"padded string", `"042"`, nil},
		{"junk string", `"42x"`, nil},
		{"null", `null`, nil},
		{"object", `{}`, nil},
		{"array", `[]`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := `{"collectionId":"_0","mintNumber":` + tc.value + `,"rank":` + tc.value + `}`
			fields := DecodeMapFields(mapLockingScript(string(bitcom.MapCmdSet),
				"subType", "collectionItem", "subTypeData", raw))
			if fields == nil || fields.CollectionID != "_0" || fields.SubTypeData != raw {
				t.Fatalf("claim or raw metadata lost: %+v", fields)
			}
			for _, got := range []*int{fields.MintNumber, fields.Rank} {
				if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
					t.Fatalf("display value: got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func intPointer(value int) *int { return &value }

func TestDecodeMapFieldsInvalidCollectionClaims(t *testing.T) {
	for _, raw := range []string{
		`{`, `null`, `[]`, `"_0"`, `42`,
		`{"collectionId":42}`, `{"collectionId":null}`, `{"collectionId":{}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			fields := DecodeMapFields(mapLockingScript(string(bitcom.MapCmdSet),
				"subType", "collectionItem", "subTypeData", raw))
			if fields == nil || fields.CollectionID != "" {
				t.Fatalf("invalid claim: %+v", fields)
			}
		})
	}
}

func TestDecodeMapFieldsHistoricalCollectionItem(t *testing.T) {
	const txid = "34adf92c766e11a656d3ff3508df7b1a31405821bf734bc9bef9fb43fcf701f9"
	raw, err := os.ReadFile("../template/bitcom/testdata/" + txid + ".hex")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := transaction.NewTransactionFromHex(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if tx.TxID().String() != txid {
		t.Fatal("fixture txid mismatch")
	}
	fields := DecodeMapFields(tx.Outputs[0].LockingScript)
	if fields == nil || fields.CollectionID != "ee8c1a403ad4d9396df261b96f53a30209c2ba419d0e9b41f4930a4602e72cde_0" {
		t.Fatalf("historical claim lost: %+v", fields)
	}
	if fields.MintNumber == nil || *fields.MintNumber != 42 {
		t.Fatalf("historical mintNumber: %v", fields.MintNumber)
	}
	// This is claim compatibility, not membership conformance: the fixture has
	// no top-level name, and this test does not resolve or verify root authority.
	if fields.Name != "" {
		t.Fatalf("unexpected top-level name: %q", fields.Name)
	}
}

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

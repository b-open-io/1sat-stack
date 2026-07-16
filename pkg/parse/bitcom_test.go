package parse

import (
	"testing"

	"github.com/b-open-io/1sat-stack/pkg/template/bitcom"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

func TestParseMAPEmitsCollectionEvents(t *testing.T) {
	txid, err := chainhash.NewHashFromHex("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	outpoint := &transaction.Outpoint{Txid: *txid, Index: 3}

	mapScript := &script.Script{}
	for _, value := range []string{
		string(bitcom.MapCmdSet),
		"type", "ord",
		"subType", "collectionItem",
		"subTypeData", `{"collectionId":"_1","mintNumber":7}`,
	} {
		if err := mapScript.AppendPushData([]byte(value)); err != nil {
			t.Fatal(err)
		}
	}

	ctx := &ParseContext{
		Outpoint: outpoint,
		Results: map[string]*ParseResult{
			TagBitcom: {
				Tag: TagBitcom,
				Data: &bitcom.Bitcom{Protocols: []*bitcom.BitcomProtocol{{
					Protocol: bitcom.MapPrefix,
					Script:   mapScript.Bytes(),
				}}},
			},
		},
	}

	result, err := ParseMAP(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected MAP parse result")
	}

	want := map[string]bool{
		"map:type:ord":                             false,
		"map:subType:collectionItem":               false,
		"map:collectionId:" + txid.String() + "_1": false,
	}
	for _, event := range result.Events {
		if _, ok := want[event]; ok {
			want[event] = true
		}
	}
	for event, found := range want {
		if !found {
			t.Errorf("missing event %q in %v", event, result.Events)
		}
	}
}

func TestParseMAPEmitsCollectionRootSubType(t *testing.T) {
	mapScript := &script.Script{}
	for _, value := range []string{
		string(bitcom.MapCmdSet),
		"type", "ord",
		"subType", "collection",
		"name", "Demo",
	} {
		if err := mapScript.AppendPushData([]byte(value)); err != nil {
			t.Fatal(err)
		}
	}

	ctx := &ParseContext{
		Results: map[string]*ParseResult{
			TagBitcom: {
				Tag: TagBitcom,
				Data: &bitcom.Bitcom{Protocols: []*bitcom.BitcomProtocol{{
					Protocol: bitcom.MapPrefix,
					Script:   mapScript.Bytes(),
				}}},
			},
		},
	}

	result, err := ParseMAP(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected MAP parse result")
	}

	want := map[string]bool{
		"map:type:ord":           false,
		"map:subType:collection": false,
	}
	for _, event := range result.Events {
		if _, ok := want[event]; ok {
			want[event] = true
		}
	}
	for event, found := range want {
		if !found {
			t.Errorf("missing event %q in %v", event, result.Events)
		}
	}
	for _, event := range result.Events {
		if len(event) >= len("map:collectionId:") && event[:len("map:collectionId:")] == "map:collectionId:" {
			t.Errorf("collection root should not emit collectionId event, got %q", event)
		}
	}
}

func TestParseMAPAbsoluteCollectionID(t *testing.T) {
	absID := "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899_0"
	mapScript := &script.Script{}
	for _, value := range []string{
		string(bitcom.MapCmdSet),
		"type", "ord",
		"subType", "collectionItem",
		"subTypeData", `{"collectionId":"` + absID + `"}`,
	} {
		if err := mapScript.AppendPushData([]byte(value)); err != nil {
			t.Fatal(err)
		}
	}

	ctx := &ParseContext{
		Results: map[string]*ParseResult{
			TagBitcom: {
				Tag: TagBitcom,
				Data: &bitcom.Bitcom{Protocols: []*bitcom.BitcomProtocol{{
					Protocol: bitcom.MapPrefix,
					Script:   mapScript.Bytes(),
				}}},
			},
		},
	}

	result, err := ParseMAP(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := "map:collectionId:" + absID
	found := false
	for _, event := range result.Events {
		if event == want {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("missing %q in %v", want, result.Events)
	}
}

func TestNormalizeCollectionID(t *testing.T) {
	txid, err := chainhash.NewHashFromHex("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
	if err != nil {
		t.Fatal(err)
	}
	op := &transaction.Outpoint{Txid: *txid, Index: 5}

	if got := NormalizeCollectionID("_2", op); got != txid.String()+"_2" {
		t.Errorf("relative: got %q", got)
	}
	abs := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef_0"
	if got := NormalizeCollectionID(abs, op); got != abs {
		t.Errorf("absolute: got %q", got)
	}
	if got := NormalizeCollectionID("_2", nil); got != "_2" {
		t.Errorf("nil outpoint: got %q", got)
	}
	if got := NormalizeCollectionID("_x", op); got != "_x" {
		t.Errorf("invalid relative: got %q", got)
	}
}

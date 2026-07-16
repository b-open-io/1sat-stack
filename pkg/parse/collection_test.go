package parse

import (
	"testing"

	"github.com/b-open-io/1sat-stack/pkg/template/bitcom"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

func TestParseCollectionEmitsNormalizedCollectionID(t *testing.T) {
	txid, err := chainhash.NewHashFromHex("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	outpoint := &transaction.Outpoint{Txid: *txid, Index: 3}

	m := &bitcom.Map{
		Cmd: bitcom.MapCmdSet,
		Data: map[string]string{
			"type":        "ord",
			"subType":     "collectionItem",
			"subTypeData": `{"collectionId":"_1","mintNumber":7}`,
		},
	}
	ctx := &ParseContext{
		Outpoint: outpoint,
		Results: map[string]*ParseResult{
			TagMAP: {Tag: TagMAP, Data: m},
		},
	}

	result, err := ParseCollection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected collection parse result")
	}
	want := "map:collectionId:" + txid.String() + "_1"
	found := false
	for _, event := range result.Events {
		if event == want {
			found = true
		}
	}
	if !found {
		t.Errorf("missing %q in %v", want, result.Events)
	}
	if id, ok := result.Data.(string); !ok || id != txid.String()+"_1" {
		t.Errorf("Data: got %#v", result.Data)
	}
}

func TestParseCollectionAbsoluteCollectionID(t *testing.T) {
	absID := "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899_0"
	m := &bitcom.Map{
		Cmd: bitcom.MapCmdSet,
		Data: map[string]string{
			"type":        "ord",
			"subType":     "collectionItem",
			"subTypeData": `{"collectionId":"` + absID + `"}`,
		},
	}
	ctx := &ParseContext{
		Results: map[string]*ParseResult{
			TagMAP: {Tag: TagMAP, Data: m},
		},
	}

	result, err := ParseCollection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := "map:collectionId:" + absID
	found := false
	for _, event := range result.Events {
		if event == want {
			found = true
		}
	}
	if !found {
		t.Errorf("missing %q in %v", want, result.Events)
	}
}

func TestParseCollectionSkipsNonItems(t *testing.T) {
	m := &bitcom.Map{
		Cmd: bitcom.MapCmdSet,
		Data: map[string]string{
			"type":    "ord",
			"subType": "collection",
			"name":    "Demo",
		},
	}
	ctx := &ParseContext{
		Results: map[string]*ParseResult{
			TagMAP: {Tag: TagMAP, Data: m},
		},
	}
	result, err := ParseCollection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Fatalf("expected nil for collection subtype, got %+v", result)
	}
}

func TestParseCollectionIgnoresTopLevelCollectionID(t *testing.T) {
	// Only subTypeData is canonical; a top-level MAP key must not route.
	m := &bitcom.Map{
		Cmd: bitcom.MapCmdSet,
		Data: map[string]string{
			"type":         "ord",
			"subType":      "collectionItem",
			"collectionId": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa_0",
		},
	}
	ctx := &ParseContext{
		Results: map[string]*ParseResult{
			TagMAP: {Tag: TagMAP, Data: m},
		},
	}
	result, err := ParseCollection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Fatalf("expected nil without subTypeData.collectionId, got %+v", result)
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

// Ensure MAP + collection pipeline order works end-to-end on a bitcom body.
func TestParseMAPThenCollection(t *testing.T) {
	txid, err := chainhash.NewHashFromHex("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	mapScript := &script.Script{}
	for _, value := range []string{
		string(bitcom.MapCmdSet),
		"type", "ord",
		"subType", "collectionItem",
		"subTypeData", `{"collectionId":"_0"}`,
	} {
		if err := mapScript.AppendPushData([]byte(value)); err != nil {
			t.Fatal(err)
		}
	}

	ctx := &ParseContext{
		Outpoint: &transaction.Outpoint{Txid: *txid, Index: 1},
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

	mapResult, err := ParseMAP(ctx)
	if err != nil || mapResult == nil {
		t.Fatalf("ParseMAP: %v %+v", err, mapResult)
	}
	ctx.Results[TagMAP] = mapResult

	colResult, err := ParseCollection(ctx)
	if err != nil || colResult == nil {
		t.Fatalf("ParseCollection: %v %+v", err, colResult)
	}
	want := "map:collectionId:" + txid.String() + "_0"
	found := false
	for _, e := range colResult.Events {
		if e == want {
			found = true
		}
	}
	if !found {
		t.Errorf("missing %q in %v", want, colResult.Events)
	}
}

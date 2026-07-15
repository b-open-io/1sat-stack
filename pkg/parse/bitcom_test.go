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

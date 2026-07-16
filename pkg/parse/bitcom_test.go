package parse

import (
	"testing"

	"github.com/b-open-io/1sat-stack/pkg/template/bitcom"
	"github.com/bsv-blockchain/go-sdk/script"
)

func TestParseMAPEmitsGenericFieldEvents(t *testing.T) {
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
		"map:type:ord":               false,
		"map:subType:collectionItem": false,
	}
	for _, event := range result.Events {
		if _, ok := want[event]; ok {
			want[event] = true
		}
		if len(event) >= len("map:collectionId:") && event[:len("map:collectionId:")] == "map:collectionId:" {
			t.Errorf("MAP parser must not emit collectionId (collection parser owns that): %q", event)
		}
	}
	for event, found := range want {
		if !found {
			t.Errorf("missing event %q in %v", event, result.Events)
		}
	}
}

func TestParseMAPEmitsCollectionSubType(t *testing.T) {
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
}

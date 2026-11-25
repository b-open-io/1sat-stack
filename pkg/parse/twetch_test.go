package parse

import (
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// Muties #15, output 14 of a7595e9f7400390d34f193a4011212ddebc7c2065716db525c399bb91856665b.
func mutiesScript(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/twetch_muties_15.hex")
	if err != nil {
		t.Fatal(err)
	}
	b, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func parseTwetch(t *testing.T, lockingScript []byte) *ParseResult {
	t.Helper()
	res, err := ParseTwetch(&ParseContext{
		Outpoint:      &transaction.Outpoint{},
		LockingScript: lockingScript,
		Satoshis:      2180,
		Results:       map[string]*ParseResult{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestParseTwetchMuties(t *testing.T) {
	res := parseTwetch(t, mutiesScript(t))
	if res == nil || res.Tag != TagTwetch {
		t.Fatalf("Muties output must parse as %s, got %+v", TagTwetch, res)
	}
	if len(res.Events) != 1 || res.Events[0] != TagTwetch {
		t.Fatalf("events = %v", res.Events)
	}
	if len(res.Owners) != 1 || res.Owners[0].Address(types.Mainnet) != "1J82Vbt4Uz8kKr2Xb3Kro9WtCh6udoctCJ" {
		t.Fatalf("owner = %v", res.Owners)
	}
	meta, ok := res.Data.(*Twetch)
	if !ok {
		t.Fatalf("data = %T", res.Data)
	}
	want := Twetch{
		HashLock: "414cc1fb9b5114dec30ced7446d88574d959f131",
		Title:    "Muties #15",
		Image:    "b://55068f35629f5fa80866882efba1cf86782e86130e2a35091965e4e577f7abcf",
		Number:   15,
		Series:   456,
		Rarity:   "Rare",
		Rank:     78,
	}
	if meta.HashLock != want.HashLock || meta.Title != want.Title || meta.Image != want.Image ||
		meta.Number != want.Number || meta.Series != want.Series || meta.Rarity != want.Rarity || meta.Rank != want.Rank {
		t.Fatalf("meta = %+v", meta)
	}
	if !strings.HasPrefix(meta.GltfModel, "b://") || !strings.HasPrefix(meta.Background, "b://") {
		t.Fatalf("model/background = %q %q", meta.GltfModel, meta.Background)
	}
	if len(meta.Attributes) != 4 || meta.Attributes[0].TraitType != "Background" || meta.Attributes[0].Value != "Love Roses" {
		t.Fatalf("attributes = %+v", meta.Attributes)
	}
}

func TestParseTwetchRejects(t *testing.T) {
	good := mutiesScript(t)
	mutate := func(i int, b byte) []byte {
		s := append([]byte{}, good...)
		s[i] = b
		return s
	}
	jsonScript := func(j string) []byte {
		s := append([]byte{}, good[:twetchPrefixLen+1]...)
		s = append(s, byte(len(j)))
		return append(s, j...)
	}
	cases := map[string][]byte{
		"plain p2pkh":            good[twetchHashLockLen : twetchHashLockLen+25],
		"no metadata":            good[:twetchPrefixLen],
		"no OP_EQUALVERIFY":      mutate(22, 0x87),
		"broken p2pkh":           mutate(twetchHashLockLen+24, 0xad),
		"no OP_RETURN":           mutate(twetchPrefixLen, 0x00),
		"not json":               jsonScript("hello"),
		"json without title/img": jsonScript(`{"foo":1}`),
	}
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			if r := parseTwetch(t, s); r != nil {
				t.Fatalf("must not parse, got %+v", r)
			}
		})
	}
}

func TestParseTwetchLenientNumbers(t *testing.T) {
	good := mutiesScript(t)
	j := `{"title":"X","number":"7","series":12.0,"score":3}`
	s := append([]byte{}, good[:twetchPrefixLen+1]...)
	s = append(append(s, byte(len(j))), j...)
	res := parseTwetch(t, s)
	if res == nil {
		t.Fatal("must parse")
	}
	meta := res.Data.(*Twetch)
	if meta.Number != 7 || meta.Series != 12 {
		t.Fatalf("meta = %+v", meta)
	}
}

func TestParseRunsTwetchByDefault(t *testing.T) {
	results, err := Parse(&ParseContext{
		Outpoint:      &transaction.Outpoint{},
		LockingScript: mutiesScript(t),
		Satoshis:      2180,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if results[TagTwetch] == nil {
		t.Fatalf("default parse must include %s, got %v", TagTwetch, results)
	}
	if results[TagP2PKH] != nil {
		t.Fatal("a Twetch output is not a P2PKH prefix")
	}
}

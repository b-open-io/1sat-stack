package ordfs

import (
	"encoding/binary"
	"testing"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

func TestProvenancePathOrder(t *testing.T) {
	// path built tip→origin: seq 2,1,0
	tip := testOutpoint(t, 3, 0)
	mid := testOutpoint(t, 2, 0)
	origin := testOutpoint(t, 1, 0)

	// Simulate GetSeqAt results for s=2,1,0
	bySeq := map[uint32]*transaction.Outpoint{0: origin, 1: mid, 2: tip}
	path := make([]*transaction.Outpoint, 0, 3)
	for s := 2; s >= 0; s-- {
		path = append(path, bySeq[uint32(s)])
	}
	if !path[0].Txid.Equal(tip.Txid) {
		t.Fatalf("path[0] should be tip")
	}
	if !path[2].Txid.Equal(origin.Txid) {
		t.Fatalf("path[last] should be origin")
	}
	// unique txids for merge
	seen := map[chainhash.Hash]struct{}{}
	var n int
	for _, op := range path {
		if _, ok := seen[op.Txid]; ok {
			continue
		}
		seen[op.Txid] = struct{}{}
		n++
	}
	if n != 3 {
		t.Fatalf("unique txids=%d want 3", n)
	}
}

func TestPathParent(t *testing.T) {
	tip := testOutpoint(t, 3, 0)
	mid := testOutpoint(t, 2, 0)
	origin := testOutpoint(t, 1, 0)
	path := []*transaction.Outpoint{tip, mid, origin}

	if p := pathParent(path, 0); p == nil || !p.Txid.Equal(mid.Txid) {
		t.Fatalf("tip parent want mid")
	}
	if p := pathParent(path, 1); p == nil || !p.Txid.Equal(origin.Txid) {
		t.Fatalf("mid parent want origin")
	}
	if p := pathParent(path, 2); p != nil {
		t.Fatalf("origin parent want nil")
	}
}

func TestOutpointBeefBytesHeader(t *testing.T) {
	// Minimal valid BEEF body via empty-ish beef is hard; wrap a fake body path:
	// outpointBeefBytes calls b.Bytes() — use a real empty V2 beef if possible.
	b := transaction.NewBeef()
	tip := testOutpoint(t, 7, 3)
	// Empty beef may still serialize; if it errors, skip body and only document constant.
	raw, err := outpointBeefBytes(b, tip)
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint32(raw[:4]) != OUTPOINT_BEEF {
		t.Fatalf("prefix=%x want 16a7beef", raw[:4])
	}
	op := tip.Bytes()
	for i := range op {
		if raw[4+i] != op[i] {
			t.Fatalf("outpoint bytes mismatch at %d", i)
		}
	}
	if tip.Index != 3 {
		t.Fatal("fixture index")
	}
	if binary.LittleEndian.Uint32(raw[4+32:4+36]) != 3 {
		t.Fatalf("vout LE want 3 got %d", binary.LittleEndian.Uint32(raw[36:40]))
	}
}

func TestCarrierInputIndexFromParent(t *testing.T) {
	o := &Ordfs{}
	parent := testOutpoint(t, 1, 0)
	noiseA := testOutpoint(t, 8, 0)
	noiseB := testOutpoint(t, 9, 0)

	tx := &transaction.Transaction{
		Inputs: []*transaction.TransactionInput{
			{SourceTXID: &noiseA.Txid, SourceTxOutIndex: noiseA.Index},
			{SourceTXID: &parent.Txid, SourceTxOutIndex: parent.Index},
			{SourceTXID: &noiseB.Txid, SourceTxOutIndex: noiseB.Index},
		},
		Outputs: []*transaction.TransactionOutput{
			{Satoshis: 1, LockingScript: &script.Script{}},
		},
	}
	hop := &transaction.Outpoint{Txid: chainhash.Hash{}, Index: 0}
	// txid unused when parent match is used
	idx, err := o.carrierInputIndex(t.Context(), tx, hop, parent)
	if err != nil {
		t.Fatal(err)
	}
	if idx != 1 {
		t.Fatalf("carrier=%d want 1 (ignore trailing noise input)", idx)
	}
}

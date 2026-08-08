package ordfs

import (
	"testing"

	"github.com/bsv-blockchain/go-sdk/chainhash"
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

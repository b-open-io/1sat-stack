package ordfs

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

func testOutpoint(t *testing.T, n byte, vout uint32) *transaction.Outpoint {
	t.Helper()
	var h chainhash.Hash
	for i := range h {
		h[i] = n
	}
	return &transaction.Outpoint{Txid: h, Index: vout}
}

func TestSchemaVersionStamp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "origins")
	store, err := NewBadgerOriginStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ver, err := store.schemaVersion()
	if err != nil {
		t.Fatal(err)
	}
	if ver != originStoreSchemaVersion {
		t.Fatalf("schema version=%d want %d", ver, originStoreSchemaVersion)
	}
}

func TestOrgValueRoundTrip(t *testing.T) {
	origin := testOutpoint(t, 1, 0)
	val := encodeOrgValue(origin, 7)
	info := decodeOrgValue(val)
	if info == nil {
		t.Fatal("decode returned nil")
	}
	if !info.Origin.Txid.Equal(origin.Txid) || info.Origin.Index != origin.Index {
		t.Fatalf("origin mismatch: got %v", info.Origin)
	}
	if info.Seq != 7 {
		t.Fatalf("seq=%d want 7", info.Seq)
	}
	if decodeOrgValue(val[:outpointSize]) != nil {
		t.Fatal("short value should fail decode")
	}
}

func TestWriteBatchStoresOriginAndSeq(t *testing.T) {
	dir := t.TempDir()
	store, err := NewBadgerOriginStore(filepath.Join(dir, "origins"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	origin := testOutpoint(t, 1, 0)
	tip := testOutpoint(t, 2, 0)
	mid := testOutpoint(t, 3, 0)

	err = store.WriteBatch(ctx, &OriginBatch{
		Origin: origin,
		Entries: []OriginEntry{
			{Outpoint: origin, Seq: 0, HasRev: true, ContentType: "text/plain", ContentLength: 1},
			{Outpoint: mid, Seq: 1},
			{Outpoint: tip, Seq: 2},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	info, err := store.GetOrigin(ctx, tip)
	if err != nil {
		t.Fatal(err)
	}
	if info == nil || info.Seq != 2 {
		t.Fatalf("tip info=%v", info)
	}
	if !info.Origin.Txid.Equal(origin.Txid) {
		t.Fatalf("tip origin=%v", info.Origin)
	}

	at, err := store.GetSeqAt(ctx, origin, 2)
	if err != nil {
		t.Fatal(err)
	}
	if at == nil || !at.Txid.Equal(tip.Txid) {
		t.Fatalf("GetSeqAt(2)=%v", at)
	}

	latest, latestSeq, err := store.GetLatestSeq(ctx, origin)
	if err != nil {
		t.Fatal(err)
	}
	if latestSeq != 2 || latest == nil || !latest.Txid.Equal(tip.Txid) {
		t.Fatalf("latest=%v seq=%d", latest, latestSeq)
	}
}

func TestAddEntryExtendsChain(t *testing.T) {
	dir := t.TempDir()
	store, err := NewBadgerOriginStore(filepath.Join(dir, "origins"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	origin := testOutpoint(t, 1, 0)
	next := testOutpoint(t, 4, 0)

	if err := store.WriteBatch(ctx, &OriginBatch{
		Origin:  origin,
		Entries: []OriginEntry{{Outpoint: origin, Seq: 0, HasRev: true, ContentType: "a", ContentLength: 1}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AddEntry(ctx, origin, &OriginEntry{Outpoint: next, Seq: 1}); err != nil {
		t.Fatal(err)
	}

	info, err := store.GetOrigin(ctx, next)
	if err != nil {
		t.Fatal(err)
	}
	if info == nil || info.Seq != 1 {
		t.Fatalf("next info=%v", info)
	}
}

func TestMigrateToOriginJoinNumbering(t *testing.T) {
	// priorSeq=5, chain tip..stop+1 with relative 0,-1 → abs 7,6
	chain := []ChainEntry{
		{Outpoint: testOutpoint(t, 9, 0), RelativeSeq: 0},
		{Outpoint: testOutpoint(t, 8, 0), RelativeSeq: -1},
	}
	lastRel := chain[len(chain)-1].RelativeSeq
	priorSeq := 5
	base := priorSeq + 1
	got := make([]uint32, len(chain))
	for i, entry := range chain {
		got[i] = uint32(entry.RelativeSeq - lastRel + base)
	}
	if got[0] != 7 || got[1] != 6 {
		t.Fatalf("join numbering got %v want [7 6]", got)
	}

	// full crawl priorSeq=-1, origin last at rel -2 → 2,1,0
	full := []ChainEntry{
		{RelativeSeq: 0},
		{RelativeSeq: -1},
		{RelativeSeq: -2},
	}
	lastRel = full[len(full)-1].RelativeSeq
	base = -1 + 1
	fullGot := make([]uint32, len(full))
	for i, entry := range full {
		fullGot[i] = uint32(entry.RelativeSeq - lastRel + base)
	}
	if fullGot[0] != 2 || fullGot[1] != 1 || fullGot[2] != 0 {
		t.Fatalf("full numbering got %v want [2 1 0]", fullGot)
	}
}

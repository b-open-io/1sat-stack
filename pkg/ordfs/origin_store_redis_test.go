package ordfs

import (
	"context"
	"encoding/binary"
	"math"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// testOutpoint builds a deterministic outpoint whose txid is 32 copies of n.
// redisTestOutpoint builds a deterministic outpoint for fixtures.
func redisTestOutpoint(n byte, index uint32) *transaction.Outpoint {
	b := make([]byte, outpointSize)
	for i := range b[:32] {
		b[i] = n
	}
	binary.LittleEndian.PutUint32(b[32:], index)
	return transaction.NewOutpointFromBytes(b)
}

var (
	testOrigin = redisTestOutpoint(1, 0)
	testSeq0   = testOrigin
	testSeq1   = redisTestOutpoint(2, 0)
	testSeq2   = redisTestOutpoint(3, 0)
	testSeq3   = redisTestOutpoint(4, 1)
	testSeq4   = redisTestOutpoint(5, 2)
	testAbsent = redisTestOutpoint(9, 9)
)

func newTestRedisOriginStore(t *testing.T) *RedisOriginStore {
	t.Helper()
	mr := miniredis.RunT(t)
	store, err := NewRedisOriginStore(t.Context(), "redis://"+mr.Addr())
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("failed to close store: %v", err)
		}
	})
	return store
}

// testChainBatch is the fixture chain written by most tests:
//
//	seq 0: rev(text/plain, 11)
//	seq 1: map
//	seq 2: par
//	seq 3: rev(image/png, 2048) + map
//	seq 4: plain sequence entry
func testChainBatch() *OriginBatch {
	return &OriginBatch{
		Origin: testOrigin,
		Entries: []OriginEntry{
			{Outpoint: testSeq0, Seq: 0, HasRev: true, ContentType: "text/plain", ContentLength: 11},
			{Outpoint: testSeq1, Seq: 1, HasMap: true},
			{Outpoint: testSeq2, Seq: 2, HasPar: true},
			{Outpoint: testSeq3, Seq: 3, HasRev: true, ContentType: "image/png", ContentLength: 2048, HasMap: true},
			{Outpoint: testSeq4, Seq: 4},
		},
	}
}

func newTestStoreWithChain(t *testing.T) *RedisOriginStore {
	t.Helper()
	store := newTestRedisOriginStore(t)
	if err := store.WriteBatch(context.Background(), testChainBatch()); err != nil {
		t.Fatalf("failed to write chain: %v", err)
	}
	return store
}

func assertOutpoint(t *testing.T, got, want *transaction.Outpoint) {
	t.Helper()
	if want == nil {
		if got != nil {
			t.Fatalf("expected nil outpoint, got %s", got.OrdinalString())
		}
		return
	}
	if got == nil {
		t.Fatalf("expected outpoint %s, got nil", want.OrdinalString())
	}
	if !got.Equal(want) {
		t.Fatalf("expected outpoint %s, got %s", want.OrdinalString(), got.OrdinalString())
	}
}

func TestRedisOriginStoreGetOrigin(t *testing.T) {
	store := newTestStoreWithChain(t)
	ctx := context.Background()

	tests := []struct {
		name     string
		outpoint *transaction.Outpoint
		want     *transaction.Outpoint
		wantSeq  uint32
	}{
		{name: "origin maps to itself at seq 0", outpoint: testSeq0, want: testOrigin, wantSeq: 0},
		{name: "mid chain carries its seq", outpoint: testSeq2, want: testOrigin, wantSeq: 2},
		{name: "tip carries its seq", outpoint: testSeq4, want: testOrigin, wantSeq: 4},
		{name: "unknown outpoint returns nil", outpoint: testAbsent, want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := store.GetOrigin(ctx, tt.outpoint)
			if err != nil {
				t.Fatalf("GetOrigin returned error: %v", err)
			}
			if tt.want == nil {
				if got != nil {
					t.Fatalf("expected nil, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected origin info, got nil")
			}
			assertOutpoint(t, got.Origin, tt.want)
			if got.Seq != tt.wantSeq {
				t.Errorf("expected seq %d, got %d", tt.wantSeq, got.Seq)
			}
		})
	}
}

func TestRedisOriginStoreGetSeqAt(t *testing.T) {
	store := newTestStoreWithChain(t)
	ctx := context.Background()

	tests := []struct {
		name string
		seq  uint32
		want *transaction.Outpoint
	}{
		{name: "first", seq: 0, want: testSeq0},
		{name: "middle", seq: 2, want: testSeq2},
		{name: "last", seq: 4, want: testSeq4},
		{name: "beyond tip returns nil", seq: 5, want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := store.GetSeqAt(ctx, testOrigin, tt.seq)
			if err != nil {
				t.Fatalf("GetSeqAt returned error: %v", err)
			}
			assertOutpoint(t, got, tt.want)
		})
	}

	t.Run("unknown origin returns nil", func(t *testing.T) {
		got, err := store.GetSeqAt(ctx, testAbsent, 0)
		if err != nil {
			t.Fatalf("GetSeqAt returned error: %v", err)
		}
		assertOutpoint(t, got, nil)
	})
}

func TestRedisOriginStoreGetLatestSeq(t *testing.T) {
	ctx := context.Background()

	t.Run("returns tip of chain", func(t *testing.T) {
		store := newTestStoreWithChain(t)
		got, seq, err := store.GetLatestSeq(ctx, testOrigin)
		if err != nil {
			t.Fatalf("GetLatestSeq returned error: %v", err)
		}
		assertOutpoint(t, got, testSeq4)
		if seq != 4 {
			t.Fatalf("expected seq 4, got %d", seq)
		}
	})

	t.Run("unknown origin returns nil with zero seq", func(t *testing.T) {
		store := newTestRedisOriginStore(t)
		got, seq, err := store.GetLatestSeq(ctx, testOrigin)
		if err != nil {
			t.Fatalf("GetLatestSeq returned error: %v", err)
		}
		assertOutpoint(t, got, nil)
		if seq != 0 {
			t.Fatalf("expected seq 0, got %d", seq)
		}
	})
}

func TestRedisOriginStoreGetLatestRevBefore(t *testing.T) {
	store := newTestStoreWithChain(t)
	ctx := context.Background()

	tests := []struct {
		name              string
		seq               uint32
		wantOutpoint      *transaction.Outpoint
		wantContentType   string
		wantContentLength uint32
	}{
		{name: "exact match at first rev", seq: 0, wantOutpoint: testSeq0, wantContentType: "text/plain", wantContentLength: 11},
		{name: "between revs holds first", seq: 1, wantOutpoint: testSeq0, wantContentType: "text/plain", wantContentLength: 11},
		{name: "just before second rev", seq: 2, wantOutpoint: testSeq0, wantContentType: "text/plain", wantContentLength: 11},
		{name: "exact match at second rev", seq: 3, wantOutpoint: testSeq3, wantContentType: "image/png", wantContentLength: 2048},
		{name: "after last rev", seq: 4, wantOutpoint: testSeq3, wantContentType: "image/png", wantContentLength: 2048},
		{name: "beyond tip", seq: 99, wantOutpoint: testSeq3, wantContentType: "image/png", wantContentLength: 2048},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := store.GetLatestRevBefore(ctx, testOrigin, tt.seq)
			if err != nil {
				t.Fatalf("GetLatestRevBefore returned error: %v", err)
			}
			if got == nil {
				t.Fatalf("expected rev entry, got nil")
			}
			assertOutpoint(t, got.Outpoint, tt.wantOutpoint)
			if got.ContentType != tt.wantContentType {
				t.Fatalf("expected content type %q, got %q", tt.wantContentType, got.ContentType)
			}
			if got.ContentLength != tt.wantContentLength {
				t.Fatalf("expected content length %d, got %d", tt.wantContentLength, got.ContentLength)
			}
		})
	}

	t.Run("no rev at or before seq returns nil", func(t *testing.T) {
		store := newTestRedisOriginStore(t)
		if err := store.WriteBatch(ctx, &OriginBatch{
			Origin: testOrigin,
			Entries: []OriginEntry{
				{Outpoint: testSeq0, Seq: 0},
				{Outpoint: testSeq3, Seq: 3, HasRev: true, ContentType: "image/png", ContentLength: 2048},
			},
		}); err != nil {
			t.Fatalf("failed to write batch: %v", err)
		}

		got, err := store.GetLatestRevBefore(ctx, testOrigin, 2)
		if err != nil {
			t.Fatalf("GetLatestRevBefore returned error: %v", err)
		}
		if got != nil {
			t.Fatalf("expected nil rev entry, got %s", got.Outpoint.OrdinalString())
		}
	})

	t.Run("unknown origin returns nil", func(t *testing.T) {
		got, err := store.GetLatestRevBefore(ctx, testAbsent, 4)
		if err != nil {
			t.Fatalf("GetLatestRevBefore returned error: %v", err)
		}
		if got != nil {
			t.Fatalf("expected nil rev entry, got %s", got.Outpoint.OrdinalString())
		}
	})
}

func TestRedisOriginStoreGetLatestMapBefore(t *testing.T) {
	store := newTestStoreWithChain(t)
	ctx := context.Background()

	tests := []struct {
		name string
		seq  uint32
		want *transaction.Outpoint
	}{
		{name: "before first map", seq: 0, want: nil},
		{name: "exact match at first map", seq: 1, want: testSeq1},
		{name: "between maps holds first", seq: 2, want: testSeq1},
		{name: "exact match at second map", seq: 3, want: testSeq3},
		{name: "after last map", seq: 4, want: testSeq3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := store.GetLatestMapBefore(ctx, testOrigin, tt.seq)
			if err != nil {
				t.Fatalf("GetLatestMapBefore returned error: %v", err)
			}
			assertOutpoint(t, got, tt.want)
		})
	}

	t.Run("unknown origin returns nil", func(t *testing.T) {
		got, err := store.GetLatestMapBefore(ctx, testAbsent, 4)
		if err != nil {
			t.Fatalf("GetLatestMapBefore returned error: %v", err)
		}
		assertOutpoint(t, got, nil)
	})
}

func TestRedisOriginStoreGetLatestParentBefore(t *testing.T) {
	store := newTestStoreWithChain(t)
	ctx := context.Background()

	tests := []struct {
		name string
		seq  uint32
		want *transaction.Outpoint
	}{
		{name: "before parent", seq: 1, want: nil},
		{name: "exact match", seq: 2, want: testSeq2},
		{name: "after parent", seq: 4, want: testSeq2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := store.GetLatestParentBefore(ctx, testOrigin, tt.seq)
			if err != nil {
				t.Fatalf("GetLatestParentBefore returned error: %v", err)
			}
			assertOutpoint(t, got, tt.want)
		})
	}

	t.Run("unknown origin returns nil", func(t *testing.T) {
		got, err := store.GetLatestParentBefore(ctx, testAbsent, 4)
		if err != nil {
			t.Fatalf("GetLatestParentBefore returned error: %v", err)
		}
		assertOutpoint(t, got, nil)
	})
}

func TestRedisOriginStoreGetAllMapUpTo(t *testing.T) {
	store := newTestStoreWithChain(t)
	ctx := context.Background()

	tests := []struct {
		name string
		seq  uint32
		want []*transaction.Outpoint
	}{
		{name: "before first map", seq: 0, want: nil},
		{name: "first map only", seq: 1, want: []*transaction.Outpoint{testSeq1}},
		{name: "inclusive of second map", seq: 3, want: []*transaction.Outpoint{testSeq1, testSeq3}},
		{name: "beyond tip", seq: 99, want: []*transaction.Outpoint{testSeq1, testSeq3}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := store.GetAllMapUpTo(ctx, testOrigin, tt.seq)
			if err != nil {
				t.Fatalf("GetAllMapUpTo returned error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("expected %d map entries, got %d", len(tt.want), len(got))
			}
			for i := range tt.want {
				assertOutpoint(t, got[i], tt.want[i])
			}
		})
	}

	t.Run("unknown origin returns empty", func(t *testing.T) {
		got, err := store.GetAllMapUpTo(ctx, testAbsent, 4)
		if err != nil {
			t.Fatalf("GetAllMapUpTo returned error: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("expected no map entries, got %d", len(got))
		}
	})
}

func TestRedisOriginStoreGetMapSeq(t *testing.T) {
	store := newTestStoreWithChain(t)
	ctx := context.Background()

	tests := []struct {
		name     string
		outpoint *transaction.Outpoint
		want     uint32
		wantErr  bool
	}{
		{name: "first map entry", outpoint: testSeq1, want: 1},
		{name: "second map entry", outpoint: testSeq3, want: 3},
		{name: "outpoint in chain without map", outpoint: testSeq2, wantErr: true},
		{name: "outpoint outside chain", outpoint: testAbsent, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := store.GetMapSeq(ctx, testOrigin, tt.outpoint)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got seq %d", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("GetMapSeq returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("expected seq %d, got %d", tt.want, got)
			}
		})
	}
}

func TestRedisOriginStoreWriteBatch(t *testing.T) {
	ctx := context.Background()

	t.Run("writes entries and origin mappings", func(t *testing.T) {
		store := newTestStoreWithChain(t)

		for _, entry := range testChainBatch().Entries {
			info, err := store.GetOrigin(ctx, entry.Outpoint)
			if err != nil {
				t.Fatalf("GetOrigin returned error: %v", err)
			}
			if info == nil {
				t.Fatalf("expected origin info for %s", entry.Outpoint.OrdinalString())
			}
			assertOutpoint(t, info.Origin, testOrigin)
			if info.Seq != entry.Seq {
				t.Errorf("expected seq %d for %s, got %d", entry.Seq, entry.Outpoint.OrdinalString(), info.Seq)
			}
		}

		for seq, want := range map[uint32]*transaction.Outpoint{0: testSeq0, 1: testSeq1, 2: testSeq2, 3: testSeq3, 4: testSeq4} {
			got, err := store.GetSeqAt(ctx, testOrigin, seq)
			if err != nil {
				t.Fatalf("GetSeqAt returned error: %v", err)
			}
			assertOutpoint(t, got, want)
		}

		rev, err := store.GetLatestRevBefore(ctx, testOrigin, 4)
		if err != nil {
			t.Fatalf("GetLatestRevBefore returned error: %v", err)
		}
		assertOutpoint(t, rev.Outpoint, testSeq3)

		mapOutpoint, err := store.GetLatestMapBefore(ctx, testOrigin, 4)
		if err != nil {
			t.Fatalf("GetLatestMapBefore returned error: %v", err)
		}
		assertOutpoint(t, mapOutpoint, testSeq3)

		parent, err := store.GetLatestParentBefore(ctx, testOrigin, 4)
		if err != nil {
			t.Fatalf("GetLatestParentBefore returned error: %v", err)
		}
		assertOutpoint(t, parent, testSeq2)
	})

	t.Run("rewriting a sequence replaces the member", func(t *testing.T) {
		store := newTestStoreWithChain(t)

		replacement := redisTestOutpoint(7, 0)
		if err := store.WriteBatch(ctx, &OriginBatch{
			Origin: testOrigin,
			Entries: []OriginEntry{
				{Outpoint: replacement, Seq: 3, HasRev: true, ContentType: "image/webp", ContentLength: 512, HasMap: true},
			},
		}); err != nil {
			t.Fatalf("failed to write replacement batch: %v", err)
		}

		got, err := store.GetSeqAt(ctx, testOrigin, 3)
		if err != nil {
			t.Fatalf("GetSeqAt returned error: %v", err)
		}
		assertOutpoint(t, got, replacement)

		rev, err := store.GetLatestRevBefore(ctx, testOrigin, 3)
		if err != nil {
			t.Fatalf("GetLatestRevBefore returned error: %v", err)
		}
		assertOutpoint(t, rev.Outpoint, replacement)
		if rev.ContentType != "image/webp" || rev.ContentLength != 512 {
			t.Fatalf("expected image/webp at 512 bytes, got %q at %d bytes", rev.ContentType, rev.ContentLength)
		}

		maps, err := store.GetAllMapUpTo(ctx, testOrigin, 4)
		if err != nil {
			t.Fatalf("GetAllMapUpTo returned error: %v", err)
		}
		if len(maps) != 2 {
			t.Fatalf("expected 2 map entries, got %d", len(maps))
		}
		assertOutpoint(t, maps[1], replacement)
	})

	t.Run("empty batch is a no-op", func(t *testing.T) {
		store := newTestRedisOriginStore(t)
		if err := store.WriteBatch(ctx, &OriginBatch{Origin: testOrigin}); err != nil {
			t.Fatalf("WriteBatch returned error: %v", err)
		}
		got, seq, err := store.GetLatestSeq(ctx, testOrigin)
		if err != nil {
			t.Fatalf("GetLatestSeq returned error: %v", err)
		}
		assertOutpoint(t, got, nil)
		if seq != 0 {
			t.Fatalf("expected seq 0, got %d", seq)
		}
	})
}

func TestRedisOriginStoreAddEntry(t *testing.T) {
	ctx := context.Background()
	store := newTestStoreWithChain(t)

	next := redisTestOutpoint(6, 3)
	if err := store.AddEntry(ctx, testOrigin, &OriginEntry{
		Outpoint:      next,
		Seq:           5,
		HasRev:        true,
		ContentType:   "application/json",
		ContentLength: 42,
		HasMap:        true,
		HasPar:        true,
	}); err != nil {
		t.Fatalf("AddEntry returned error: %v", err)
	}

	t.Run("writes origin mapping with seq", func(t *testing.T) {
		info, err := store.GetOrigin(ctx, next)
		if err != nil {
			t.Fatalf("GetOrigin returned error: %v", err)
		}
		if info == nil {
			t.Fatal("expected origin info, got nil")
		}
		assertOutpoint(t, info.Origin, testOrigin)
		if info.Seq != 5 {
			t.Errorf("expected seq 5, got %d", info.Seq)
		}
	})

	t.Run("advances latest seq", func(t *testing.T) {
		got, seq, err := store.GetLatestSeq(ctx, testOrigin)
		if err != nil {
			t.Fatalf("GetLatestSeq returned error: %v", err)
		}
		assertOutpoint(t, got, next)
		if seq != 5 {
			t.Fatalf("expected seq 5, got %d", seq)
		}
	})

	t.Run("writes rev with metadata", func(t *testing.T) {
		rev, err := store.GetLatestRevBefore(ctx, testOrigin, 5)
		if err != nil {
			t.Fatalf("GetLatestRevBefore returned error: %v", err)
		}
		if rev == nil {
			t.Fatalf("expected rev entry, got nil")
		}
		assertOutpoint(t, rev.Outpoint, next)
		if rev.ContentType != "application/json" || rev.ContentLength != 42 {
			t.Fatalf("expected application/json at 42 bytes, got %q at %d bytes", rev.ContentType, rev.ContentLength)
		}
	})

	t.Run("writes map and parent", func(t *testing.T) {
		mapOutpoint, err := store.GetLatestMapBefore(ctx, testOrigin, 5)
		if err != nil {
			t.Fatalf("GetLatestMapBefore returned error: %v", err)
		}
		assertOutpoint(t, mapOutpoint, next)

		seq, err := store.GetMapSeq(ctx, testOrigin, next)
		if err != nil {
			t.Fatalf("GetMapSeq returned error: %v", err)
		}
		if seq != 5 {
			t.Fatalf("expected map seq 5, got %d", seq)
		}

		parent, err := store.GetLatestParentBefore(ctx, testOrigin, 5)
		if err != nil {
			t.Fatalf("GetLatestParentBefore returned error: %v", err)
		}
		assertOutpoint(t, parent, next)
	})

	t.Run("entry without flags writes only the sequence", func(t *testing.T) {
		plain := redisTestOutpoint(8, 0)
		if err := store.AddEntry(ctx, testOrigin, &OriginEntry{Outpoint: plain, Seq: 6}); err != nil {
			t.Fatalf("AddEntry returned error: %v", err)
		}
		got, err := store.GetSeqAt(ctx, testOrigin, 6)
		if err != nil {
			t.Fatalf("GetSeqAt returned error: %v", err)
		}
		assertOutpoint(t, got, plain)

		rev, err := store.GetLatestRevBefore(ctx, testOrigin, 6)
		if err != nil {
			t.Fatalf("GetLatestRevBefore returned error: %v", err)
		}
		assertOutpoint(t, rev.Outpoint, next)
	})
}

func TestRedisOriginStoreNotFoundOnEmptyStore(t *testing.T) {
	store := newTestRedisOriginStore(t)
	ctx := context.Background()

	t.Run("GetOrigin", func(t *testing.T) {
		got, err := store.GetOrigin(ctx, testAbsent)
		if err != nil {
			t.Fatalf("GetOrigin returned error: %v", err)
		}
		if got != nil {
			t.Fatalf("expected nil, got %+v", got)
		}
	})

	t.Run("GetSeqAt", func(t *testing.T) {
		got, err := store.GetSeqAt(ctx, testOrigin, 0)
		if err != nil {
			t.Fatalf("GetSeqAt returned error: %v", err)
		}
		assertOutpoint(t, got, nil)
	})

	t.Run("GetLatestSeq", func(t *testing.T) {
		got, seq, err := store.GetLatestSeq(ctx, testOrigin)
		if err != nil {
			t.Fatalf("GetLatestSeq returned error: %v", err)
		}
		assertOutpoint(t, got, nil)
		if seq != 0 {
			t.Fatalf("expected seq 0, got %d", seq)
		}
	})

	t.Run("GetLatestRevBefore", func(t *testing.T) {
		got, err := store.GetLatestRevBefore(ctx, testOrigin, 0)
		if err != nil {
			t.Fatalf("GetLatestRevBefore returned error: %v", err)
		}
		if got != nil {
			t.Fatalf("expected nil rev entry, got %s", got.Outpoint.OrdinalString())
		}
	})

	t.Run("GetLatestMapBefore", func(t *testing.T) {
		got, err := store.GetLatestMapBefore(ctx, testOrigin, 0)
		if err != nil {
			t.Fatalf("GetLatestMapBefore returned error: %v", err)
		}
		assertOutpoint(t, got, nil)
	})

	t.Run("GetLatestParentBefore", func(t *testing.T) {
		got, err := store.GetLatestParentBefore(ctx, testOrigin, 0)
		if err != nil {
			t.Fatalf("GetLatestParentBefore returned error: %v", err)
		}
		assertOutpoint(t, got, nil)
	})

	t.Run("GetAllMapUpTo", func(t *testing.T) {
		got, err := store.GetAllMapUpTo(ctx, testOrigin, 0)
		if err != nil {
			t.Fatalf("GetAllMapUpTo returned error: %v", err)
		}
		if got != nil {
			t.Fatalf("expected nil slice, got %d entries", len(got))
		}
	})

	t.Run("GetMapSeq", func(t *testing.T) {
		if _, err := store.GetMapSeq(ctx, testOrigin, testAbsent); err == nil {
			t.Fatalf("expected error for missing map entry")
		}
	})
}

func TestRedisOriginStoreRevMemberEncoding(t *testing.T) {
	tests := []struct {
		name          string
		outpoint      *transaction.Outpoint
		contentType   string
		contentLength uint32
	}{
		{name: "simple content type", outpoint: testSeq0, contentType: "text/plain", contentLength: 11},
		{name: "content type with parameters", outpoint: testSeq3, contentType: "text/html;charset=utf-8", contentLength: 4096},
		{name: "content type with colon", outpoint: testSeq1, contentType: "application/x-thing;a=b:c", contentLength: 1},
		{name: "empty content type", outpoint: testSeq2, contentType: "", contentLength: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			member := encodeRevMember(tt.outpoint, tt.contentLength, tt.contentType)
			got, err := decodeRevMember(member)
			if err != nil {
				t.Fatalf("decodeRevMember returned error: %v", err)
			}
			assertOutpoint(t, got.Outpoint, tt.outpoint)
			if got.ContentType != tt.contentType {
				t.Fatalf("expected content type %q, got %q", tt.contentType, got.ContentType)
			}
			if got.ContentLength != tt.contentLength {
				t.Fatalf("expected content length %d, got %d", tt.contentLength, got.ContentLength)
			}
		})
	}

	t.Run("malformed member is rejected", func(t *testing.T) {
		if _, err := decodeRevMember(testSeq0.OrdinalString()); err == nil {
			t.Fatalf("expected error for member without metadata")
		}
	})
}

func TestSeqFromScore(t *testing.T) {
	tests := []struct {
		name      string
		score     float64
		want      uint32
		expectErr bool
	}{
		{name: "zero", score: 0, want: 0},
		{name: "max uint32", score: float64(math.MaxUint32), want: math.MaxUint32},
		{name: "above uint32 range", score: float64(math.MaxUint32) + 1, expectErr: true},
		{name: "negative", score: -1, expectErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := seqFromScore(tt.score)
			if tt.expectErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("expected %d, got %d", tt.want, got)
			}
		})
	}
}

func TestRedisOrgMemberRoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		origin *transaction.Outpoint
		seq    uint32
	}{
		{name: "seq zero", origin: redisTestOutpoint(1, 0), seq: 0},
		{name: "mid chain", origin: redisTestOutpoint(2, 3), seq: 42},
		{name: "max uint32", origin: redisTestOutpoint(3, 1), seq: ^uint32(0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := decodeOrgMember(encodeOrgMember(tt.origin, tt.seq))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !info.Origin.Equal(tt.origin) {
				t.Errorf("expected origin %s, got %s", tt.origin.OrdinalString(), info.Origin.OrdinalString())
			}
			if info.Seq != tt.seq {
				t.Errorf("expected seq %d, got %d", tt.seq, info.Seq)
			}
		})
	}

	t.Run("malformed member is rejected", func(t *testing.T) {
		for _, bad := range []string{"", "no-separator", "notanoutpoint:1", redisTestOutpoint(1, 0).OrdinalString() + ":notanumber"} {
			if _, err := decodeOrgMember(bad); err == nil {
				t.Errorf("expected error for %q", bad)
			}
		}
	})
}

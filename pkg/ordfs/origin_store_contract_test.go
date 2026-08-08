package ordfs

import (
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// contractStores returns every OriginStore implementation, each loaded with the
// same fixture chain. Implementations are compared against one table so the
// interface contract in interfaces.go holds for all of them.
func contractStores(t *testing.T) map[string]OriginStore {
	t.Helper()

	badgerStore, err := NewBadgerOriginStore(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create badger store: %v", err)
	}
	t.Cleanup(func() {
		if err := badgerStore.Close(); err != nil {
			t.Errorf("failed to close badger store: %v", err)
		}
	})

	mr := miniredis.RunT(t)
	redisStore, err := NewRedisOriginStore(t.Context(), "redis://"+mr.Addr())
	if err != nil {
		t.Fatalf("failed to create redis store: %v", err)
	}
	t.Cleanup(func() {
		if err := redisStore.Close(); err != nil {
			t.Errorf("failed to close redis store: %v", err)
		}
	})

	stores := map[string]OriginStore{"badger": badgerStore, "redis": redisStore}

	// Fixture chain: revisions at seq 0 and 3, map entries at 1 and 3, parent at 2.
	origin := testOutpoint(t, 1, 0)
	for name, store := range stores {
		if err := store.WriteBatch(t.Context(), &OriginBatch{
			Origin: origin,
			Entries: []OriginEntry{
				{Outpoint: origin, Seq: 0, HasRev: true, ContentType: "text/plain", ContentLength: 11},
				{Outpoint: testOutpoint(t, 2, 0), Seq: 1, HasMap: true},
				{Outpoint: testOutpoint(t, 3, 0), Seq: 2, HasPar: true},
				{Outpoint: testOutpoint(t, 4, 0), Seq: 3, HasRev: true, ContentType: "image/png", ContentLength: 2048, HasMap: true},
				{Outpoint: testOutpoint(t, 5, 0), Seq: 4},
			},
		}); err != nil {
			t.Fatalf("failed to write fixture batch to %s store: %v", name, err)
		}
	}
	return stores
}

// TestOriginStoreLatestBeforeContract pins the "at or before seq" contract from
// interfaces.go. The Badger store previously seeked seq+1, so it returned
// entries one sequence past the requested one (and wrapped at MaxUint32).
func TestOriginStoreLatestBeforeContract(t *testing.T) {
	for name, store := range contractStores(t) {
		t.Run(name, func(t *testing.T) {
			t.Run("rev", func(t *testing.T) {
				tests := []struct {
					name            string
					seq             uint32
					wantOutpoint    *transaction.Outpoint
					wantContentType string
				}{
					{name: "inclusive at first revision", seq: 0, wantOutpoint: testOutpoint(t, 1, 0), wantContentType: "text/plain"},
					{name: "between revisions holds the earlier one", seq: 2, wantOutpoint: testOutpoint(t, 1, 0), wantContentType: "text/plain"},
					{name: "inclusive at second revision", seq: 3, wantOutpoint: testOutpoint(t, 4, 0), wantContentType: "image/png"},
					{name: "past the tip returns the latest", seq: 4, wantOutpoint: testOutpoint(t, 4, 0), wantContentType: "image/png"},
					{name: "max uint32 does not wrap", seq: ^uint32(0), wantOutpoint: testOutpoint(t, 4, 0), wantContentType: "image/png"},
				}
				for _, tt := range tests {
					t.Run(tt.name, func(t *testing.T) {
						entry, err := store.GetLatestRevBefore(t.Context(), testOutpoint(t, 1, 0), tt.seq)
						if err != nil {
							t.Fatalf("unexpected error: %v", err)
						}
						if entry == nil {
							t.Fatalf("expected a revision at or before seq %d", tt.seq)
						}
						if !entry.Outpoint.Equal(tt.wantOutpoint) {
							t.Errorf("expected outpoint %s, got %s", tt.wantOutpoint.OrdinalString(), entry.Outpoint.OrdinalString())
						}
						if entry.ContentType != tt.wantContentType {
							t.Errorf("expected content type %s, got %s", tt.wantContentType, entry.ContentType)
						}
					})
				}
			})

			t.Run("map", func(t *testing.T) {
				tests := []struct {
					name string
					seq  uint32
					want *transaction.Outpoint
				}{
					{name: "absent before the first map entry", seq: 0, want: nil},
					{name: "inclusive at the first map entry", seq: 1, want: testOutpoint(t, 2, 0)},
					{name: "between entries holds the earlier one", seq: 2, want: testOutpoint(t, 2, 0)},
					{name: "inclusive at the second map entry", seq: 3, want: testOutpoint(t, 4, 0)},
				}
				for _, tt := range tests {
					t.Run(tt.name, func(t *testing.T) {
						got, err := store.GetLatestMapBefore(t.Context(), testOutpoint(t, 1, 0), tt.seq)
						if err != nil {
							t.Fatalf("unexpected error: %v", err)
						}
						assertContractOutpoint(t, got, tt.want)
					})
				}
			})

			t.Run("parent", func(t *testing.T) {
				tests := []struct {
					name string
					seq  uint32
					want *transaction.Outpoint
				}{
					{name: "absent before the parent entry", seq: 1, want: nil},
					{name: "inclusive at the parent entry", seq: 2, want: testOutpoint(t, 3, 0)},
					{name: "past the entry returns it", seq: 4, want: testOutpoint(t, 3, 0)},
				}
				for _, tt := range tests {
					t.Run(tt.name, func(t *testing.T) {
						got, err := store.GetLatestParentBefore(t.Context(), testOutpoint(t, 1, 0), tt.seq)
						if err != nil {
							t.Fatalf("unexpected error: %v", err)
						}
						assertContractOutpoint(t, got, tt.want)
					})
				}
			})
		})
	}
}

// TestOriginStoreGetOriginContract pins that every implementation returns the
// origin AND the sequence the outpoint holds, so callers never need a second
// lookup to number a chain.
func TestOriginStoreGetOriginContract(t *testing.T) {
	for name, store := range contractStores(t) {
		t.Run(name, func(t *testing.T) {
			tests := []struct {
				name     string
				outpoint *transaction.Outpoint
				wantSeq  uint32
				wantNil  bool
			}{
				{name: "origin maps to itself at seq 0", outpoint: testOutpoint(t, 1, 0), wantSeq: 0},
				{name: "mid chain carries its seq", outpoint: testOutpoint(t, 3, 0), wantSeq: 2},
				{name: "tip carries its seq", outpoint: testOutpoint(t, 5, 0), wantSeq: 4},
				{name: "unknown outpoint returns nil", outpoint: testOutpoint(t, 9, 9), wantNil: true},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					info, err := store.GetOrigin(t.Context(), tt.outpoint)
					if err != nil {
						t.Fatalf("unexpected error: %v", err)
					}
					if tt.wantNil {
						if info != nil {
							t.Fatalf("expected nil, got %+v", info)
						}
						return
					}
					if info == nil {
						t.Fatal("expected origin info, got nil")
					}
					if !info.Origin.Equal(testOutpoint(t, 1, 0)) {
						t.Errorf("expected origin %s, got %s", testOutpoint(t, 1, 0).OrdinalString(), info.Origin.OrdinalString())
					}
					if info.Seq != tt.wantSeq {
						t.Errorf("expected seq %d, got %d", tt.wantSeq, info.Seq)
					}
				})
			}
		})
	}
}

func assertContractOutpoint(t *testing.T, got, want *transaction.Outpoint) {
	t.Helper()
	if want == nil {
		if got != nil {
			t.Errorf("expected nil, got %s", got.OrdinalString())
		}
		return
	}
	if got == nil {
		t.Fatalf("expected %s, got nil", want.OrdinalString())
	}
	if !got.Equal(want) {
		t.Errorf("expected %s, got %s", want.OrdinalString(), got.OrdinalString())
	}
}

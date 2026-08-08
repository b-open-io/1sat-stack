package ordfs

import (
	"context"

	"github.com/bsv-blockchain/go-sdk/transaction"
)

// OriginStore persists ordinal chain structure.
//
// Key layout (Badger):
//
//	org: + [36B outpoint] → [36B origin] + [4B seq]
//	seq: + [36B origin] + [4B seq] → [36B outpoint]
//	rev: + [36B origin] + [4B seq] → [36B outpoint] + [4B contentLength] + [contentType string]
//	map: + [36B origin] + [4B seq] → [36B outpoint]
//	par: + [36B origin] + [4B seq] → [36B outpoint]
type OriginStore interface {
	// GetOrigin returns origin + sequence for any indexed outpoint. Nil if unknown.
	GetOrigin(ctx context.Context, outpoint *transaction.Outpoint) (*OriginInfo, error)

	// GetSeqAt returns the outpoint at a specific sequence for an origin.
	GetSeqAt(ctx context.Context, origin *transaction.Outpoint, seq uint32) (*transaction.Outpoint, error)

	// GetLatestSeq returns the highest sequence entry for an origin.
	GetLatestSeq(ctx context.Context, origin *transaction.Outpoint) (*transaction.Outpoint, uint32, error)

	// GetLatestRevBefore returns the most recent content revision at or before seq.
	GetLatestRevBefore(ctx context.Context, origin *transaction.Outpoint, seq uint32) (*RevEntry, error)

	// GetLatestMapBefore returns the most recent MAP outpoint at or before seq.
	GetLatestMapBefore(ctx context.Context, origin *transaction.Outpoint, seq uint32) (*transaction.Outpoint, error)

	// GetLatestParentBefore returns the most recent parent outpoint at or before seq.
	GetLatestParentBefore(ctx context.Context, origin *transaction.Outpoint, seq uint32) (*transaction.Outpoint, error)

	// GetAllMapUpTo returns all MAP outpoints from sequence 0 through seq (inclusive), ordered by sequence.
	GetAllMapUpTo(ctx context.Context, origin *transaction.Outpoint, seq uint32) ([]*transaction.Outpoint, error)

	// GetMapSeq returns the sequence number for a specific outpoint in the map index.
	GetMapSeq(ctx context.Context, origin *transaction.Outpoint, outpoint *transaction.Outpoint) (uint32, error)

	// WriteBatch atomically writes a set of chain entries during migration/crawl.
	WriteBatch(ctx context.Context, batch *OriginBatch) error

	// AddEntry adds a single chain entry during forward crawl.
	AddEntry(ctx context.Context, origin *transaction.Outpoint, entry *OriginEntry) error

	Close() error
}

// OriginInfo is the origin mapping for an indexed chain outpoint.
type OriginInfo struct {
	Origin *transaction.Outpoint
	Seq    uint32
}

// RevEntry holds a content revision outpoint with its metadata.
type RevEntry struct {
	Outpoint      *transaction.Outpoint
	ContentType   string
	ContentLength uint32
}

// OriginEntry represents a single entry to write.
type OriginEntry struct {
	Outpoint      *transaction.Outpoint
	Seq           uint32
	HasRev        bool
	HasMap        bool
	HasPar        bool
	ContentType   string // populated when HasRev
	ContentLength uint32 // populated when HasRev
}

// OriginBatch represents an atomic batch of chain entries.
// Each entry writes org: (origin+seq) and seq: (and optional rev/map/par).
type OriginBatch struct {
	Origin  *transaction.Outpoint
	Entries []OriginEntry
}

// Cache is a generic key-value cache with pluggable providers.
type Cache interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte) error
	Delete(ctx context.Context, key string) error
}

// CrawlCoordinator handles concurrent crawl deduplication.
type CrawlCoordinator interface {
	// AcquireLock attempts to acquire an exclusive crawl lock. Returns false if already held.
	AcquireLock(ctx context.Context, outpoint *transaction.Outpoint) (bool, error)
	// ReleaseLock releases the crawl lock.
	ReleaseLock(outpoint *transaction.Outpoint)
	// RefreshLocks extends the TTL on all held locks.
	RefreshLocks(ctx context.Context, outpoints []*transaction.Outpoint)
	// WaitForCrawl blocks until the crawl for this outpoint completes.
	WaitForCrawl(ctx context.Context, outpoint *transaction.Outpoint) error
	// PublishComplete notifies waiters that crawl completed with the given origin.
	PublishComplete(outpoints []*transaction.Outpoint, origin string)
	// PublishFailure notifies waiters that crawl failed.
	PublishFailure(outpoints []*transaction.Outpoint)
}

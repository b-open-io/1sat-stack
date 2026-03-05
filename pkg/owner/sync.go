package owner

import (
	"context"
	"encoding/binary"
	"log/slog"
	"sync"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/dedup"
	"github.com/b-open-io/1sat-stack/pkg/indexer"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/b-open-io/go-junglebus"
)

const defaultSyncConcurrency = 16

// SyncProgress reports the state of an owner sync operation.
type SyncProgress struct {
	Phase     string `json:"phase"`               // "fetch", "ingest", "done", "error"
	Total     int    `json:"total,omitempty"`     // Total txns to process (set after fetch)
	Processed int    `json:"processed,omitempty"` // Txns processed so far
	Error     string `json:"error,omitempty"`     // Error message if phase=="error"
	Owner     string `json:"owner,omitempty"`     // Owner being synced
	Height    uint32 `json:"height,omitempty"`    // Last synced height
}

// OwnerSync handles syncing transactions for owners from JungleBus
type OwnerSync struct {
	jb          *junglebus.Client
	beefStorage *beef.Storage
	indexer     *indexer.IngestCtx
	outputStore *txo.OutputStore
	concurrency int
	logger      *slog.Logger
	syncDedup   *dedup.Loader[string, struct{}]
}

// NewOwnerSync creates a new OwnerSync instance
func NewOwnerSync(
	jb *junglebus.Client,
	beefStorage *beef.Storage,
	idx *indexer.IngestCtx,
	outputStore *txo.OutputStore,
	logger *slog.Logger,
) *OwnerSync {
	if logger == nil {
		logger = slog.Default()
	}
	s := &OwnerSync{
		jb:          jb,
		beefStorage: beefStorage,
		indexer:     idx,
		outputStore: outputStore,
		concurrency: defaultSyncConcurrency,
		logger:      logger,
	}
	s.syncDedup = dedup.NewLoader(func(owner string) (struct{}, error) {
		return struct{}{}, s.sync(context.Background(), owner)
	})
	return s
}

// WithConcurrency sets the concurrency level for syncing
func (s *OwnerSync) WithConcurrency(n int) *OwnerSync {
	s.concurrency = n
	return s
}

// Sync syncs all transactions for an owner from JungleBus.
// Concurrent calls for the same owner are deduplicated — only one sync runs
// at a time per owner, and other callers wait for the result.
func (s *OwnerSync) Sync(ctx context.Context, owner string) error {
	_, err := s.syncDedup.Load(owner)
	return err
}

// SyncWithProgress syncs an owner and sends progress updates to the provided channel.
// Unlike Sync, this bypasses deduplication so the caller always receives progress events.
// The channel is NOT closed by this method — the caller owns it.
func (s *OwnerSync) SyncWithProgress(ctx context.Context, owner string, progress chan<- SyncProgress) error {
	return s.syncWithProgress(ctx, owner, progress)
}

// sync performs the actual sync work for an owner (no progress reporting).
func (s *OwnerSync) sync(ctx context.Context, owner string) error {
	return s.syncWithProgress(ctx, owner, nil)
}

// syncWithProgress performs the actual sync work for an owner.
// If progress is non-nil, it sends SyncProgress updates as work proceeds.
func (s *OwnerSync) syncWithProgress(ctx context.Context, owner string, progress chan<- SyncProgress) error {
	s.logger.Debug("OwnerSync starting", "owner", owner)

	sendProgress := func(p SyncProgress) {
		if progress == nil {
			return
		}
		p.Owner = owner
		select {
		case progress <- p:
		case <-ctx.Done():
		}
	}

	sendProgress(SyncProgress{Phase: "fetch"})

	// Get last synced height (0 if not found)
	var lastHeight float64
	if progressBytes, err := s.outputStore.Store.HGet(ctx, txo.KeyProgress, []byte(owner)); err == nil && len(progressBytes) == 4 {
		lastHeight = float64(binary.BigEndian.Uint32(progressBytes))
	} else if err != nil && err != store.ErrKeyNotFound {
		s.logger.Error("OwnerSync: failed to get last height", "owner", owner, "error", err)
		sendProgress(SyncProgress{Phase: "error", Error: err.Error()})
		return err
	}

	s.logger.Debug("OwnerSync: fetching from JungleBus", "owner", owner, "fromHeight", lastHeight)

	// Fetch transactions from JungleBus
	addTxns, err := s.jb.GetAddressTransactions(ctx, owner, uint32(lastHeight))
	if err != nil {
		s.logger.Error("OwnerSync: JungleBus fetch failed", "owner", owner, "error", err)
		sendProgress(SyncProgress{Phase: "error", Error: err.Error()})
		return err
	}

	s.logger.Debug("OwnerSync: fetched transactions", "owner", owner, "count", len(addTxns))

	if len(addTxns) == 0 {
		s.logger.Debug("OwnerSync: no new transactions", "owner", owner)
		sendProgress(SyncProgress{Phase: "done", Height: uint32(lastHeight)})
		return nil
	}

	// Filter out already-synced txns to get accurate total
	var toProcess []struct {
		txid        string
		blockHeight uint32
	}
	for _, addTxn := range addTxns {
		if float64(addTxn.BlockHeight) >= lastHeight {
			toProcess = append(toProcess, struct {
				txid        string
				blockHeight uint32
			}{addTxn.TransactionID, addTxn.BlockHeight})
		}
	}

	total := len(toProcess)
	sendProgress(SyncProgress{Phase: "ingest", Total: total, Processed: 0})

	limiter := make(chan struct{}, s.concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	var processed int
	var newMaxHeight float64 = lastHeight

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	for _, item := range toProcess {
		// Stop if context cancelled
		if ctx.Err() != nil {
			break
		}

		wg.Add(1)
		limiter <- struct{}{}

		go func(txid string, blockHeight uint32) {
			defer func() {
				<-limiter
				wg.Done()
			}()

			if _, err := s.indexer.IngestTxid(ctx, txid); err != nil {
				s.logger.Error("OwnerSync: error ingesting txid", "txid", txid, "error", err)
				mu.Lock()
				if firstErr == nil {
					firstErr = err
					cancel()
				}
				mu.Unlock()
			} else {
				mu.Lock()
				processed++
				if float64(blockHeight) > newMaxHeight {
					newMaxHeight = float64(blockHeight)
				}
				mu.Unlock()

				sendProgress(SyncProgress{
					Phase:     "ingest",
					Total:     total,
					Processed: processed,
				})
			}
		}(item.txid, item.blockHeight)
	}

	wg.Wait()

	if firstErr != nil {
		sendProgress(SyncProgress{Phase: "error", Error: firstErr.Error()})
		return firstErr
	}

	// Update sync progress
	progressBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(progressBytes, uint32(newMaxHeight))
	if err := s.outputStore.Store.HSet(ctx, txo.KeyProgress, []byte(owner), progressBytes); err != nil {
		sendProgress(SyncProgress{Phase: "error", Error: err.Error()})
		return err
	}

	sendProgress(SyncProgress{Phase: "done", Height: uint32(newMaxHeight)})
	s.logger.Debug("OwnerSync complete", "owner", owner, "processed", processed)
	return nil
}

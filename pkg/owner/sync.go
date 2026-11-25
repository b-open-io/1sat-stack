package owner

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/config"
	"github.com/b-open-io/1sat-stack/pkg/dedup"
	"github.com/b-open-io/1sat-stack/pkg/indexer"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/b-open-io/go-junglebus"
	"github.com/bsv-blockchain/go-sdk/chainhash"
)

// confirmedScoreBoundary separates confirmed HeightScore values (block height
// ~850k) from mempool unix-timestamp scores (~1.7e9). Same split as the
// pending auditor.
const confirmedScoreBoundary = 10_000_000

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
	configStore config.Store
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
	cs config.Store,
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
		configStore: cs,
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
	if val, err := s.configStore.Get(ctx, "progress:"+owner); err == nil {
		if parsed, parseErr := strconv.ParseUint(val, 10, 32); parseErr == nil {
			lastHeight = float64(parsed)
		}
	} else if !errors.Is(err, config.ErrNotFound) {
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

	// Filter out already-synced mined txns. Height 0 is JungleBus mempool:
	// those rows have no height, so they cannot be cursor'd via lastHeight
	// and must be included every fetch. skipIngest de-dupes them.
	var toProcess []struct {
		txid        string
		blockHeight uint32
	}
	for _, addTxn := range addTxns {
		if includeJungleBusTx(addTxn.BlockHeight, lastHeight) {
			toProcess = append(toProcess, struct {
				txid        string
				blockHeight uint32
			}{addTxn.TransactionID, addTxn.BlockHeight})
		}
	}

	total := len(toProcess)
	s.logger.Info("OwnerSync: starting ingest",
		"owner", owner,
		"jbTotal", len(addTxns),
		"filtered", total,
		"fromHeight", lastHeight,
	)
	sendProgress(SyncProgress{Phase: "ingest", Total: total, Processed: 0})

	limiter := make(chan struct{}, s.concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	var processed int
	var dispatched int
	var newMaxHeight float64 = lastHeight

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	for _, item := range toProcess {
		// Stop if context cancelled
		if ctx.Err() != nil {
			s.logger.Warn("OwnerSync: context cancelled, stopping dispatch",
				"owner", owner,
				"dispatched", dispatched,
				"total", total,
			)
			break
		}

		dispatched++
		s.logger.Debug("OwnerSync: dispatching",
			"txid", item.txid,
			"height", item.blockHeight,
			"n", dispatched,
			"total", total,
		)
		wg.Add(1)
		limiter <- struct{}{}

		go func(txid string, blockHeight uint32) {
			defer func() {
				<-limiter
				wg.Done()
			}()

			skip, err := s.skipIngest(ctx, txid, blockHeight)
			if err != nil {
				s.logger.Error("OwnerSync: skip check failed", "txid", txid, "error", err)
			} else if skip {
				mu.Lock()
				processed++
				mu.Unlock()
				sendProgress(SyncProgress{
					Phase:     "ingest",
					Total:     total,
					Processed: processed,
				})
				return
			}

			if _, err := s.indexer.IngestTxid(ctx, txid); err != nil {
				s.logger.Error("OwnerSync: error ingesting txid", "txid", txid, "height", blockHeight, "error", err)
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

	s.logger.Info("OwnerSync: ingest complete",
		"owner", owner,
		"dispatched", dispatched,
		"processed", processed,
		"total", total,
		"hasError", firstErr != nil,
	)

	if firstErr != nil {
		sendProgress(SyncProgress{Phase: "error", Error: firstErr.Error()})
		return firstErr
	}

	// Update sync progress
	if err := s.configStore.Set(ctx, "progress:"+owner, strconv.FormatUint(uint64(newMaxHeight), 10)); err != nil {
		sendProgress(SyncProgress{Phase: "error", Error: err.Error()})
		return err
	}

	sendProgress(SyncProgress{Phase: "done", Height: uint32(newMaxHeight)})
	return nil
}

// includeJungleBusTx reports whether a JungleBus address row should be
// considered for ingest. Mined txs use lastHeight as a cursor. Mempool txs
// (BlockHeight == 0) have no height and are always candidates; skipIngest
// drops ones already stored.
func includeJungleBusTx(blockHeight uint32, lastHeight float64) bool {
	return blockHeight == 0 || float64(blockHeight) >= lastHeight
}

// shouldSkipIngest reports whether IngestTxid can be skipped.
//
//   - Never stored: ingest.
//   - In immutable log: skip (already confirmed).
//   - In pending, this fetch is mempool (height 0): skip (already indexed).
//   - In pending with a confirmed score, this fetch is mined: skip.
//   - In pending with a mempool score, this fetch is mined: re-ingest so
//     ZAdd moves owner-set members from timestamp → height.
func shouldSkipIngest(blockHeight uint32, pendingScore *float64, inImmutable bool) bool {
	if inImmutable {
		return true
	}
	if pendingScore == nil {
		return false
	}
	if blockHeight == 0 {
		return true
	}
	return *pendingScore < confirmedScoreBoundary
}

func (s *OwnerSync) skipIngest(ctx context.Context, txid string, blockHeight uint32) (bool, error) {
	if s.outputStore == nil {
		return false, nil
	}
	hash, err := chainhash.NewHashFromHex(txid)
	if err != nil {
		return false, err
	}

	pendingScore, err := s.outputStore.Store.ZScore(ctx, txo.KeyLog(txo.PendingTxLog), hash[:])
	if err == nil {
		return shouldSkipIngest(blockHeight, &pendingScore, false), nil
	}
	if !errors.Is(err, store.ErrKeyNotFound) {
		return false, err
	}

	_, err = s.outputStore.Store.ZScore(ctx, txo.KeyLog(txo.ImmutableTxLog), hash[:])
	if err == nil {
		return shouldSkipIngest(blockHeight, nil, true), nil
	}
	if !errors.Is(err, store.ErrKeyNotFound) {
		return false, err
	}
	return shouldSkipIngest(blockHeight, nil, false), nil
}

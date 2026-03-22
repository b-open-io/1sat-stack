package indexer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bsv-blockchain/arcade/service"
	"github.com/bsv-blockchain/go-chaintracks/chaintracks"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/redis/go-redis/v9"
)

const (
	// confirmedBoundary separates confirmed scores (block height ~850k) from
	// unconfirmed scores (unix timestamp ~1.7e9).
	confirmedBoundary = 10_000_000

	// proofCheckAge is the minimum age before checking unconfirmed txs for proofs.
	proofCheckAge = 10 * time.Minute

	// rollbackAge is the maximum age for an unconfirmed tx before rollback.
	rollbackAge = 3 * time.Hour

	// auditConcurrency is the number of concurrent goroutines for audit processing.
	auditConcurrency = 8
)

// PendingAuditor audits the tx:pending sorted set on each new block.
// It promotes confirmed+deep transactions to tx:immutable and rolls back
// stale unconfirmed transactions that have no proof after rollbackAge.
type PendingAuditor struct {
	redis         *redis.Client
	outputStore   *txo.OutputStore
	beefStorage   *beef.Storage
	indexer       *IngestCtx
	chaintracks   chaintracks.Chaintracks
	arcadeService service.ArcadeService // may be nil
	logger        *slog.Logger
	auditing      atomic.Bool

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewPendingAuditor creates a new PendingAuditor.
func NewPendingAuditor(
	rdb *redis.Client,
	outputStore *txo.OutputStore,
	beefStorage *beef.Storage,
	indexer *IngestCtx,
	ct chaintracks.Chaintracks,
	arcadeService service.ArcadeService,
	logger *slog.Logger,
) *PendingAuditor {
	if logger == nil {
		logger = slog.Default()
	}
	return &PendingAuditor{
		redis:         rdb,
		outputStore:   outputStore,
		beefStorage:   beefStorage,
		indexer:       indexer,
		chaintracks:   ct,
		arcadeService: arcadeService,
		logger:        logger.With("component", "pending-auditor"),
	}
}

// Start subscribes to chaintracks for new block events and triggers audits.
func (a *PendingAuditor) Start(ctx context.Context) {
	a.ctx, a.cancel = context.WithCancel(ctx)
	blockCh := a.chaintracks.Subscribe(a.ctx)

	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer a.chaintracks.Unsubscribe(blockCh)

		a.logger.Info("pending auditor started")

		for {
			select {
			case <-a.ctx.Done():
				a.logger.Info("pending auditor shutting down")
				return
			case header, ok := <-blockCh:
				if !ok {
					a.logger.Info("block channel closed")
					return
				}
				go a.RunAudit(a.ctx, header.Height)
			}
		}
	}()
}

// Stop stops the auditor gracefully.
func (a *PendingAuditor) Stop() {
	if a.cancel != nil {
		a.cancel()
	}
	a.wg.Wait()
	a.logger.Info("pending auditor stopped")
}

// RunAudit processes the tx:pending sorted set for the given chain tip height.
// Only one audit runs at a time — concurrent calls are skipped.
func (a *PendingAuditor) RunAudit(ctx context.Context, tipHeight uint32) {
	if !a.auditing.CompareAndSwap(false, true) {
		a.logger.Debug("audit already in progress, skipping")
		return
	}
	defer a.auditing.Store(false)

	a.logger.Info("running pending audit", "tipHeight", tipHeight)

	var immutablePromoted, proofsFound, rolledBack, stillPending int

	// Range 1: Immutable candidates — confirmed txs deep enough (score < tipHeight - 100)
	if tipHeight > txo.ImmutabilityBlocks {
		immutableThreshold := types.HeightScore(tipHeight-txo.ImmutabilityBlocks, 0)
		min := float64(0)
		results, err := a.redis.ZRangeByScoreWithScores(ctx, string(txo.KeyLog(txo.PendingTxLog)), &redis.ZRangeBy{
			Min: fmt.Sprintf("%f", min),
			Max: fmt.Sprintf("%f", immutableThreshold),
		}).Result()
		if err != nil {
			a.logger.Error("failed to query immutable candidates", "error", err)
		} else {
			immutablePromoted, proofsFound = a.processImmutableCandidates(ctx, results)
		}
	}

	// Range 2: Unconfirmed audit — unconfirmed txs older than proofCheckAge
	proofCheckCutoff := float64(time.Now().Add(-proofCheckAge).UnixNano()) / 1e9
	members, err := a.redis.ZRangeByScoreWithScores(ctx, string(txo.KeyLog(txo.PendingTxLog)), &redis.ZRangeBy{
		Min: fmt.Sprintf("%f", float64(confirmedBoundary)),
		Max: fmt.Sprintf("%f", proofCheckCutoff),
	}).Result()
	if err != nil {
		a.logger.Error("failed to query unconfirmed txs", "error", err)
	} else {
		found, rolled, pending := a.processUnconfirmed(ctx, members)
		proofsFound += found
		rolledBack = rolled
		stillPending = pending
	}

	a.logger.Info("pending audit complete",
		"tipHeight", tipHeight,
		"immutable", immutablePromoted,
		"proofsFound", proofsFound,
		"rolledBack", rolledBack,
		"stillPending", stillPending,
	)
}

// processImmutableCandidates verifies proofs for confirmed txs and promotes valid ones.
func (a *PendingAuditor) processImmutableCandidates(ctx context.Context, members []redis.Z) (promoted, refetched int) {
	limiter := make(chan struct{}, auditConcurrency)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, m := range members {
		if ctx.Err() != nil {
			break
		}

		txid, err := chainhash.NewHash([]byte(m.Member.(string)))
		if err != nil {
			a.logger.Error("invalid txid in pending set", "error", err)
			continue
		}
		txidHex := txid.String()
		wg.Add(1)
		limiter <- struct{}{}

		go func(txid *chainhash.Hash, txidHex string, score float64) {
			defer func() { <-limiter; wg.Done() }()

			// Load tx from BEEF to check its proof
			tx, err := a.beefStorage.LoadTx(ctx, txid)
			if err != nil {
				a.logger.Warn("failed to load tx for immutability check", "txid", txidHex, "error", err)
				return
			}

			if tx == nil || tx.MerklePath == nil {
				// Confirmed score but no proof — shouldn't happen, but try to refetch
				a.logger.Warn("confirmed tx missing proof, attempting refetch", "txid", txidHex)
				if a.refetchAndReingest(ctx, txid, txidHex) {
					mu.Lock()
					refetched++
					mu.Unlock()
				}
				return
			}

			// Verify proof against chaintracks
			root, err := tx.MerklePath.ComputeRoot(txid)
			if err != nil {
				a.logger.Error("failed to compute merkle root", "txid", txidHex, "error", err)
				return
			}

			valid, err := a.chaintracks.IsValidRootForHeight(ctx, root, tx.MerklePath.BlockHeight)
			if err != nil {
				a.logger.Error("failed to validate merkle root", "txid", txidHex, "error", err)
				return
			}

			if !valid {
				// Proof is invalid (likely reorg) — refetch
				a.logger.Warn("invalid proof detected, refetching", "txid", txidHex)
				if a.refetchAndReingest(ctx, txid, txidHex) {
					mu.Lock()
					refetched++
					mu.Unlock()
				}
				return
			}

			// Proof is valid and deep enough — promote to immutable
			if err := a.redis.ZAdd(ctx, string(txo.KeyLog(txo.ImmutableTxLog)), redis.Z{
				Member: string(txid[:]),
				Score:  score,
			}).Err(); err != nil {
				a.logger.Error("failed to add to immutable", "txid", txidHex, "error", err)
				return
			}
			if err := a.redis.ZRem(ctx, string(txo.KeyLog(txo.PendingTxLog)), string(txid[:])).Err(); err != nil {
				a.logger.Error("failed to remove from pending", "txid", txidHex, "error", err)
			}

			mu.Lock()
			promoted++
			mu.Unlock()
		}(txid, txidHex, m.Score)
	}

	wg.Wait()
	return
}

// processUnconfirmed attempts to find proofs for unconfirmed transactions.
func (a *PendingAuditor) processUnconfirmed(ctx context.Context, members []redis.Z) (proofsFound, rolledBack, stillPending int) {
	rollbackCutoff := float64(time.Now().Add(-rollbackAge).UnixNano()) / 1e9
	limiter := make(chan struct{}, auditConcurrency)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, m := range members {
		if ctx.Err() != nil {
			break
		}

		txid, err := chainhash.NewHash([]byte(m.Member.(string)))
		if err != nil {
			a.logger.Error("invalid txid in pending set", "error", err)
			continue
		}
		txidHex := txid.String()
		score := m.Score
		wg.Add(1)
		limiter <- struct{}{}

		go func(txid *chainhash.Hash, txidHex string, score float64) {
			defer func() { <-limiter; wg.Done() }()

			// Try Arcade first (opportunistic — 404 is not authoritative)
			if a.arcadeService != nil {
				status, err := a.arcadeService.GetStatus(ctx, txidHex)
				if err == nil && status != nil && len(status.MerklePath) > 0 {
					// Arcade has a proof — update BEEF and re-ingest
					if a.applyMerkleProof(ctx, txid, txidHex, status.MerklePath) {
						mu.Lock()
						proofsFound++
						mu.Unlock()
						return
					}
				}
			}

			// Try JungleBus via UpdateMerklePath
			updatedBeef, err := a.beefStorage.UpdateMerklePath(ctx, txid, a.chaintracks)
			if err == nil && len(updatedBeef) > 0 {
				// JungleBus returned proof — re-ingest to update scores
				if _, err := a.indexer.IngestTxid(ctx, txidHex); err != nil {
					a.logger.Error("failed to re-ingest after proof update", "txid", txidHex, "error", err)
				} else {
					a.logger.Info("proof found via JungleBus", "txid", txidHex)
					mu.Lock()
					proofsFound++
					mu.Unlock()
				}
				return
			}

			// Determine if this was an explicit "not found" or an error
			isNotFound := err != nil && errors.Is(err, beef.ErrNotFound)
			isError := err != nil && !isNotFound

			if isError {
				// Server error — skip, retry next block
				a.logger.Debug("JungleBus error, will retry", "txid", txidHex, "error", err)
				mu.Lock()
				stillPending++
				mu.Unlock()
				return
			}

			// No proof found — check rollback threshold
			if score <= rollbackCutoff {
				a.logger.Info("rolling back stale unconfirmed tx", "txid", txidHex)
				if err := a.outputStore.Rollback(ctx, txid); err != nil {
					a.logger.Error("rollback failed", "txid", txidHex, "error", err)
					mu.Lock()
					stillPending++
					mu.Unlock()
					return
				}
				// Remove from pending, add to rollback log
				a.redis.ZRem(ctx, string(txo.KeyLog(txo.PendingTxLog)), string(txid[:]))
				a.redis.ZAdd(ctx, string(txo.KeyLog(txo.RollbackTxLog)), redis.Z{
					Member: string(txid[:]),
					Score:  types.HeightScore(0, 0),
				})
				mu.Lock()
				rolledBack++
				mu.Unlock()
				return
			}

			// Not old enough to rollback — skip
			mu.Lock()
			stillPending++
			mu.Unlock()
		}(txid, txidHex, score)
	}

	wg.Wait()
	return
}

// refetchAndReingest attempts to get a fresh proof from Arcade or JungleBus and re-ingest.
func (a *PendingAuditor) refetchAndReingest(ctx context.Context, txid *chainhash.Hash, txidHex string) bool {
	// Try Arcade
	if a.arcadeService != nil {
		status, err := a.arcadeService.GetStatus(ctx, txidHex)
		if err == nil && status != nil && len(status.MerklePath) > 0 {
			if a.applyMerkleProof(ctx, txid, txidHex, status.MerklePath) {
				return true
			}
		}
	}

	// Try JungleBus
	_, err := a.beefStorage.UpdateMerklePath(ctx, txid, a.chaintracks)
	if err != nil {
		a.logger.Warn("failed to refetch proof", "txid", txidHex, "error", err)
		return false
	}

	// Re-ingest with updated BEEF
	if _, err := a.indexer.IngestTxid(ctx, txidHex); err != nil {
		a.logger.Error("failed to re-ingest after refetch", "txid", txidHex, "error", err)
		return false
	}

	a.logger.Info("proof refetched and re-ingested", "txid", txidHex)
	return true
}

// applyMerkleProof parses a binary merkle path, attaches it to the tx's BEEF, and re-ingests.
func (a *PendingAuditor) applyMerkleProof(ctx context.Context, txid *chainhash.Hash, txidHex string, merklePathBytes []byte) bool {
	merklePath, err := transaction.NewMerklePathFromBinary(merklePathBytes)
	if err != nil {
		a.logger.Error("failed to parse merkle path", "txid", txidHex, "error", err)
		return false
	}

	// Load tx from BEEF storage and attach the proof
	tx, err := a.beefStorage.LoadTx(ctx, txid)
	if err != nil || tx == nil {
		a.logger.Warn("could not load tx for proof attachment", "txid", txidHex, "error", err)
		return false
	}

	tx.MerklePath = merklePath
	beefObj := assembleBEEF(tx)
	if beefObj != nil {
		a.beefStorage.SaveBeef(ctx, txid, beefObj)
	}

	// Re-ingest to update all scores
	if _, err := a.indexer.IngestTxid(ctx, txidHex); err != nil {
		a.logger.Error("failed to re-ingest after proof application", "txid", txidHex, "error", err)
		return false
	}

	a.logger.Info("proof applied from Arcade", "txid", txidHex, "height", merklePath.BlockHeight)
	return true
}

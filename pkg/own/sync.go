package own

import (
	"context"
	"log/slog"
	"sync"

	"github.com/b-open-io/go-junglebus"
	"github.com/b-open-io/1sat-stack/pkg/indexer"
	"github.com/b-open-io/1sat-stack/pkg/txo"
)

const (
	// OwnerSyncKey is the key used to track owner sync progress
	OwnerSyncKey = "own:sync"
)

// SyncOwner syncs all transactions for an owner from JungleBus.
// It fetches transactions starting from the last synced height and ingests them.
func SyncOwner(ctx context.Context, owner string, ingestCtx *indexer.IngestCtx, jb *junglebus.Client, outputStore *txo.OutputStore, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}

	// Get last synced height
	lastHeight, err := outputStore.LogScore(ctx, OwnerSyncKey, []byte(owner))
	if err != nil {
		return err
	}

	// Fetch transactions from JungleBus
	addTxns, err := jb.GetAddressTransactions(ctx, owner, uint32(lastHeight))
	if err != nil {
		return err
	}

	if len(addTxns) == 0 {
		return nil
	}

	limiter := make(chan struct{}, ingestCtx.Concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	var processed, skipped int

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	for _, addTxn := range addTxns {
		// Stop if context cancelled
		if ctx.Err() != nil {
			break
		}

		// Skip if already before last synced height (use < not <= to allow same-block txs)
		if float64(addTxn.BlockHeight) < lastHeight {
			skipped++
			continue
		}

		// Skip if already processed
		score, err := outputStore.LogScore(ctx, indexer.IngestTag, []byte(addTxn.TransactionID))
		if err != nil {
			return err
		}
		if score > 0 {
			skipped++
			continue
		}

		wg.Add(1)
		limiter <- struct{}{}

		go func(txid string, blockHeight uint32) {
			defer func() {
				<-limiter
				wg.Done()
			}()

			if _, err := ingestCtx.IngestTxid(ctx, txid); err != nil {
				logger.Error("SyncOwner: error ingesting txid", "txid", txid, "error", err)
				mu.Lock()
				if firstErr == nil {
					firstErr = err
					cancel()
				}
				mu.Unlock()
			} else {
				mu.Lock()
				processed++
				mu.Unlock()
			}

			if float64(blockHeight) > lastHeight {
				mu.Lock()
				if float64(blockHeight) > lastHeight {
					lastHeight = float64(blockHeight)
				}
				mu.Unlock()
			}
		}(addTxn.TransactionID, addTxn.BlockHeight)
	}

	wg.Wait()

	if firstErr != nil {
		return firstErr
	}

	// Update sync progress
	if err := outputStore.Log(ctx, OwnerSyncKey, []byte(owner), lastHeight); err != nil {
		return err
	}

	logger.Debug("SyncOwner complete", "owner", owner, "processed", processed, "skipped", skipped)
	return nil
}

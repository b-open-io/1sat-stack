package owner

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"sync"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/overlay"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/b-open-io/go-junglebus"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	sdkoverlay "github.com/bsv-blockchain/go-sdk/overlay"
)

// OwnerSync handles syncing transactions for owners from JungleBus
type OwnerSync struct {
	jb          *junglebus.Client
	beefStorage *beef.Storage
	overlay     *overlay.Services
	outputStore *txo.OutputStore
	concurrency int
	logger      *slog.Logger
}

// NewOwnerSync creates a new OwnerSync instance
func NewOwnerSync(
	jb *junglebus.Client,
	beefStorage *beef.Storage,
	overlay *overlay.Services,
	outputStore *txo.OutputStore,
	logger *slog.Logger,
) *OwnerSync {
	if logger == nil {
		logger = slog.Default()
	}
	return &OwnerSync{
		jb:          jb,
		beefStorage: beefStorage,
		overlay:     overlay,
		outputStore: outputStore,
		concurrency: 8,
		logger:      logger,
	}
}

// WithConcurrency sets the concurrency level for syncing
func (s *OwnerSync) WithConcurrency(n int) *OwnerSync {
	s.concurrency = n
	return s
}

// Sync syncs all transactions for an owner from JungleBus.
// It fetches transactions starting from the last synced height and submits them to the overlay.
func (s *OwnerSync) Sync(ctx context.Context, owner string) error {
	s.logger.Debug("OwnerSync starting", "owner", owner)

	// Get last synced height (0 if not found)
	var lastHeight float64
	if progressBytes, err := s.outputStore.Store.HGet(ctx, txo.KeyProgress, []byte(owner)); err == nil && len(progressBytes) == 4 {
		lastHeight = float64(binary.BigEndian.Uint32(progressBytes))
	} else if err != nil && err != store.ErrKeyNotFound {
		s.logger.Error("OwnerSync: failed to get last height", "owner", owner, "error", err)
		return err
	}

	s.logger.Debug("OwnerSync: fetching from JungleBus", "owner", owner, "fromHeight", lastHeight)

	// Fetch transactions from JungleBus
	addTxns, err := s.jb.GetAddressTransactions(ctx, owner, uint32(lastHeight))
	if err != nil {
		s.logger.Error("OwnerSync: JungleBus fetch failed", "owner", owner, "error", err)
		return err
	}

	s.logger.Debug("OwnerSync: fetched transactions", "owner", owner, "count", len(addTxns))

	if len(addTxns) == 0 {
		s.logger.Debug("OwnerSync: no new transactions", "owner", owner)
		return nil
	}

	limiter := make(chan struct{}, s.concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	var processed, skipped int
	var newMaxHeight float64 = lastHeight

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	for _, addTxn := range addTxns {
		// Stop if context cancelled
		if ctx.Err() != nil {
			break
		}

		// Skip if already before last synced height
		if float64(addTxn.BlockHeight) < lastHeight {
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

			if err := s.submitToOverlay(ctx, txid); err != nil {
				s.logger.Error("OwnerSync: error submitting txid", "txid", txid, "error", err)
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
			}
		}(addTxn.TransactionID, addTxn.BlockHeight)
	}

	wg.Wait()

	if firstErr != nil {
		return firstErr
	}

	// Update sync progress
	progressBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(progressBytes, uint32(newMaxHeight))
	if err := s.outputStore.Store.HSet(ctx, txo.KeyProgress, []byte(owner), progressBytes); err != nil {
		return err
	}

	s.logger.Debug("OwnerSync complete", "owner", owner, "processed", processed, "skipped", skipped)
	return nil
}

// submitToOverlay builds BEEF with inputs and submits to overlay
func (s *OwnerSync) submitToOverlay(ctx context.Context, txidStr string) error {
	if s.overlay == nil {
		return fmt.Errorf("overlay service not configured")
	}

	txid, err := chainhash.NewHashFromHex(txidStr)
	if err != nil {
		return fmt.Errorf("invalid txid %s: %w", txidStr, err)
	}

	// Load transaction with all inputs populated (needed for overlay validation)
	beefBytes, err := s.beefStorage.BuildFullBeef(ctx, txid)
	if err != nil {
		return fmt.Errorf("failed to build beef: %w", err)
	}

	// Submit to overlay with tm_1sat topic
	steak, err := s.overlay.Submit(ctx, sdkoverlay.TaggedBEEF{
		Beef:   beefBytes,
		Topics: []string{"tm_1sat"},
	}, engine.SubmitModeHistorical)
	if err != nil {
		return fmt.Errorf("failed to submit to overlay: %w", err)
	}

	// Log what was admitted
	if steak != nil {
		for topic, admit := range steak {
			s.logger.Debug("overlay submit result",
				"txid", txidStr,
				"topic", topic,
				"outputsAdmitted", len(admit.OutputsToAdmit),
				"coinsRetained", len(admit.CoinsToRetain))
		}
	}

	return nil
}

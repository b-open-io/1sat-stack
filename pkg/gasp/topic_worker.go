// Package gasp provides GASP (Graph Aware Sync Protocol) implementations for topic-based sync.
package gasp

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/gasp"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// TopicWorkerConfig holds configuration for a TopicWorker.
type TopicWorkerConfig struct {
	TopicName   string           // Topic name (e.g., "tm_abc123...")
	Store       store.Store      // Store for queue operations
	Engine      *engine.Engine   // Overlay engine for submitting transactions
	Remotes     []gasp.Remote    // Remote chain (fallback order)
	Concurrency int              // Max concurrent processing (default: 8)
	PageSize    uint32           // Items to fetch per batch (default: 100)
	PollDelay   time.Duration    // Delay when queue is empty (default: 1s)
	StatusDelay time.Duration    // Status log interval (default: 15s)
	OnProcessed func(string) error // Called after each successful item (optional)
	Logger      *slog.Logger     // Logger (optional)
}

// TopicWorker processes items from a topic queue using GASP for dependency resolution.
// Unlike the generic Worker, it manages batch-scoped seenNodes for memory efficiency
// and supports a remote chain for fetching transaction data.
type TopicWorker struct {
	config      *TopicWorkerConfig
	queueKey    []byte
	logger      *slog.Logger
	limiter     chan struct{}

	// Runtime state
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewTopicWorker creates a new TopicWorker.
func NewTopicWorker(cfg *TopicWorkerConfig) *TopicWorker {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 8
	}
	if cfg.PageSize == 0 {
		cfg.PageSize = 100
	}
	if cfg.PollDelay == 0 {
		cfg.PollDelay = time.Second
	}
	if cfg.StatusDelay == 0 {
		cfg.StatusDelay = 15 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	logger := cfg.Logger.With("component", "topic-worker", "topic", cfg.TopicName)

	return &TopicWorker{
		config:   cfg,
		queueKey: []byte("q:" + cfg.TopicName),
		logger:   logger,
		limiter:  make(chan struct{}, cfg.Concurrency),
	}
}

// Start begins processing the topic queue. Blocks until context is cancelled.
func (w *TopicWorker) Start(ctx context.Context) error {
	ctx, w.cancel = context.WithCancel(ctx)

	w.logger.Info("topic worker starting",
		"queue", string(w.queueKey),
		"concurrency", w.config.Concurrency,
		"remotes", len(w.config.Remotes),
	)

	ticker := time.NewTicker(w.config.StatusDelay)
	defer ticker.Stop()

	processedCount := 0
	errorCount := 0
	statusTime := time.Now()
	var lastScore float64

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("topic worker shutting down")
			w.wg.Wait()
			return ctx.Err()

		case <-ticker.C:
			if processedCount > 0 || errorCount > 0 {
				duration := time.Since(statusTime)
				rate := float64(processedCount) / duration.Seconds()
				w.logger.Info("topic worker status",
					"processed", processedCount,
					"errors", errorCount,
					"rate", rate,
					"lastScore", lastScore,
				)
			}
			processedCount = 0
			errorCount = 0
			statusTime = time.Now()

		default:
			// Process a batch
			processed, errors, score := w.processBatch(ctx)
			processedCount += processed
			errorCount += errors
			if score > lastScore {
				lastScore = score
			}

			// If no items, wait before polling again
			if processed == 0 && errors == 0 {
				time.Sleep(w.config.PollDelay)
			}
		}
	}
}

// processBatch fetches and processes a batch of items from the queue.
// Returns the count of successfully processed items, error count, and max score.
func (w *TopicWorker) processBatch(ctx context.Context) (processed int, errors int, maxScore float64) {
	// Fetch items up to current time
	to := types.HeightScore(0, 0)
	items, err := w.config.Store.Search(ctx, &store.SearchCfg{
		Keys:  [][]byte{w.queueKey},
		Limit: w.config.PageSize,
		To:    &to,
	})
	if err != nil {
		w.logger.Error("failed to fetch from queue", "error", err)
		return 0, 1, 0
	}

	if len(items) == 0 {
		return 0, 0, 0
	}

	// Create fresh seenNodes for this batch (memory management)
	seenNodes := &sync.Map{}

	// Create GASP storage for this topic
	gaspStorage := engine.NewOverlayGASPStorage(w.config.TopicName, w.config.Engine, nil)

	// Track inflight items for this batch
	inflight := make(map[string]struct{})
	var inflightMu sync.Mutex

	done := make(chan result, len(items))

	for _, item := range items {
		id := string(item.Member)
		score := item.Score

		inflightMu.Lock()
		if _, ok := inflight[id]; ok {
			inflightMu.Unlock()
			continue
		}
		inflight[id] = struct{}{}
		inflightMu.Unlock()

		if score > maxScore {
			maxScore = score
		}

		w.limiter <- struct{}{}
		w.wg.Add(1)

		go func(id string, score float64) {
			defer func() {
				<-w.limiter
				w.wg.Done()
			}()

			// Parse outpoint from queue member
			outpoint := transaction.NewOutpointFromBytes([]byte(id))
			if outpoint == nil {
				w.logger.Error("invalid outpoint in queue", "id", id)
				done <- result{id: id, err: fmt.Errorf("invalid outpoint: %s", id)}
				return
			}

			// Process using GASP with remote chain
			if err := w.processWithRemotes(ctx, gaspStorage, seenNodes, outpoint); err != nil {
				w.logger.Debug("failed to process output", "outpoint", id, "error", err)
				done <- result{id: id, err: err}
				return
			}

			// Success - remove from queue
			if err := w.config.Store.ZRem(ctx, w.queueKey, []byte(id)); err != nil {
				w.logger.Error("failed to remove from queue", "id", id, "error", err)
			}

			// Call OnProcessed callback if configured
			if w.config.OnProcessed != nil {
				if err := w.config.OnProcessed(w.config.TopicName); err != nil {
					w.logger.Debug("OnProcessed callback error", "error", err)
				}
			}

			done <- result{id: id, err: nil}
		}(id, score)
	}

	// Wait for all items in this batch
	for range items {
		r := <-done
		if r.err != nil {
			errors++
		} else {
			processed++
		}
	}

	return processed, errors, maxScore
}

// processWithRemotes tries each remote in the chain until one succeeds.
func (w *TopicWorker) processWithRemotes(ctx context.Context, storage gasp.Storage, seenNodes *sync.Map, outpoint *transaction.Outpoint) error {
	var lastErr error

	for i, remote := range w.config.Remotes {
		// Create GASP instance for this remote
		logPrefix := fmt.Sprintf("[GASP %s remote-%d] ", w.config.TopicName, i)
		g := gasp.NewGASP(gasp.Params{
			Storage:        storage,
			Remote:         remote,
			Unidirectional: true,
			Topic:          w.config.TopicName,
			Concurrency:    w.config.Concurrency,
			LogPrefix:      &logPrefix,
		})

		// Try to process with this remote
		err := g.ProcessUTXOToCompletion(ctx, outpoint, nil, seenNodes)
		if err == nil {
			return nil // Success
		}

		w.logger.Debug("remote failed, trying next",
			"remote", i,
			"outpoint", outpoint.String(),
			"error", err,
		)
		lastErr = err
	}

	// All remotes failed
	if lastErr != nil {
		return fmt.Errorf("all remotes failed: %w", lastErr)
	}
	return fmt.Errorf("no remotes configured")
}

// Stop stops the worker gracefully.
func (w *TopicWorker) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
	w.wg.Wait()
}

// result holds the outcome of processing a single item.
type result struct {
	id  string
	err error
}

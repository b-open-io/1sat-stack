package worker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/types"
)

// Handler processes a single work item. Returns error if processing fails.
type Handler func(ctx context.Context, id string, score float64) error

// ErrorHandler is called when processing fails.
type ErrorHandler func(ctx context.Context, id string, score float64, err error)

// Worker processes items from a sorted set queue with configurable concurrency.
type Worker struct {
	store       store.Store
	key         string
	concurrency int
	handler     Handler
	onError     ErrorHandler
	logger      *slog.Logger
	pageSize    uint32
	pollDelay   time.Duration
	statusDelay time.Duration

	// Runtime state
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// Config holds worker configuration.
type Config struct {
	Store       store.Store
	Key         string        // Sorted set key to consume from
	Concurrency int           // Number of concurrent workers
	Handler     Handler       // Called for each item
	OnError     ErrorHandler  // Called on handler error (optional)
	Logger      *slog.Logger  // Logger (optional)
	PageSize    uint32        // Items to fetch per batch (default: 100)
	PollDelay   time.Duration // Delay when queue is empty (default: 1s)
	StatusDelay time.Duration // Status log interval (default: 15s)
}

// New creates a new Worker.
func New(cfg *Config) *Worker {
	if cfg.Concurrency < 1 {
		cfg.Concurrency = 1
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

	// Add component tag with queue key for identification
	logger := cfg.Logger.With("component", "worker", "queue", cfg.Key)

	return &Worker{
		store:       cfg.Store,
		key:         cfg.Key,
		concurrency: cfg.Concurrency,
		handler:     cfg.Handler,
		onError:     cfg.OnError,
		logger:      logger,
		pageSize:    cfg.PageSize,
		pollDelay:   cfg.PollDelay,
		statusDelay: cfg.StatusDelay,
	}
}

// Start begins processing the queue. Blocks until context is cancelled.
func (w *Worker) Start(ctx context.Context) error {
	ctx, w.cancel = context.WithCancel(ctx)

	limiter := make(chan struct{}, w.concurrency)
	inflight := make(map[string]struct{})
	var inflightMu sync.Mutex

	done := make(chan string, w.concurrency)
	errChan := make(chan workerError, w.concurrency)

	ticker := time.NewTicker(w.statusDelay)
	defer ticker.Stop()

	processedCount := 0
	statusTime := time.Now()
	var lastScore float64

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("worker shutting down", "key", w.key)
			w.wg.Wait()
			return ctx.Err()

		case <-ticker.C:
			// Only log status if items were processed
			if processedCount > 0 {
				duration := time.Since(statusTime)
				rate := float64(processedCount) / duration.Seconds()
				w.logger.Info("worker status",
					"key", w.key,
					"processed", processedCount,
					"rate", rate,
					"lastScore", lastScore,
				)
			}
			processedCount = 0
			statusTime = time.Now()

		case id := <-done:
			inflightMu.Lock()
			delete(inflight, id)
			inflightMu.Unlock()
			processedCount++

		case we := <-errChan:
			if w.onError != nil {
				w.onError(ctx, we.id, we.score, we.err)
			}
			w.logger.Error("worker error",
				"key", w.key,
				"id", we.id,
				"error", we.err,
			)

		default:
			// Fetch items up to current time using HeightScore scale
			to := types.HeightScore(0, 0)
			items, err := w.store.Search(ctx, &store.SearchCfg{
				Keys:  [][]byte{[]byte(w.key)},
				Limit: w.pageSize,
				To:    &to,
			})
			if err != nil {
				w.logger.Error("search error", "key", w.key, "error", err)
				time.Sleep(w.pollDelay)
				continue
			}

			if len(items) == 0 {
				time.Sleep(w.pollDelay)
				continue
			}

			for _, item := range items {
				id := string(item.Member)
				lastScore = item.Score

				inflightMu.Lock()
				if _, ok := inflight[id]; ok {
					inflightMu.Unlock()
					continue
				}
				inflight[id] = struct{}{}
				inflightMu.Unlock()

				limiter <- struct{}{}
				w.wg.Add(1)

				go func(id string, score float64) {
					defer func() {
						if r := recover(); r != nil {
							w.logger.Error("worker panic",
								"key", w.key,
								"id", id,
								"panic", r,
							)
						}
						<-limiter
						w.wg.Done()
						done <- id
					}()

					if err := w.handler(ctx, id, score); err != nil {
						errChan <- workerError{id: id, score: score, err: err}
						return
					}

					// Remove successfully processed item from queue
					if err := w.store.ZRem(ctx, []byte(w.key), []byte(id)); err != nil {
						w.logger.Error("failed to remove from queue", "key", w.key, "id", id, "error", err)
					}
				}(id, item.Score)
			}
		}
	}
}

// Stop stops the worker gracefully.
func (w *Worker) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
	w.wg.Wait()
}

type workerError struct {
	id    string
	score float64
	err   error
}

// ProcessOnce processes all items once and returns.
// Useful for one-time catchup or batch processing.
func (w *Worker) ProcessOnce(ctx context.Context) error {
	limiter := make(chan struct{}, w.concurrency)
	var wg sync.WaitGroup

	for {
		// Fetch items up to current time using HeightScore scale
		to := types.HeightScore(0, 0)
		items, err := w.store.Search(ctx, &store.SearchCfg{
			Keys:  [][]byte{[]byte(w.key)},
			Limit: w.pageSize,
			To:    &to,
		})
		if err != nil {
			return err
		}

		if len(items) == 0 {
			break
		}

		for _, item := range items {
			id := string(item.Member)
			score := item.Score

			limiter <- struct{}{}
			wg.Add(1)

			go func(id string, score float64) {
				defer func() {
					<-limiter
					wg.Done()
				}()

				if err := w.handler(ctx, id, score); err != nil {
					if w.onError != nil {
						w.onError(ctx, id, score, err)
					}
				}
			}(id, score)
		}
	}

	wg.Wait()
	return nil
}

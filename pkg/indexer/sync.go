package indexer

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/jbsync"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/worker"
	"github.com/bsv-blockchain/go-sdk/chainhash"
)

// IngestSync processes transactions from a queue using the indexer.
type IngestSync struct {
	config  *SyncConfig
	store   store.Store
	indexer *IngestCtx
	logger  *slog.Logger
	worker  *worker.Worker
}

// NewIngestSync creates a new IngestSync instance.
func NewIngestSync(
	cfg *SyncConfig,
	s store.Store,
	idx *IngestCtx,
	logger *slog.Logger,
) *IngestSync {
	if logger == nil {
		logger = slog.Default()
	}

	return &IngestSync{
		config:  cfg,
		store:   s,
		indexer: idx,
		logger:  logger.With("component", "ingest-sync"),
	}
}

// Start begins processing the ingest queue. Blocks until context is cancelled.
func (s *IngestSync) Start(ctx context.Context) error {
	s.worker = worker.New(&worker.Config{
		Store:       s.store,
		Key:         jbsync.QueueKey(s.config.QueueName),
		Concurrency: s.config.Concurrency,
		Handler:     s.ingest,
		OnError: func(ctx context.Context, id string, score float64, err error) {
			s.logger.Error("ingest error", "txid", id, "score", score, "error", err)
		},
		PageSize:    s.config.PageSize,
		PollDelay:   s.config.PollDelay,
		StatusDelay: s.config.StatusDelay,
		Logger:      s.logger,
	})

	s.logger.Info("starting ingest sync",
		"queue", s.config.QueueName,
		"concurrency", s.config.Concurrency,
	)

	return s.worker.Start(ctx)
}

// Stop stops the ingest sync worker gracefully.
func (s *IngestSync) Stop() {
	if s.worker != nil {
		s.worker.Stop()
	}
}

// ingest processes a single transaction from the queue.
func (s *IngestSync) ingest(ctx context.Context, member string, score float64) error {
	// Member is binary 32-byte txid
	if len(member) != 32 {
		return fmt.Errorf("invalid txid length: expected 32, got %d", len(member))
	}

	txid := &chainhash.Hash{}
	copy(txid[:], []byte(member))

	_, err := s.indexer.IngestTxid(ctx, txid.String())
	return err
}

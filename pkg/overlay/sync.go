package overlay

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/jbsync"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/worker"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	sdkoverlay "github.com/bsv-blockchain/go-sdk/overlay"
)

// ErrorAction determines how the overlay sync worker handles an error from Submit().
type ErrorAction int

const (
	ErrorRetry ErrorAction = iota // Keep in queue, retry later (default)
	ErrorSkip                     // Remove from queue, log warning
)

// ErrorClassifier classifies errors from Submit() to determine worker behavior.
// Set programmatically by overlay modules that need custom error handling.
type ErrorClassifier func(err error) ErrorAction

// OverlaySyncConfig configures the overlay sync worker and optional JungleBus subscriber for a single topic.
type OverlaySyncConfig struct {
	Enabled         bool            `mapstructure:"enabled"`
	SubscriptionID  string          `mapstructure:"subscription_id"` // JungleBus subscription ID (set via env var)
	QueueName       string          `mapstructure:"queue_name"`      // Queue to consume from (e.g., "bap" → q:bap)
	FromBlock       uint64          `mapstructure:"from_block"`      // Starting block for JungleBus subscription
	Concurrency     int             `mapstructure:"concurrency"`     // Worker concurrency (default: 8)
	PageSize        uint32          `mapstructure:"page_size"`       // Batch fetch size (default: 100)
	PollDelay       time.Duration   `mapstructure:"poll_delay"`      // Sleep when queue empty (default: 1s)
	BatchSize       int             `mapstructure:"batch_size"`      // JungleBus batch size (default: 1000)
	ReorgDepth      uint32          `mapstructure:"reorg_depth"`     // JungleBus reorg depth (default: 6)
	EnableMempool   bool            `mapstructure:"enable_mempool"`  // JungleBus mempool subscription
	ErrorClassifier ErrorClassifier `mapstructure:"-"`               // Classifies Submit errors (set programmatically)
}

// SubscriberConfig creates a jbsync.SubscriberConfig from this overlay sync config.
func (c *OverlaySyncConfig) SubscriberConfig() *jbsync.SubscriberConfig {
	return &jbsync.SubscriberConfig{
		AutoStart:      true,
		SubscriptionID: c.SubscriptionID,
		QueueName:      c.QueueName,
		FromBlock:      c.FromBlock,
		BatchSize:      c.BatchSize,
		ReorgDepth:     c.ReorgDepth,
		EnableMempool:  c.EnableMempool,
	}
}

// OverlaySync processes transactions from a queue and submits them through the overlay engine.
// This is a generic worker shared by BAP, BSocial, OPNS, and any future overlay topics.
type OverlaySync struct {
	config      *OverlaySyncConfig
	topicName   string
	store       store.Store
	beefStorage *beef.Storage
	overlay     *Services
	logger      *slog.Logger
	worker      *worker.Worker
}

// NewOverlaySync creates a new overlay sync worker.
func NewOverlaySync(
	cfg *OverlaySyncConfig,
	topicName string,
	s store.Store,
	beefStorage *beef.Storage,
	overlaySvc *Services,
	logger *slog.Logger,
) *OverlaySync {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.Concurrency == 0 {
		cfg.Concurrency = 8
	}
	if cfg.PageSize == 0 {
		cfg.PageSize = 100
	}
	if cfg.PollDelay == 0 {
		cfg.PollDelay = time.Second
	}

	return &OverlaySync{
		config:      cfg,
		topicName:   topicName,
		store:       s,
		beefStorage: beefStorage,
		overlay:     overlaySvc,
		logger:      logger.With("component", "overlay-sync", "topic", topicName),
	}
}

// Start begins processing the queue. Blocks until context is cancelled.
func (s *OverlaySync) Start(ctx context.Context) error {
	limiter := make(chan struct{}, s.config.Concurrency)
	s.worker = worker.New(&worker.Config{
		Store:   s.store,
		Key:     jbsync.QueueKey(s.config.QueueName),
		Limiter: limiter,
		Handler: s.process,
		OnError: func(ctx context.Context, id string, score float64, err error) {
			s.logger.Error("overlay sync error", "txid", id, "score", score, "error", err)
		},
		PageSize:  s.config.PageSize,
		PollDelay: s.config.PollDelay,
		Logger:    s.logger,
	})

	s.logger.Info("starting overlay sync",
		"queue", s.config.QueueName,
		"topic", s.topicName,
		"concurrency", s.config.Concurrency,
	)

	return s.worker.Start(ctx)
}

// Stop stops the sync worker gracefully.
func (s *OverlaySync) Stop() {
	if s.worker != nil {
		s.worker.Stop()
	}
}

// process handles a single transaction from the queue.
func (s *OverlaySync) process(ctx context.Context, member string, score float64) error {
	if len(member) != 32 {
		return fmt.Errorf("invalid txid length: expected 32, got %d", len(member))
	}

	txid := &chainhash.Hash{}
	copy(txid[:], []byte(member))

	beefBytes, err := s.beefStorage.BuildFullBeef(ctx, txid)
	if err != nil {
		return fmt.Errorf("failed to build BEEF for %s: %w", txid.String(), err)
	}

	if _, err := s.overlay.Submit(ctx, sdkoverlay.TaggedBEEF{
		Beef:   beefBytes,
		Topics: []string{s.topicName},
	}, engine.SubmitModeHistorical); err != nil {
		var missingErr *MissingInputError
		if errors.As(err, &missingErr) {
			s.logger.Info("skipping transaction with missing input",
				"txid", missingErr.TransactionID.String(),
				"missing_txid", missingErr.MissingTxID.String(),
				"input_index", missingErr.InputIndex,
				"output_index", missingErr.OutputIndex,
				"topic", missingErr.Topic)
			return nil
		}
		if s.config.ErrorClassifier != nil && s.config.ErrorClassifier(err) == ErrorSkip {
			s.logger.Warn("skipping transaction due to classified error",
				"txid", txid.String(), "error", err)
			return nil
		}
		return fmt.Errorf("failed to submit %s to %s: %w", txid.String(), s.topicName, err)
	}

	return nil
}

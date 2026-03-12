package overlay

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	gaspqueue "github.com/b-open-io/1sat-stack/pkg/gasp"
	"github.com/b-open-io/1sat-stack/pkg/jbsync"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/worker"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/gasp"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	sdkoverlay "github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/transaction"
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
	Enabled               bool            `mapstructure:"enabled"`
	SubscriptionID        string          `mapstructure:"subscription_id"`    // JungleBus subscription ID (set via env var)
	QueueName             string          `mapstructure:"queue_name"`         // Queue to consume from (e.g., "bap" → q:bap)
	FromBlock             uint64          `mapstructure:"from_block"`         // Starting block for JungleBus subscription
	Concurrency           int             `mapstructure:"concurrency"`        // Worker concurrency (default: 8)
	PageSize              uint32          `mapstructure:"page_size"`          // Batch fetch size (default: 100)
	PollDelay             time.Duration   `mapstructure:"poll_delay"`         // Sleep when queue empty (default: 1s)
	BatchSize             int             `mapstructure:"batch_size"`         // JungleBus batch size (default: 1000)
	ReorgDepth            uint32          `mapstructure:"reorg_depth"`        // JungleBus reorg depth (default: 6)
	EnableMempool         bool            `mapstructure:"enable_mempool"`     // JungleBus mempool subscription
	ResolveDependencies   bool            `mapstructure:"resolve_dependencies"` // Use GASP to resolve input dependencies before submit
	ErrorClassifier       ErrorClassifier `mapstructure:"-"`                  // Classifies Submit errors (set programmatically)
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

	if s.config.ResolveDependencies {
		return s.processWithGASP(ctx, txid)
	}
	return s.processDirect(ctx, txid)
}

// processDirect submits a transaction directly without dependency resolution.
// Used by overlays with independent outputs (BAP, BSocial).
func (s *OverlaySync) processDirect(ctx context.Context, txid *chainhash.Hash) error {
	beefBytes, err := s.beefStorage.BuildFullBeef(ctx, txid)
	if err != nil {
		return fmt.Errorf("failed to build BEEF for %s: %w", txid.String(), err)
	}

	if _, err := s.overlay.Submit(ctx, sdkoverlay.TaggedBEEF{
		Beef:   beefBytes,
		Topics: []string{s.topicName},
	}, engine.SubmitModeHistorical); err != nil {
		if s.config.ErrorClassifier != nil && s.config.ErrorClassifier(err) == ErrorSkip {
			s.logger.Warn("skipping transaction due to classified error",
				"txid", txid.String(), "error", err)
			return nil
		}
		return fmt.Errorf("failed to submit %s to %s: %w", txid.String(), s.topicName, err)
	}

	return nil
}

// processWithGASP uses GASP to resolve input dependencies before submitting.
// Loads the transaction, enumerates outputs, and runs ProcessUTXOToCompletion
// for each output. GASP walks the input graph and submits ancestors first.
// If a dependency can't be resolved, MissingInputError is treated as complete
// (we've done everything we can for this item).
func (s *OverlaySync) processWithGASP(ctx context.Context, txid *chainhash.Hash) error {
	tx, err := s.beefStorage.LoadTx(ctx, txid)
	if err != nil {
		return fmt.Errorf("failed to load tx %s: %w", txid.String(), err)
	}

	beefRemote := gaspqueue.NewBeefRemote(s.beefStorage, s.store, "")
	gaspStorage := engine.NewOverlayGASPStorage(s.topicName, s.overlay.Engine, nil)
	seenNodes := &sync.Map{}

	logPrefix := fmt.Sprintf("[GASP %s] ", s.topicName)
	g := gasp.NewGASP(gasp.Params{
		Storage:        gaspStorage,
		Remote:         beefRemote,
		Unidirectional: true,
		Topic:          s.topicName,
		Concurrency:    s.config.Concurrency,
		LogPrefix:      &logPrefix,
	})

	for vout := range tx.Outputs {
		outpoint := &transaction.Outpoint{
			Txid:  *txid,
			Index: uint32(vout),
		}

		if err := g.ProcessUTXOToCompletion(ctx, outpoint, nil, seenNodes); err != nil {
			var missingErr *MissingInputError
			if errors.As(err, &missingErr) {
				s.logger.Info("dependency unresolvable after GASP",
					"txid", missingErr.TransactionID.String(),
					"missing_txid", missingErr.MissingTxID.String(),
					"input_index", missingErr.InputIndex,
					"topic", missingErr.Topic)
				continue
			}
			if errors.Is(err, gasp.ErrGraphNoTopicalAdmittance) {
				continue
			}
			s.logger.Info("GASP processing failed", "txid", txid.String(), "vout", vout, "error", err)
		}
	}

	return nil
}

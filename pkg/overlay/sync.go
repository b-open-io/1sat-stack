package overlay

import (
	"context"
	"encoding/binary"
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
	Enabled             bool            `mapstructure:"enabled"`
	SubscriptionID      string          `mapstructure:"subscription_id"`      // JungleBus subscription ID (set via env var)
	QueueName           string          `mapstructure:"queue_name"`           // Queue to consume from (e.g., "bap" → q:bap)
	FromBlock           uint64          `mapstructure:"from_block"`           // Starting block for JungleBus subscription
	Concurrency         int             `mapstructure:"concurrency"`          // Worker concurrency (default: 8)
	PageSize            uint32          `mapstructure:"page_size"`            // Batch fetch size (default: 100)
	PollDelay           time.Duration   `mapstructure:"poll_delay"`           // Sleep when queue empty (default: 1s)
	BatchSize           int             `mapstructure:"batch_size"`           // JungleBus batch size (default: 1000)
	ReorgDepth          uint32          `mapstructure:"reorg_depth"`          // JungleBus reorg depth (default: 6)
	EnableMempool       bool            `mapstructure:"enable_mempool"`       // JungleBus mempool subscription
	ResolveDependencies bool            `mapstructure:"resolve_dependencies"` // Use GASP to resolve input dependencies before submit
	ErrorClassifier     ErrorClassifier `mapstructure:"-"`                    // Classifies Submit errors (set programmatically)
	OnProcessed         func(string) error `mapstructure:"-"`                 // Called after each successful item with topic name (set programmatically)
	Limiter             chan struct{}  `mapstructure:"-"`                    // External shared limiter (optional, set programmatically)
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
//
// Queue members can be either:
//   - 36 bytes: outpoint (txid + vout) — targets a specific output for GASP processing
//   - 32 bytes: txid — submits the entire transaction (legacy/spend events)
type OverlaySync struct {
	config      *OverlaySyncConfig
	topicName   string
	store       store.Store
	beefStorage *beef.Storage
	engine      *engine.Engine
	logger      *slog.Logger
	worker      *worker.Worker
	directSeen  sync.Map // txid dedup for processDirect within a batch
}

// NewOverlaySync creates a new overlay sync worker.
func NewOverlaySync(
	cfg *OverlaySyncConfig,
	topicName string,
	s store.Store,
	beefStorage *beef.Storage,
	eng *engine.Engine,
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
		engine:      eng,
		logger:      logger.With("component", "overlay-sync", "topic", topicName),
	}
}

// Start begins processing the queue. Blocks until context is cancelled.
func (s *OverlaySync) Start(ctx context.Context) error {
	limiter := s.config.Limiter
	if limiter == nil {
		limiter = make(chan struct{}, s.config.Concurrency)
	}
	handler := s.process
	if s.config.OnProcessed != nil {
		inner := handler
		handler = func(ctx context.Context, member string, score float64) error {
			if err := inner(ctx, member, score); err != nil {
				return err
			}
			return s.config.OnProcessed(s.topicName)
		}
	}

	s.worker = worker.New(&worker.Config{
		Store:   s.store,
		Key:     jbsync.QueueKey(s.config.QueueName),
		Limiter: limiter,
		Handler: handler,
		OnError: func(ctx context.Context, id string, score float64, err error) {
			s.logger.Error("overlay sync error", "member", id, "score", score, "error", err)
		},
		PageSize:  s.config.PageSize,
		PollDelay: s.config.PollDelay,
		Logger:    s.logger,
		OnBatchStart: func() {
			s.directSeen = sync.Map{}
		},
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

// parseQueueMember extracts txid and optional vout from a queue member.
// 36 bytes = outpoint (txid + vout), 32 bytes = txid only (vout = -1).
func parseQueueMember(member string) (txid *chainhash.Hash, vout int, err error) {
	b := []byte(member)
	switch len(b) {
	case 36:
		txid = &chainhash.Hash{}
		copy(txid[:], b[:32])
		vout = int(binary.LittleEndian.Uint32(b[32:]))
		return
	case 32:
		txid = &chainhash.Hash{}
		copy(txid[:], b)
		vout = -1
		return
	default:
		return nil, -1, fmt.Errorf("invalid member length: expected 32 or 36, got %d", len(b))
	}
}

// process handles a single item from the queue.
func (s *OverlaySync) process(ctx context.Context, member string, score float64) error {
	txid, vout, err := parseQueueMember(member)
	if err != nil {
		return err
	}

	if !s.config.ResolveDependencies || vout < 0 {
		return s.processDirect(ctx, txid)
	}
	return s.processWithGASP(ctx, txid, vout)
}

// processDirect submits a transaction directly without dependency resolution.
// Deduplicates by txid within the current batch to avoid redundant submissions
// when multiple outpoints from the same transaction are queued.
func (s *OverlaySync) processDirect(ctx context.Context, txid *chainhash.Hash) error {
	if _, loaded := s.directSeen.LoadOrStore(*txid, struct{}{}); loaded {
		return nil
	}

	beefBytes, err := s.beefStorage.BuildFullBeef(ctx, txid)
	if err != nil {
		return fmt.Errorf("failed to build BEEF for %s: %w", txid.String(), err)
	}

	if _, err := s.engine.Submit(ctx, sdkoverlay.TaggedBEEF{
		Beef:   beefBytes,
		Topics: []string{s.topicName},
	}, engine.SubmitModeHistorical, nil); err != nil {
		if s.config.ErrorClassifier != nil && s.config.ErrorClassifier(err) == ErrorSkip {
			s.logger.Warn("skipping transaction due to classified error",
				"txid", txid.String(), "error", err)
			return nil
		}
		return fmt.Errorf("failed to submit %s to %s: %w", txid.String(), s.topicName, err)
	}

	return nil
}

// processWithGASP uses GASP to resolve input dependencies before submitting
// a specific outpoint.
func (s *OverlaySync) processWithGASP(ctx context.Context, txid *chainhash.Hash, vout int) error {
	beefRemote := gaspqueue.NewBeefRemote(s.beefStorage, s.store, "")
	gaspStorage := engine.NewOverlayGASPStorage(s.topicName, s.engine, nil)
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

	return s.processOutpoint(ctx, g, &transaction.Outpoint{Txid: *txid, Index: uint32(vout)}, seenNodes)
}

func (s *OverlaySync) processOutpoint(ctx context.Context, g *gasp.GASP, outpoint *transaction.Outpoint, seenNodes *sync.Map) error {
	s.logger.Info("GASP processing outpoint", "outpoint", outpoint.String())
	if err := g.ProcessUTXOToCompletion(ctx, outpoint, nil, seenNodes); err != nil {
		var missingErr *MissingInputError
		if errors.As(err, &missingErr) {
			s.logger.Info("GASP missing input",
				"outpoint", outpoint.String(),
				"txid", missingErr.TransactionID.String(),
				"missing_txid", missingErr.MissingTxID.String(),
				"input_index", missingErr.InputIndex,
				"topic", missingErr.Topic)
			return nil
		}
		if errors.Is(err, gasp.ErrGraphNoTopicalAdmittance) {
			s.logger.Info("GASP not admitted", "outpoint", outpoint.String())
			return nil
		}
		s.logger.Info("GASP failed", "outpoint", outpoint.String(), "error", err)
	} else {
		s.logger.Info("GASP success", "outpoint", outpoint.String())
	}
	return nil
}

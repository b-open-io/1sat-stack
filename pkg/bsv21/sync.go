package bsv21

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/jbsync"
	"github.com/b-open-io/1sat-stack/pkg/logging"
	"github.com/b-open-io/1sat-stack/pkg/overlay"
	"github.com/b-open-io/1sat-stack/pkg/owner"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/b-open-io/1sat-stack/pkg/worker"
	"github.com/b-open-io/go-junglebus"
	"github.com/bitcoin-sv/go-templates/template/bsv21"
	"github.com/bsv-blockchain/go-chaintracks/chaintracks"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	sdkoverlay "github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"golang.org/x/sync/errgroup"
)

// SyncConfig holds BSV21 sync configuration
type SyncConfig struct {
	Enabled           bool          `mapstructure:"enabled"`            // Enable sync services
	DispatchWorkers   int           `mapstructure:"dispatch_workers"`   // Concurrency for dispatcher
	TokenWorkers      int           `mapstructure:"token_workers"`      // Concurrency for token processing
	FeePerOutput      int64         `mapstructure:"fee_per_output"`     // Satoshis per admitted output (default: 1000)
	LogLevel          string        `mapstructure:"log_level"`          // Log level for sync (debug, info, warn, error)
	LifecycleInterval time.Duration `mapstructure:"lifecycle_interval"` // Interval for token lifecycle management (default: 5m)
}

// SyncServices manages BSV21 sync pipeline
type SyncServices struct {
	config       *SyncConfig
	store        store.Store
	beefStorage  *beef.Storage
	outputStore  *txo.OutputStore
	overlay      *overlay.Services
	chainTracker chaintracks.Chaintracks
	jbClient     *junglebus.Client
	logger       *slog.Logger

	dispatcher *worker.Worker
	manager    *TokenManager
}

// NewSyncServices creates a new BSV21 sync service
func NewSyncServices(
	cfg *SyncConfig,
	s store.Store,
	beefStorage *beef.Storage,
	outputStore *txo.OutputStore,
	overlaySvc *overlay.Services,
	ct chaintracks.Chaintracks,
	jbClient *junglebus.Client,
	logger *slog.Logger,
) (*SyncServices, error) {
	if overlaySvc == nil {
		return nil, errors.New("overlay service is required for BSV21 sync")
	}
	if logger == nil {
		logger = slog.Default()
	}

	// Set defaults
	if cfg.DispatchWorkers == 0 {
		cfg.DispatchWorkers = 8
	}
	if cfg.TokenWorkers == 0 {
		cfg.TokenWorkers = 16
	}
	if cfg.FeePerOutput == 0 {
		cfg.FeePerOutput = 1000 // 1000 sats per output
	}
	if cfg.LifecycleInterval == 0 {
		cfg.LifecycleInterval = 5 * time.Minute // Check token lifecycle every 5 minutes
	}

	// Create logger with optional level override
	syncLogger := logging.NewComponentLogger(logger, "bsv21-sync", cfg.LogLevel)

	// Create owner sync for fee address syncing
	ownerSync := owner.NewOwnerSync(
		jbClient,
		beefStorage,
		overlaySvc,
		outputStore,
		logger,
	)

	// Create token manager upfront so it's available for status queries
	manager := NewTokenManager(
		s,
		beefStorage,
		outputStore,
		overlaySvc,
		ownerSync,
		ct,
		cfg.TokenWorkers,
		cfg.FeePerOutput,
		cfg.LifecycleInterval,
		syncLogger,
	)

	return &SyncServices{
		config:       cfg,
		store:        s,
		beefStorage:  beefStorage,
		outputStore:  outputStore,
		overlay:      overlaySvc,
		chainTracker: ct,
		jbClient:     jbClient,
		logger:       syncLogger,
		manager:      manager,
	}, nil
}

// GetManager returns the token manager for status queries
func (s *SyncServices) GetManager() *TokenManager {
	return s.manager
}

// Start begins the BSV21 sync pipeline
func (s *SyncServices) Start(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)

	// Start dispatcher - reads from q:bsv21 and routes to per-token queues
	s.dispatcher = worker.New(&worker.Config{
		Store:       s.store,
		Key:         jbsync.QueueKey("bsv21"),
		Concurrency: s.config.DispatchWorkers,
		Handler:     s.dispatch,
		OnError: func(ctx context.Context, id string, score float64, err error) {
			s.logger.Error("dispatcher error", "txid", id, "score", score, "error", err)
		},
		PageSize:  1000,
		PollDelay: time.Second,
		Logger:    s.logger,
	})

	g.Go(func() error {
		return s.dispatcher.Start(ctx)
	})

	// Start token manager (created in NewSyncServices)
	g.Go(func() error {
		return s.manager.Start(ctx)
	})

	return g.Wait()
}

// dispatch processes a transaction from the main queue and routes to token queues
func (s *SyncServices) dispatch(ctx context.Context, member string, score float64) error {
	// Member is binary 32-byte txid
	if len(member) != 32 {
		return fmt.Errorf("invalid txid length: expected 32, got %d", len(member))
	}
	txid := &chainhash.Hash{}
	copy(txid[:], []byte(member))

	tx, err := s.beefStorage.LoadTx(ctx, txid)
	if err != nil {
		return fmt.Errorf("failed to load transaction %s: %w", txid.String(), err)
	}

	var beefBytes []byte // Lazy-loaded only if we find a deploy operation

	for vout, output := range tx.Outputs {
		b := bsv21.Decode(output.LockingScript)
		if b == nil {
			continue
		}

		outpoint := &transaction.Outpoint{
			Txid:  *txid,
			Index: uint32(vout),
		}

		var tokenId string
		switch b.Op {
		case string(bsv21.OpDeployMint), string(bsv21.OpDeployAuth):
			// Deploy operations: tokenId = outpoint, submit for discovery
			tokenId = outpoint.OrdinalString()

			if beefBytes == nil {
				fullTx, err := s.beefStorage.BuildFullBeefTx(ctx, txid)
				if err != nil {
					s.logger.Error("failed to build beef for discovery", "error", err, "txid", txid.String())
					continue
				}
				beefBytes, err = fullTx.AtomicBEEF(false)
				if err != nil {
					s.logger.Error("failed to serialize beef for discovery", "error", err, "txid", txid.String())
					continue
				}
			}

			if _, err := s.overlay.Submit(ctx, sdkoverlay.TaggedBEEF{
				Beef:   beefBytes,
				Topics: []string{"tm_bsv21"},
			}, engine.SubmitModeHistorical); err != nil {
				s.logger.Error("failed to submit to tm_bsv21", "error", err, "txid", txid.String())
			}

		default:
			// Transfer/burn/mint/auth operations: tokenId from the output
			tokenId = b.Id
		}

		if tokenId == "" {
			continue
		}

		// Add outpoint to token queue
		tokenQueueKey := []byte(jbsync.TokenQueueKey(tokenId))
		if err := s.store.ZAdd(ctx, tokenQueueKey, store.ScoredMember{
			Member: outpoint.Bytes(),
			Score:  score,
		}); err != nil {
			return fmt.Errorf("failed to add to token queue: %w", err)
		}
	}

	return nil
}

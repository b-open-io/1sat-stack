package bsv21

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/fees"
	"github.com/b-open-io/1sat-stack/pkg/jbsync"
	"github.com/b-open-io/1sat-stack/pkg/overlay"
	"github.com/b-open-io/1sat-stack/pkg/store"
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
	SubscriptionID     string `mapstructure:"subscription_id"`     // JungleBus subscription for BSV21
	FromBlock          uint64 `mapstructure:"from_block"`          // Starting block (default: 811302)
	CategorizerWorkers int    `mapstructure:"categorizer_workers"` // Concurrency for categorizer
	TokenWorkers       int    `mapstructure:"token_workers"`       // Concurrency for token processing
}

// SyncServices manages BSV21 JungleBus sync
type SyncServices struct {
	config       *SyncConfig
	store        store.Store
	beefStorage  *beef.Storage
	overlay      *overlay.Services
	feeService   *fees.FeeService
	chainTracker chaintracks.Chaintracks
	jbClient     *junglebus.Client
	logger       *slog.Logger

	subscriber  *jbsync.Subscriber
	categorizer *worker.Worker
	manager     *TokenManager
}

// NewSyncServices creates a new BSV21 sync service
func NewSyncServices(
	cfg *SyncConfig,
	s store.Store,
	beefStorage *beef.Storage,
	overlaySvc *overlay.Services,
	feeService *fees.FeeService,
	ct chaintracks.Chaintracks,
	jbClient *junglebus.Client,
	logger *slog.Logger,
) (*SyncServices, error) {
	if logger == nil {
		logger = slog.Default()
	}

	// Set defaults
	if cfg.FromBlock == 0 {
		cfg.FromBlock = 811302 // BSV21 activation block
	}
	if cfg.CategorizerWorkers == 0 {
		cfg.CategorizerWorkers = 8
	}
	if cfg.TokenWorkers == 0 {
		cfg.TokenWorkers = 16
	}

	return &SyncServices{
		config:       cfg,
		store:        s,
		beefStorage:  beefStorage,
		overlay:      overlaySvc,
		feeService:   feeService,
		chainTracker: ct,
		jbClient:     jbClient,
		logger:       logger.With("component", "bsv21-sync"),
	}, nil
}

// Start begins the BSV21 sync pipeline
func (s *SyncServices) Start(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)

	// Start JungleBus subscriber (Stage 1)
	if s.config.SubscriptionID != "" && s.jbClient != nil {
		subscriberCfg := &jbsync.SubscriberConfig{
			SubscriptionID: s.config.SubscriptionID,
			QueueName:      "bsv21",
			FromBlock:      s.config.FromBlock,
			BatchSize:      1000,
			ReorgDepth:     6,
		}

		var err error
		s.subscriber, err = jbsync.NewSubscriber(subscriberCfg, s.store, s.chainTracker, s.jbClient, s.logger)
		if err != nil {
			return fmt.Errorf("failed to create subscriber: %w", err)
		}

		g.Go(func() error {
			return s.subscriber.Start(ctx)
		})
	}

	// Start categorizer (Stage 2)
	s.categorizer = worker.New(&worker.Config{
		Store:       s.store,
		Key:         jbsync.QueueKey("bsv21"),
		Concurrency: s.config.CategorizerWorkers,
		Handler:     s.categorize,
		OnError: func(ctx context.Context, id string, score float64, err error) {
			s.logger.Error("categorizer error", "txid", id, "score", score, "error", err)
		},
		PageSize:  1000,
		PollDelay: time.Second,
		Logger:    s.logger,
	})

	g.Go(func() error {
		return s.categorizer.Start(ctx)
	})

	// Start token manager (Stage 3)
	s.manager = NewTokenManager(
		s.store,
		s.beefStorage,
		s.overlay,
		s.feeService,
		s.chainTracker,
		s.config.TokenWorkers,
		s.logger,
	)

	g.Go(func() error {
		return s.manager.Start(ctx)
	})

	return g.Wait()
}

// categorize processes a transaction from the main queue and routes to token queues
func (s *SyncServices) categorize(ctx context.Context, txidStr string, score float64) error {
	txid, err := chainhash.NewHashFromHex(txidStr)
	if err != nil {
		return fmt.Errorf("failed to parse txid %s: %w", txidStr, err)
	}

	tx, err := s.beefStorage.LoadTx(ctx, txid)
	if err != nil {
		return fmt.Errorf("failed to load transaction %s: %w", txidStr, err)
	}

	// Extract token IDs from outputs
	seen := make(map[string]struct{})
	for vout, output := range tx.Outputs {
		b := bsv21.Decode(output.LockingScript)
		if b == nil {
			continue
		}
		tokenId := b.Id
		if b.Op == string(bsv21.OpMint) {
			tokenId = (&transaction.Outpoint{
				Txid:  *txid,
				Index: uint32(vout),
			}).OrdinalString()
		}

		if _, ok := seen[tokenId]; !ok {
			seen[tokenId] = struct{}{}

			// Add to token queue
			tokenQueueKey := []byte(jbsync.TokenQueueKey(tokenId))
			if err := s.store.ZAdd(ctx, tokenQueueKey, store.ScoredMember{
				Member: []byte(txidStr),
				Score:  score,
			}); err != nil {
				return fmt.Errorf("failed to add to token queue: %w", err)
			}

			// Discover new token
			if err := s.manager.DiscoverToken(ctx, tokenId); err != nil {
				s.logger.Error("failed to discover token", "error", err, "tokenId", tokenId)
			}
		}
	}

	return nil
}

// TokenManager manages per-token processor workers
type TokenManager struct {
	store        store.Store
	beefStorage  *beef.Storage
	overlay      *overlay.Services
	feeService   *fees.FeeService
	chainTracker chaintracks.Chaintracks
	concurrency  int
	logger       *slog.Logger

	workers sync.Map // tokenId → *TokenWorker
	limiter chan struct{}
	g       *errgroup.Group
	ctx     context.Context
}

// NewTokenManager creates a new token manager
func NewTokenManager(
	s store.Store,
	beefStorage *beef.Storage,
	overlaySvc *overlay.Services,
	feeService *fees.FeeService,
	ct chaintracks.Chaintracks,
	concurrency int,
	logger *slog.Logger,
) *TokenManager {
	return &TokenManager{
		store:        s,
		beefStorage:  beefStorage,
		overlay:      overlaySvc,
		feeService:   feeService,
		chainTracker: ct,
		concurrency:  concurrency,
		logger:       logger.With("component", "token-manager"),
		limiter:      make(chan struct{}, concurrency),
	}
}

// Start begins the token manager
func (m *TokenManager) Start(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)
	m.g = g
	m.ctx = ctx

	// Initial worker lifecycle management
	m.manageWorkerLifecycle(ctx)

	// Periodic lifecycle management
	g.Go(func() error {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				m.manageWorkerLifecycle(ctx)
			}
		}
	})

	return g.Wait()
}

// DiscoverToken handles discovery of a new token
func (m *TokenManager) DiscoverToken(ctx context.Context, tokenId string) error {
	// Check if already registered
	if _, exists := m.workers.Load(tokenId); exists {
		return nil
	}

	// Generate address for fee tracking
	if m.feeService == nil {
		return nil
	}

	topicId := "tm_" + tokenId
	address, err := m.feeService.Register(ctx, topicId, tokenId)
	if err != nil {
		return fmt.Errorf("failed to register token with fee service: %w", err)
	}

	// Check if token is eligible (has balance or whitelisted)
	activeTopics := m.feeService.GetActiveTopics(ctx)
	if _, active := activeTopics[topicId]; !active {
		return nil // Not eligible
	}

	// Create worker
	return m.createWorker(ctx, tokenId, address)
}

// createWorker creates a new worker for a token
func (m *TokenManager) createWorker(ctx context.Context, tokenId, address string) error {
	if m.g == nil || m.ctx == nil {
		return errors.New("token manager not started")
	}

	workerCtx, cancel := context.WithCancel(m.ctx)

	w := &TokenWorker{
		tokenId:      tokenId,
		address:      address,
		store:        m.store,
		beefStorage:  m.beefStorage,
		overlay:      m.overlay,
		chainTracker: m.chainTracker,
		limiter:      m.limiter,
		cancel:       cancel,
		logger:       m.logger.With("tokenId", tokenId),
	}

	m.workers.Store(tokenId, w)

	m.g.Go(func() error {
		defer m.workers.Delete(tokenId)
		return w.Run(workerCtx)
	})

	m.logger.Info("token worker created", "tokenId", tokenId)
	return nil
}

// manageWorkerLifecycle manages worker creation/destruction based on active topics
func (m *TokenManager) manageWorkerLifecycle(ctx context.Context) {
	if m.feeService == nil {
		return
	}

	// Get active topics
	activeTopics := m.feeService.GetActiveTopics(ctx)

	// Create workers for active topics that don't have workers
	for topicId := range activeTopics {
		// Extract tokenId from topicId (tm_{tokenId})
		if len(topicId) <= 3 || topicId[:3] != "tm_" {
			continue
		}
		tokenId := topicId[3:]

		if _, exists := m.workers.Load(tokenId); exists {
			continue
		}

		address, _ := m.feeService.GenerateAddress(tokenId)
		if err := m.createWorker(ctx, tokenId, address); err != nil {
			m.logger.Error("failed to create worker", "error", err, "tokenId", tokenId)
		}
	}

	// Remove workers for topics that are no longer active
	m.workers.Range(func(key, value any) bool {
		tokenId := key.(string)
		topicId := "tm_" + tokenId
		if _, active := activeTopics[topicId]; !active {
			if w, ok := value.(*TokenWorker); ok {
				w.cancel()
			}
		}
		return true
	})
}

// TokenWorker processes transactions for a single token
type TokenWorker struct {
	tokenId      string
	address      string
	store        store.Store
	beefStorage  *beef.Storage
	overlay      *overlay.Services
	chainTracker chaintracks.Chaintracks
	limiter      chan struct{}
	cancel       context.CancelFunc
	logger       *slog.Logger
}

// Run executes the token worker loop
func (w *TokenWorker) Run(ctx context.Context) error {
	queueKey := []byte(jbsync.TokenQueueKey(w.tokenId))

	for {
		// Get pending transactions
		members, err := w.store.ZRange(ctx, queueKey, store.ScoreRange{Count: 100})
		if err != nil {
			return fmt.Errorf("failed to load token queue: %w", err)
		}

		if len(members) == 0 {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(30 * time.Second):
				continue
			}
		}

		for _, member := range members {
			select {
			case <-ctx.Done():
				return nil
			default:
			}

			// Acquire limiter slot
			w.limiter <- struct{}{}

			txidStr := string(member.Member)
			if err := w.processTransaction(ctx, txidStr, member.Score); err != nil {
				w.logger.Error("failed to process transaction", "error", err, "txid", txidStr)
				// Continue processing other transactions
			}

			// Release limiter slot
			<-w.limiter

			// Remove from queue
			if err := w.store.ZRem(ctx, queueKey, member.Member); err != nil {
				w.logger.Error("failed to remove from queue", "error", err, "txid", txidStr)
			}
		}
	}
}

// processTransaction processes a single transaction
func (w *TokenWorker) processTransaction(ctx context.Context, txidStr string, score float64) error {
	if w.overlay == nil {
		return errors.New("overlay service not available")
	}

	txid, err := chainhash.NewHashFromHex(txidStr)
	if err != nil {
		return fmt.Errorf("failed to parse txid: %w", err)
	}

	// Load transaction with all inputs populated (needed for overlay validation)
	tx, err := w.beefStorage.BuildFullBeefTx(ctx, txid)
	if err != nil {
		return fmt.Errorf("failed to load transaction: %w", err)
	}

	// Build BEEF with all input transactions
	beefBytes, err := tx.AtomicBEEF(false)
	if err != nil {
		return fmt.Errorf("failed to build beef: %w", err)
	}

	// Submit to overlay engine
	topicName := "tm_" + w.tokenId
	_, err = w.overlay.Submit(ctx, sdkoverlay.TaggedBEEF{
		Beef:   beefBytes,
		Topics: []string{topicName},
	}, engine.SubmitModeHistorical)
	if err != nil {
		return fmt.Errorf("failed to submit to overlay: %w", err)
	}

	w.logger.Debug("transaction processed", "txid", txidStr)
	return nil
}

package bsv21

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	gaspqueue "github.com/b-open-io/1sat-stack/pkg/gasp"
	"github.com/b-open-io/1sat-stack/pkg/overlay"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/bsv-blockchain/go-chaintracks/chaintracks"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/gasp"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"golang.org/x/sync/errgroup"
)

// Set keys for token management
var (
	KeyWhitelist = txo.KeySet("bsv21:whitelist") // Tokens always active regardless of balance
	KeyBlacklist = txo.KeySet("bsv21:blacklist") // Tokens never active
)

// OwnerSyncer is an interface for syncing owner data
type OwnerSyncer interface {
	Sync(ctx context.Context, owner string) error
}

// TokenManager manages per-token processor workers
type TokenManager struct {
	store             store.Store
	beefStorage       *beef.Storage
	outputStore       *txo.OutputStore
	overlay           *overlay.Services
	ownerSync         OwnerSyncer
	chainTracker      chaintracks.Chaintracks
	concurrency       int
	feePerOutput      int64
	lifecycleInterval time.Duration
	logger            *slog.Logger

	workers  sync.Map // tokenId -> *TokenWorker
	statuses sync.Map // tokenId -> *TokenStatus
	limiter  chan struct{}
	g       *errgroup.Group
	ctx     context.Context
}

// NewTokenManager creates a new token manager
func NewTokenManager(
	s store.Store,
	beefStorage *beef.Storage,
	outputStore *txo.OutputStore,
	overlaySvc *overlay.Services,
	ownerSync OwnerSyncer,
	ct chaintracks.Chaintracks,
	concurrency int,
	feePerOutput int64,
	lifecycleInterval time.Duration,
	logger *slog.Logger,
) *TokenManager {
	if lifecycleInterval == 0 {
		lifecycleInterval = 5 * time.Minute
	}
	return &TokenManager{
		store:             s,
		beefStorage:       beefStorage,
		outputStore:       outputStore,
		overlay:           overlaySvc,
		ownerSync:         ownerSync,
		chainTracker:      ct,
		concurrency:       concurrency,
		feePerOutput:      feePerOutput,
		lifecycleInterval: lifecycleInterval,
		logger:            logger.With("component", "token-manager"),
		limiter:           make(chan struct{}, concurrency),
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
		ticker := time.NewTicker(m.lifecycleInterval)
		defer ticker.Stop()
		m.logger.Info("token lifecycle manager started", "interval", m.lifecycleInterval)
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				m.manageWorkerLifecycle(ctx)
			}
		}
	})

	// Background refresher for inactive tokens (every 15 minutes)
	g.Go(func() error {
		ticker := time.NewTicker(15 * time.Minute)
		defer ticker.Stop()
		m.logger.Info("inactive token refresher started", "interval", "15m")
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				m.refreshInactiveTokens(ctx)
			}
		}
	})

	return g.Wait()
}

// createWorker creates a new worker for a token
func (m *TokenManager) createWorker(ctx context.Context, status *TokenStatus) error {
	if m.g == nil || m.ctx == nil {
		return errors.New("token manager not started")
	}

	tokenId := status.TokenID
	topicName := "tm_" + tokenId

	// Create cancellable context for this worker
	workerCtx, cancel := context.WithCancel(ctx)

	// Create BeefRemote for local BEEF storage access
	beefRemote := gaspqueue.NewBeefRemote(m.beefStorage, m.store, "")

	// Create TopicWorker with GASP processing
	tw := gaspqueue.NewTopicWorker(&gaspqueue.TopicWorkerConfig{
		TopicName:   topicName,
		Store:       m.store,
		Engine:      m.overlay.Engine,
		Remotes:     []gasp.Remote{beefRemote},
		Concurrency: m.concurrency,
		OnProcessed: func(name string) error {
			m.onTokenItemProcessed(tokenId)
			return nil
		},
		Logger: m.logger.With("tokenId", tokenId),
	})

	tokenWorker := &TokenWorker{
		tokenId:   tokenId,
		address:   status.FeeAddress,
		worker:    nil, // TopicWorker is separate
		startedAt: time.Now(),
		cancel:    cancel,
	}

	// Store status for live tracking (all tokens, for admin API access)
	m.statuses.Store(tokenId, status)
	m.workers.Store(tokenId, tokenWorker)

	m.g.Go(func() error {
		defer m.workers.Delete(tokenId)
		defer m.statuses.Delete(tokenId)
		err := tw.Start(workerCtx)
		// Workers can be cancelled for valid lifecycle reasons (e.g., underfunding)
		// Return nil to prevent cascading shutdown of other workers
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			m.logger.Debug("worker exited normally", "tokenId", tokenId)
			return nil
		}
		return err
	})

	m.logger.Info("token worker created", "tokenId", tokenId)
	return nil
}

// onTokenItemProcessed is called after each successful item processing.
// It decrements the in-memory balance and triggers sync/shutdown if needed.
func (m *TokenManager) onTokenItemProcessed(tokenId string) {
	statusVal, ok := m.statuses.Load(tokenId)
	if !ok {
		return
	}
	status := statusVal.(*TokenStatus)

	// Whitelisted tokens don't track balance
	if status.IsWhitelisted {
		return
	}

	// Atomically decrement balance
	newBalance := status.Deduct()

	if newBalance <= 0 {
		// Balance exhausted - try to sync (only one goroutine will succeed)
		if !status.TryStartSync() {
			// Another goroutine is already syncing, let it handle this
			return
		}

		// We acquired the sync lock - do the sync and recalc
		go func() {
			defer status.EndSync()
			ctx := context.Background()

			// Sync fee address to get new UTXOs
			if m.ownerSync != nil {
				if err := m.ownerSync.Sync(ctx, status.FeeAddress); err != nil {
					m.logger.Debug("failed to sync fee address", "tokenId", tokenId, "error", err)
				}
			}

			// Recalculate from DB
			newStatus, err := m.GetTokenStatus(ctx, tokenId)
			if err != nil {
				m.logger.Error("failed to recalculate token status", "tokenId", tokenId, "error", err)
				return
			}

			// Update live balance from fresh calculation
			status.Credits = newStatus.Credits
			status.OutputCount = newStatus.OutputCount
			status.Debits = newStatus.Debits
			status.UpdateBalance(newStatus.Balance())

			// If still underfunded, cancel the worker
			if !status.IsActive() {
				if tw, ok := m.workers.Load(tokenId); ok {
					tw.(*TokenWorker).cancel()
					m.logger.Info("worker cancelled due to insufficient funding", "tokenId", tokenId)
				}
			}
		}()
	}
}

// manageWorkerLifecycle manages worker creation/destruction based on fee balances
func (m *TokenManager) manageWorkerLifecycle(ctx context.Context) {
	// Track which tokens are currently active (for cleanup at the end)
	activeTokens := make(map[string]struct{})

	// Phase 1: Register topic managers for all whitelisted tokens
	// (They should always be ready to receive transactions, even if no work queued yet)
	whitelistMembers, err := m.store.SMembers(ctx, KeyWhitelist)
	if err != nil {
		m.logger.Error("failed to load whitelist", "error", err)
	} else {
		for _, member := range whitelistMembers {
			tokenId := string(member)
			topicName := "tm_" + tokenId
			if m.overlay != nil {
				tm := NewBsv21ValidatedTopicManager(topicName, m.outputStore, nil)
				m.overlay.Engine.RegisterTopicManager(topicName, tm)
			}
			activeTokens[tokenId] = struct{}{}
		}
		if len(whitelistMembers) > 0 {
			m.logger.Debug("registered topic managers for whitelisted tokens", "count", len(whitelistMembers))
		}
	}

	// Phase 2: Discover tokens needing workers
	topicKey := txo.KeyTopicOutputs("tm_bsv21")
	members, err := m.store.ZRange(ctx, topicKey, store.ScoreRange{})
	if err != nil {
		m.logger.Error("failed to query tm_bsv21 topic", "error", err)
		return
	}

	for _, member := range members {
		outpoint := transaction.NewOutpointFromBytes(member.Member)
		if outpoint == nil {
			continue
		}
		tokenId := outpoint.OrdinalString()

		// Skip if worker already exists - it's self-monitoring
		if _, exists := m.workers.Load(tokenId); exists {
			activeTokens[tokenId] = struct{}{}
			continue
		}

		// Check funding for NEW tokens only
		status, err := m.GetTokenStatus(ctx, tokenId)
		if err != nil {
			m.logger.Debug("failed to get token status", "error", err, "tokenId", tokenId)
			continue
		}

		if !status.IsActive() {
			continue
		}

		activeTokens[tokenId] = struct{}{}

		// Register topic manager
		topicName := "tm_" + tokenId
		if m.overlay != nil {
			tm := NewBsv21ValidatedTopicManager(topicName, m.outputStore, nil)
			m.overlay.Engine.RegisterTopicManager(topicName, tm)
		}

		// Create worker
		if err := m.createWorker(ctx, status); err != nil {
			m.logger.Error("failed to create worker", "error", err, "tokenId", tokenId)
		}
	}

	// Phase 3: Unregister topic managers for tokens no longer active
	// (workers delete themselves from maps when they exit via deferred cleanup)
	m.workers.Range(func(key, value any) bool {
		tokenId := key.(string)
		if _, active := activeTokens[tokenId]; !active {
			topicName := "tm_" + tokenId
			if m.overlay != nil {
				m.overlay.Engine.UnregisterTopicManager(topicName)
			}
		}
		return true
	})
}

// refreshInactiveTokens syncs fee addresses for tokens without active workers.
// This allows inactive tokens to receive new funding deposits.
func (m *TokenManager) refreshInactiveTokens(ctx context.Context) {
	topicKey := txo.KeyTopicOutputs("tm_bsv21")
	members, err := m.store.ZRange(ctx, topicKey, store.ScoreRange{})
	if err != nil {
		m.logger.Error("failed to query tm_bsv21 topic for refresh", "error", err)
		return
	}

	refreshed := 0
	for _, member := range members {
		outpoint := transaction.NewOutpointFromBytes(member.Member)
		if outpoint == nil {
			continue
		}
		tokenId := outpoint.OrdinalString()

		// Only refresh tokens WITHOUT active workers
		if _, exists := m.workers.Load(tokenId); exists {
			continue
		}

		feeAddress, err := GenerateFeeAddress(outpoint)
		if err != nil {
			continue
		}

		if m.ownerSync != nil {
			if err := m.ownerSync.Sync(ctx, feeAddress); err != nil {
				m.logger.Debug("failed to sync inactive token fee address", "tokenId", tokenId, "error", err)
				continue
			}
			refreshed++
		}
	}

	if refreshed > 0 {
		m.logger.Debug("refreshed inactive token fee addresses", "count", refreshed)
	}
}

package bsv21

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/config"
	lookuppkg "github.com/b-open-io/1sat-stack/pkg/lookup"
	"github.com/b-open-io/1sat-stack/pkg/overlay"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/bsv-blockchain/go-chaintracks/chaintracks"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	sdkoverlay "github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"golang.org/x/sync/errgroup"
)

// OwnerSyncer is an interface for syncing owner data
type OwnerSyncer interface {
	Sync(ctx context.Context, owner string) error
}

// TokenManager manages per-token processor workers
type TokenManager struct {
	store             store.Store
	configStore       config.Store
	beefStorage       *beef.Storage
	outputStore       *txo.OutputStore
	overlay           *engine.Engine
	lookup            *lookuppkg.BSV21Lookup
	ownerSync         OwnerSyncer
	chainTracker      chaintracks.Chaintracks
	concurrency       int
	feePerOutput      int64
	lifecycleInterval time.Duration
	logger            *slog.Logger

	workers  sync.Map // tokenId -> *TokenWorker
	statuses sync.Map // tokenId -> *TokenStatus
	limiter  chan struct{}
	g        *errgroup.Group
	ctx      context.Context
}

// NewTokenManager creates a new token manager
func NewTokenManager(
	s store.Store,
	cs config.Store,
	beefStorage *beef.Storage,
	outputStore *txo.OutputStore,
	overlaySvc *engine.Engine,
	lookup *lookuppkg.BSV21Lookup,
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
		configStore:       cs,
		beefStorage:       beefStorage,
		outputStore:       outputStore,
		overlay:           overlaySvc,
		lookup:            lookup,
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

	// The evaluation that got us here may have used a persisted count that lags
	// what the token actually indexed, which understates debits. Settle it against
	// the database before committing a worker - the topic is being opened anyway.
	count, err := m.lookup.CountOutputs(ctx, topicName)
	if err != nil {
		return fmt.Errorf("failed to count outputs for %s: %w", tokenId, err)
	}
	status.SetOutputCount(count)
	status.UpdateBalance(int64(status.Credits) - status.Debits())
	m.persistOutputCount(ctx, tokenId, count)

	if !status.IsActive() {
		m.logger.Debug("token underfunded once counted", "tokenId", tokenId, "outputCount", count)
		return nil
	}

	// Create cancellable context for this worker
	workerCtx, cancel := context.WithCancel(ctx)

	// Create OverlaySync worker for this token's queue
	syncWorker := overlay.NewOverlaySync(
		&overlay.OverlaySyncConfig{
			QueueName:           topicName,
			Limiter:             m.limiter,
			ResolveDependencies: true,
			OnProcessed: func(name string) error {
				m.onTokenItemProcessed(tokenId)
				return nil
			},
		},
		topicName,
		m.store,
		m.beefStorage,
		m.overlay,
		m.logger.With("tokenId", tokenId),
	)

	tokenWorker := &TokenWorker{
		tokenId:   tokenId,
		address:   status.FeeAddress,
		worker:    nil,
		startedAt: time.Now(),
		cancel:    cancel,
	}

	// Store status for live tracking (all tokens, for admin API access)
	m.statuses.Store(tokenId, status)
	m.workers.Store(tokenId, tokenWorker)

	m.g.Go(func() error {
		defer m.workers.Delete(tokenId)
		defer m.statuses.Delete(tokenId)
		err := syncWorker.Start(workerCtx)
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

	// Atomically record the indexed output and charge for it
	newBalance := status.RecordOutput()

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
			status.SetOutputCount(newStatus.OutputCount())
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
	whitelistEntries, err := m.configStore.List(ctx, "bsv21.whitelist:")
	if err != nil {
		m.logger.Error("failed to load whitelist", "error", err)
	} else {
		for key := range whitelistEntries {
			tokenId := strings.TrimPrefix(key, "bsv21.whitelist:")
			topicName := "tm_" + tokenId
			if m.overlay != nil {
				var metadata *sdkoverlay.MetaData
				if outpoint, err := transaction.OutpointFromString(tokenId); err == nil {
					metadata = m.getTokenMetadata(ctx, outpoint)
				}
				tm := NewBsv21ValidatedTopicManager(topicName, nil, metadata)
				m.overlay.RegisterTopicManager(topicName, tm)
			}
			activeTokens[tokenId] = struct{}{}
		}
		if len(whitelistEntries) > 0 {
			m.logger.Debug("registered topic managers for whitelisted tokens", "count", len(whitelistEntries))
		}
	}

	// Phase 2: Discover tokens needing workers
	tokens, err := m.lookup.ListTokens(ctx)
	if err != nil {
		m.logger.Error("failed to query tm_bsv21 topic", "error", err)
		return
	}

	for _, token := range tokens {
		tokenId := token.TokenID

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
		status.Symbol = token.Symbol
		status.Decimals = token.Decimals
		status.Icon = token.Icon

		if !status.IsActive() {
			continue
		}

		activeTokens[tokenId] = struct{}{}

		// Register topic manager
		topicName := "tm_" + tokenId
		if m.overlay != nil {
			var metadata *sdkoverlay.MetaData
			if outpoint, err := transaction.OutpointFromString(tokenId); err == nil {
				metadata = m.getTokenMetadata(ctx, outpoint)
			}
			tm := NewBsv21ValidatedTopicManager(topicName, nil, metadata)
			m.overlay.RegisterTopicManager(topicName, tm)
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
				m.overlay.UnregisterTopicManager(topicName)
			}
		}
		return true
	})
}

// refreshInactiveTokens syncs fee addresses for tokens without active workers.
// This allows inactive tokens to receive new funding deposits.
func (m *TokenManager) refreshInactiveTokens(ctx context.Context) {
	tokens, err := m.lookup.ListTokens(ctx)
	if err != nil {
		m.logger.Error("failed to query tm_bsv21 topic for refresh", "error", err)
		return
	}

	refreshed := 0
	for _, token := range tokens {
		// Only refresh tokens WITHOUT active workers
		if _, exists := m.workers.Load(token.TokenID); exists {
			continue
		}

		outpoint, err := transaction.OutpointFromString(token.TokenID)
		if err != nil {
			continue
		}
		feeAddress, err := GenerateFeeAddress(outpoint)
		if err != nil {
			continue
		}

		if m.ownerSync != nil {
			if err := m.ownerSync.Sync(ctx, feeAddress); err != nil {
				m.logger.Debug("failed to sync inactive token fee address", "tokenId", token.TokenID, "error", err)
				continue
			}
			refreshed++
		}
	}

	if refreshed > 0 {
		m.logger.Debug("refreshed inactive token fee addresses", "count", refreshed)
	}
}

// ListTokenStatuses returns token statuses. By default only active/whitelisted tokens
// (from the live statuses map) are returned. When includeAll is true, all known tokens
// are returned with their status calculated from the DB.
func (m *TokenManager) ListTokenStatuses(ctx context.Context, includeAll bool) []*TokenStatus {
	if !includeAll {
		var statuses []*TokenStatus
		m.statuses.Range(func(_, value any) bool {
			statuses = append(statuses, value.(*TokenStatus))
			return true
		})
		return statuses
	}

	tokens, err := m.lookup.ListTokens(ctx)
	if err != nil {
		m.logger.Error("failed to list tokens", "error", err)
		return nil
	}

	statuses := make([]*TokenStatus, 0, len(tokens))
	for _, token := range tokens {
		// Use live status if available
		if val, ok := m.statuses.Load(token.TokenID); ok {
			statuses = append(statuses, val.(*TokenStatus))
			continue
		}
		status, err := m.GetTokenStatus(ctx, token.TokenID)
		if err != nil {
			m.logger.Debug("failed to get token status", "tokenId", token.TokenID, "error", err)
			continue
		}
		status.Symbol = token.Symbol
		status.Decimals = token.Decimals
		status.Icon = token.Icon
		statuses = append(statuses, status)
	}
	return statuses
}

// getTokenMetadata fetches token metadata from the output store for topic manager registration
func (m *TokenManager) getTokenMetadata(ctx context.Context, outpoint *transaction.Outpoint) *sdkoverlay.MetaData {
	cfg := &txo.OutputSearchCfg{
		IncludeTags: []string{"bsv21"},
	}
	output, err := m.outputStore.LoadOutput(ctx, outpoint, cfg)
	if err != nil || output == nil {
		return nil
	}

	bsv21DataRaw, ok := output.Data["bsv21"]
	if !ok {
		return nil
	}

	bsv21Data, ok := bsv21DataRaw.(map[string]any)
	if !ok {
		return nil
	}

	metadata := &sdkoverlay.MetaData{
		Description: "BSV21 Token",
	}

	// Use symbol as name if available
	if sym, ok := bsv21Data["sym"].(string); ok && sym != "" {
		metadata.Name = sym
	} else {
		metadata.Name = "BSV21"
	}

	// Use icon URL if available
	if icon, ok := bsv21Data["icon"].(string); ok && icon != "" {
		metadata.Icon = icon
	}

	return metadata
}

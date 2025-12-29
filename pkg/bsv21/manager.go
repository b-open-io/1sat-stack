package bsv21

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
	"github.com/b-open-io/1sat-stack/pkg/overlay"
	"github.com/b-open-io/1sat-stack/pkg/store"
	topicpkg "github.com/b-open-io/1sat-stack/pkg/topic"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/b-open-io/1sat-stack/pkg/worker"
	"github.com/bsv-blockchain/go-chaintracks/chaintracks"
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

	workers sync.Map // tokenId -> *TokenWorker
	limiter chan struct{}
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

	return g.Wait()
}

// createWorker creates a new worker for a token
func (m *TokenManager) createWorker(ctx context.Context, tokenId, address string) error {
	if m.g == nil || m.ctx == nil {
		return errors.New("token manager not started")
	}

	// Create GASP processor for this token with shared concurrency limit
	topic := "tm_" + tokenId
	processor := gaspqueue.NewProcessor(topic, m.beefStorage, m.overlay.Engine, m.concurrency)

	// Create handler - limiter is now managed by the worker
	handler := func(ctx context.Context, id string, score float64) error {
		// Parse outpoint from queue member
		outpoint := transaction.NewOutpointFromBytes([]byte(id))
		if outpoint == nil {
			return fmt.Errorf("invalid outpoint: %s", id)
		}

		return processor.ProcessOutput(ctx, outpoint)
	}

	// Create worker using generic worker package with shared limiter
	w := worker.New(&worker.Config{
		Store:   m.store,
		Key:     jbsync.TokenQueueKey(tokenId),
		Limiter: m.limiter, // Shared across all token workers
		Handler: handler,
		OnError: func(ctx context.Context, id string, score float64, err error) {
			m.logger.Error("token worker error", "tokenId", tokenId, "outpoint", id, "error", err)
		},
		Logger: m.logger.With("tokenId", tokenId),
	})

	tw := &TokenWorker{
		tokenId:   tokenId,
		address:   address,
		worker:    w,
		startedAt: time.Now(),
	}

	m.workers.Store(tokenId, tw)

	m.g.Go(func() error {
		defer m.workers.Delete(tokenId)
		return w.Start(m.ctx)
	})

	m.logger.Info("token worker created", "tokenId", tokenId)
	return nil
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
				tm := topicpkg.NewBsv21ValidatedTopicManager(topicName, m.outputStore, nil)
				m.overlay.Engine.RegisterTopicManager(topicName, tm)
			}
			activeTokens[tokenId] = struct{}{}
		}
		if len(whitelistMembers) > 0 {
			m.logger.Debug("registered topic managers for whitelisted tokens", "count", len(whitelistMembers))
		}
	}

	// Phase 2: Process tokens in tm_bsv21 topic (tokens with work to do)
	// For active tokens: register topic manager (if not already) and create worker
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

		// Get funding status (handles whitelist/blacklist checks efficiently)
		status, err := m.GetFundingStatus(ctx, tokenId)
		if err != nil {
			m.logger.Debug("failed to get funding status", "error", err, "tokenId", tokenId)
			continue
		}

		if !status.IsActive {
			continue
		}

		activeTokens[tokenId] = struct{}{}

		// Sync fee address to ingest any new payment transactions
		if m.ownerSync != nil {
			if err := m.ownerSync.Sync(ctx, status.FeeAddress); err != nil {
				m.logger.Debug("failed to sync fee address", "error", err, "address", status.FeeAddress, "tokenId", tokenId)
			}
		}

		// Create worker if not exists
		if _, exists := m.workers.Load(tokenId); !exists {
			// Register topic manager (may already be registered for whitelisted tokens, but that's ok)
			topicName := "tm_" + tokenId
			if m.overlay != nil {
				tm := topicpkg.NewBsv21ValidatedTopicManager(topicName, m.outputStore, nil)
				m.overlay.Engine.RegisterTopicManager(topicName, tm)
			}

			if err := m.createWorker(ctx, tokenId, status.FeeAddress); err != nil {
				m.logger.Error("failed to create worker", "error", err, "tokenId", tokenId)
			}
		}
	}

	// Phase 3: Stop workers and unregister topic managers for inactive tokens
	m.workers.Range(func(key, value any) bool {
		tokenId := key.(string)
		if _, active := activeTokens[tokenId]; !active {
			if w, ok := value.(*TokenWorker); ok {
				w.worker.Stop()
			}

			topicName := "tm_" + tokenId
			if m.overlay != nil {
				m.overlay.Engine.UnregisterTopicManager(topicName)
			}
		}
		return true
	})
}

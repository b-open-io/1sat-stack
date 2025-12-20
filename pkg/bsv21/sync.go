package bsv21

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/jbsync"
	"github.com/b-open-io/1sat-stack/pkg/logging"
	"github.com/b-open-io/1sat-stack/pkg/overlay"
	"github.com/b-open-io/1sat-stack/pkg/owner"
	"github.com/b-open-io/1sat-stack/pkg/store"
	topicpkg "github.com/b-open-io/1sat-stack/pkg/topic"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/b-open-io/1sat-stack/pkg/worker"
	"github.com/b-open-io/go-junglebus"
	"github.com/bitcoin-sv/go-templates/template/bsv21"
	"github.com/bsv-blockchain/go-chaintracks/chaintracks"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	bip32 "github.com/bsv-blockchain/go-sdk/compat/bip32"
	sdkoverlay "github.com/bsv-blockchain/go-sdk/overlay"
	bsvhash "github.com/bsv-blockchain/go-sdk/primitives/hash"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"golang.org/x/sync/errgroup"
)

// HD key for fee address generation (matches 1sat-indexer and bsv21-overlay-1sat-sync)
const hdKeyString = "xpub661MyMwAqRbcF221R74MPqdipLsgUevAAX4hZP2rywyEeShpbe3v2r9ciAvSGT6FB22TEmFLdUyeEDJL4ekG8s9H5WXbzDQPr6eW1zEYYy9"

var hdKey *bip32.ExtendedKey

func init() {
	var err error
	hdKey, err = bip32.GetHDKeyFromExtendedPublicKey(hdKeyString)
	if err != nil {
		panic(fmt.Sprintf("failed to initialize HD key: %v", err))
	}
}

// GenerateFeeAddress generates a deterministic Bitcoin address for a token's fee payments
func GenerateFeeAddress(tokenId string) (string, error) {
	hash := sha256.Sum256([]byte(tokenId))

	path := fmt.Sprintf("21/%d/%d",
		binary.BigEndian.Uint32(hash[:8])>>1,
		binary.BigEndian.Uint32(hash[24:])>>1)

	ek, err := hdKey.DeriveChildFromPath(path)
	if err != nil {
		return "", fmt.Errorf("failed to derive key for path %s: %w", path, err)
	}

	pubKey, err := ek.ECPubKey()
	if err != nil {
		return "", fmt.Errorf("failed to get public key: %w", err)
	}

	pubKeyBytes := pubKey.Compressed()
	pkHash := bsvhash.Hash160(pubKeyBytes)

	address, err := script.NewAddressFromPublicKeyHash(pkHash, true)
	if err != nil {
		return "", fmt.Errorf("failed to create address: %w", err)
	}

	return address.AddressString, nil
}

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
func (s *SyncServices) dispatch(ctx context.Context, txidStr string, score float64) error {
	txid, err := chainhash.NewHashFromHex(txidStr)
	if err != nil {
		return fmt.Errorf("failed to parse txid %s: %w", txidStr, err)
	}

	tx, err := s.beefStorage.LoadTx(ctx, txid)
	if err != nil {
		return fmt.Errorf("failed to load transaction %s: %w", txidStr, err)
	}

	// Track which tokens we've seen in this tx and if we need BEEF for discovery
	seen := make(map[string]struct{})
	var beefBytes []byte // Lazy-loaded only if we find a deploy operation

	for vout, output := range tx.Outputs {
		b := bsv21.Decode(output.LockingScript)
		if b == nil {
			continue
		}

		// Determine tokenId and if this is a deploy operation
		var tokenId string
		isDeploy := false

		switch b.Op {
		case string(bsv21.OpDeployMint), string(bsv21.OpDeployAuth):
			// Deploy operations: tokenId = outpoint
			outpoint := &transaction.Outpoint{
				Txid:  *txid,
				Index: uint32(vout),
			}
			tokenId = outpoint.OrdinalString()
			isDeploy = true
		default:
			// Transfer/burn/mint/auth operations: tokenId from the output
			tokenId = b.Id
		}

		if tokenId == "" {
			continue
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

			// For deploy operations, submit to tm_bsv21 for token discovery
			if isDeploy && s.overlay != nil {
				// Lazy-load BEEF only when needed
				if beefBytes == nil {
					fullTx, err := s.beefStorage.BuildFullBeefTx(ctx, txid)
					if err != nil {
						s.logger.Error("failed to build beef for discovery", "error", err, "txid", txidStr)
						continue
					}
					beefBytes, err = fullTx.AtomicBEEF(false)
					if err != nil {
						s.logger.Error("failed to serialize beef for discovery", "error", err, "txid", txidStr)
						continue
					}
				}

				// Submit to tm_bsv21 - TokenManager will pick up new tokens via manageWorkerLifecycle
				if _, err := s.overlay.Submit(ctx, sdkoverlay.TaggedBEEF{
					Beef:   beefBytes,
					Topics: []string{"tm_bsv21"},
				}, engine.SubmitModeHistorical); err != nil {
					s.logger.Error("failed to submit to tm_bsv21", "error", err, "txid", txidStr)
				}
			}
		}
	}

	return nil
}

// TokenManager manages per-token processor workers

// OwnerSyncer is an interface for syncing owner data
type OwnerSyncer interface {
	Sync(ctx context.Context, owner string) error
}

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

	workers sync.Map // tokenId → *TokenWorker
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
		startedAt:    time.Now(),
	}

	m.workers.Store(tokenId, w)

	m.g.Go(func() error {
		defer m.workers.Delete(tokenId)
		return w.Run(workerCtx)
	})

	m.logger.Info("token worker created", "tokenId", tokenId)
	return nil
}

// manageWorkerLifecycle manages worker creation/destruction based on fee balances
func (m *TokenManager) manageWorkerLifecycle(ctx context.Context) {
	// Query tm_bsv21 topic for all known token outpoints
	topicKey := []byte("z:tp:tm_bsv21")
	members, err := m.store.ZRange(ctx, topicKey, store.ScoreRange{})
	if err != nil {
		m.logger.Error("failed to query tm_bsv21 topic", "error", err)
		return
	}

	// Load whitelist (tokens always active regardless of balance)
	whitelist := make(map[string]struct{})
	if whitelistMembers, err := m.store.SMembers(ctx, []byte("bsv21:whitelist")); err == nil {
		for _, member := range whitelistMembers {
			whitelist[string(member)] = struct{}{}
		}
	}

	// Load blacklist (tokens never active)
	blacklist := make(map[string]struct{})
	if blacklistMembers, err := m.store.SMembers(ctx, []byte("bsv21:blacklist")); err == nil {
		for _, member := range blacklistMembers {
			blacklist[string(member)] = struct{}{}
		}
	}

	// Track which tokens are currently active
	activeTokens := make(map[string]struct{})

	// Evaluate each known token
	for _, member := range members {
		outpoint := transaction.NewOutpointFromBytes(member.Member)
		if outpoint == nil {
			continue
		}
		tokenId := outpoint.OrdinalString()

		// Skip blacklisted tokens
		if _, blocked := blacklist[tokenId]; blocked {
			continue
		}

		// Check if whitelisted (always active)
		_, isWhitelisted := whitelist[tokenId]

		// Derive fee address
		feeAddress, err := GenerateFeeAddress(tokenId)
		if err != nil {
			m.logger.Error("failed to generate fee address", "error", err, "tokenId", tokenId)
			continue
		}

		// Sync fee address to ingest any new payment transactions
		if m.ownerSync != nil {
			if err := m.ownerSync.Sync(ctx, feeAddress); err != nil {
				m.logger.Debug("failed to sync fee address", "error", err, "address", feeAddress, "tokenId", tokenId)
			}
		}

		// Calculate balance: credits - debits
		balance, err := m.calculateBalance(ctx, tokenId, feeAddress)
		if err != nil {
			m.logger.Debug("failed to calculate balance", "error", err, "tokenId", tokenId)
			// If whitelisted, continue even if balance check fails
			if !isWhitelisted {
				continue
			}
		}

		// Token is active if whitelisted OR balance > 0
		if isWhitelisted || balance > 0 {
			activeTokens[tokenId] = struct{}{}

			// Create worker if not exists
			if _, exists := m.workers.Load(tokenId); !exists {
				// Register topic with overlay engine
				topicName := "tm_" + tokenId
				if m.overlay != nil {
					tm := topicpkg.NewBsv21ValidatedTopicManager(topicName, m.outputStore, nil)
					m.overlay.Engine.RegisterTopicManager(topicName, tm)
				}

				if err := m.createWorker(ctx, tokenId, feeAddress); err != nil {
					m.logger.Error("failed to create worker", "error", err, "tokenId", tokenId)
				}
			}
		}
	}

	// Stop workers for tokens that are no longer active
	m.workers.Range(func(key, value any) bool {
		tokenId := key.(string)
		if _, active := activeTokens[tokenId]; !active {
			topicName := "tm_" + tokenId

			// Unregister topic from overlay engine
			if m.overlay != nil {
				m.overlay.Engine.UnregisterTopicManager(topicName)
			}

			if w, ok := value.(*TokenWorker); ok {
				w.cancel()
			}
		}
		return true
	})
}

// ListWorkers returns the status of all active token workers
func (m *TokenManager) ListWorkers(ctx context.Context) []WorkerStatus {
	var workers []WorkerStatus

	m.workers.Range(func(key, value any) bool {
		tokenId := key.(string)
		w, ok := value.(*TokenWorker)
		if !ok {
			return true
		}

		// Get queue depth
		queueKey := []byte(jbsync.TokenQueueKey(tokenId))
		queueDepth, err := m.store.ZCard(ctx, queueKey)
		if err != nil {
			m.logger.Warn("failed to get queue depth", "tokenId", tokenId, "error", err)
			queueDepth = 0
		}

		workers = append(workers, WorkerStatus{
			TokenID:    tokenId,
			FeeAddress: w.address,
			QueueDepth: queueDepth,
			StartedAt:  w.startedAt,
		})
		return true
	})

	return workers
}

// calculateBalance calculates credits - debits for a token
func (m *TokenManager) calculateBalance(ctx context.Context, tokenId, feeAddress string) (int64, error) {
	// Credits: unspent satoshis at fee address
	cfg := txo.NewOutputSearchCfg().
		WithStringKeys("own:" + feeAddress).
		WithFilterSpent(true)
	credits, _, err := m.outputStore.SearchBalance(ctx, cfg)
	if err != nil {
		return 0, fmt.Errorf("failed to query balance: %w", err)
	}

	// Debits: output count × fee per output
	topicKey := []byte("z:tp:tm_" + tokenId)
	outputCount, err := m.store.ZCard(ctx, topicKey)
	if err != nil {
		return 0, fmt.Errorf("failed to count outputs: %w", err)
	}

	debits := outputCount * m.feePerOutput
	balance := int64(credits) - debits

	m.logger.Debug("balance calculated",
		"tokenId", tokenId,
		"credits", credits,
		"outputCount", outputCount,
		"debits", debits,
		"balance", balance)

	return balance, nil
}

// WorkerStatus represents the status of a token worker for monitoring
type WorkerStatus struct {
	TokenID    string    `json:"token_id"`
	FeeAddress string    `json:"fee_address"`
	QueueDepth int64     `json:"queue_depth"`
	StartedAt  time.Time `json:"started_at"`
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
	startedAt    time.Time
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

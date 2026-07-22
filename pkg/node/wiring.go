package node

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/b-open-io/1sat-stack/admin"
	"github.com/b-open-io/1sat-stack/landing"
	"github.com/b-open-io/1sat-stack/pkg/arcadeclient"
	"github.com/b-open-io/1sat-stack/pkg/auth"
	"github.com/b-open-io/1sat-stack/pkg/bap"
	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/broadcast"
	"github.com/b-open-io/1sat-stack/pkg/bsocial"
	"github.com/b-open-io/1sat-stack/pkg/bsv21"
	configpkg "github.com/b-open-io/1sat-stack/pkg/config"
	"github.com/b-open-io/1sat-stack/pkg/indexer"
	"github.com/b-open-io/1sat-stack/pkg/jbsync"
	"github.com/b-open-io/1sat-stack/pkg/logging"
	"github.com/b-open-io/1sat-stack/pkg/opns"
	"github.com/b-open-io/1sat-stack/pkg/ordfs"
	ordlockpkg "github.com/b-open-io/1sat-stack/pkg/ordlock"
	"github.com/b-open-io/1sat-stack/pkg/overlay"
	"github.com/b-open-io/1sat-stack/pkg/owner"
	"github.com/b-open-io/1sat-stack/pkg/paymail"
	"github.com/b-open-io/1sat-stack/pkg/pubsub"
	"github.com/b-open-io/1sat-stack/pkg/queue"
	"github.com/b-open-io/1sat-stack/pkg/spends"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/b-open-io/1sat-stack/pkg/wallet"
	"github.com/b-open-io/1sat-stack/sweep"
	"github.com/b-open-io/go-junglebus"
	"github.com/bsv-blockchain/go-chaintracks/chaintracks"
	chaintracksconfig "github.com/bsv-blockchain/go-chaintracks/config"
	chaintracksroutes "github.com/bsv-blockchain/go-chaintracks/routes/fiber"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	msgbus "github.com/bsv-blockchain/go-p2p-message-bus"
	"github.com/bsv-blockchain/go-sdk/transaction"
	p2p "github.com/bsv-blockchain/go-teranode-p2p-client"
	"github.com/libp2p/go-libp2p/core/crypto"
	"go.mongodb.org/mongo-driver/v2/mongo"
	mongooptions "go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Services holds all initialized services
type Services struct {
	Store   *store.Services
	Queue   queue.Queue
	PubSub  *pubsub.Services
	Beef    *beef.Services
	TXO     *txo.Services
	Indexer *indexer.Services
	BSV21   *bsv21.Services
	BAP     *bap.Services
	BSocial *bsocial.Services
	OPNS    *opns.Services
	OrdLock *ordlockpkg.Services
	Overlay *overlay.Services
	Spends  *spends.Services
	ORDFS   *ordfs.Services
	Own     *owner.Services

	// StatusHandler is the arc status feedback loop for overlay-only
	// processes (no index). When index runs, its own StatusHandler is used
	// and this stays nil.
	StatusHandler *indexer.StatusHandler
	Admin         *admin.Services
	Sweep         *sweep.Services
	Landing       *landing.Services
	Wallet        *wallet.Services
	Paymail       *paymail.Services

	// ConfigStore for admin data (users, progress, settings)
	ConfigStore configpkg.Store

	// Auth middleware (nil when wallet is disabled)
	// Used for routes that require authentication (wallet, admin)
	AuthMiddleware *auth.Middleware

	// MongoDB client (shared by BAP and BSocial)
	MongoDB *mongo.Client

	// JungleBus subscriptions
	JBSubscribers []*jbsync.Subscriber

	// External services
	JungleBus         *junglebus.Client
	P2PClient         *p2p.Client
	Chaintracks       chaintracks.Chaintracks
	ChaintracksRoutes *chaintracksroutes.Routes
	// External arcade (HTTP)
	ArcadeClient     *arcadeclient.Client
	ArcadeBroker     *arcadeclient.EventBroker
	BroadcastHandler *broadcast.Handler
	BroadcastRoutes  *broadcast.Routes
}

// arcadeCheckpointStore adapts configpkg.Store to arcadeclient.CheckpointStore,
// translating configpkg.ErrNotFound into ("", nil) so a missing key on first
// run is treated as "no prior checkpoint" rather than a store error.
type arcadeCheckpointStore struct {
	cs configpkg.Store
}

func (a *arcadeCheckpointStore) Get(ctx context.Context, key string) (string, error) {
	v, err := a.cs.Get(ctx, key)
	if errors.Is(err, configpkg.ErrNotFound) {
		return "", nil
	}
	return v, err
}

func (a *arcadeCheckpointStore) Set(ctx context.Context, key string, value string) error {
	return a.cs.Set(ctx, key, value)
}

// Initialize creates all services from the configuration
func (c *Config) Initialize(ctx context.Context, logger *slog.Logger) (*Services, error) {
	if logger == nil {
		logger = slog.Default()
	}

	initStart := time.Now()
	svc := &Services{}

	// Gateway is a standalone reverse proxy: it runs alone and builds none of
	// the service substrate. Reject combining it with other services, and
	// short-circuit before any store/chaintracks/overlay initialization.
	if c.gatewayCombined() {
		return nil, fmt.Errorf("gateway mode cannot be combined with other services in one process; run gateway alone")
	}
	if c.runsService("gateway") {
		logger.Info("gateway mode: reverse proxy only, skipping service substrate")
		return svc, nil
	}

	// Initialize store (foundational - other services may depend on it)
	start := time.Now()
	storeSvc, err := c.Store.Initialize(ctx, logging.NewComponentLogger(logger, "store", ""))
	if err != nil {
		return nil, fmt.Errorf("failed to initialize store: %w", err)
	}
	svc.Store = storeSvc
	logger.Info("store initialized", "duration", time.Since(start).Round(time.Millisecond))

	// Initialize config store (admin data, users, settings)
	start = time.Now()
	configDBPath := filepath.Join(c.DataDir, "config.db")
	configStore, err := configpkg.NewSQLiteStore(configDBPath)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize config store: %w", err)
	}
	svc.ConfigStore = configStore
	logger.Info("config store initialized", "duration", time.Since(start).Round(time.Millisecond))

	// Load runtime config from config store and apply to Config struct
	runtimeCfg, err := configpkg.LoadRuntimeConfig(ctx, configStore, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to load runtime config: %w", err)
	}
	c.applyRuntimeConfig(runtimeCfg)
	c.applyModes()
	c.resolveAllPaths()

	// Initialize the work queue. Default provider "inherit" wraps the main
	// store (same q:* keys); provider "store" opens a dedicated backend.
	// With no main store (disabled) and no dedicated backend, there is no queue.
	var mainStore store.Store
	if storeSvc != nil {
		mainStore = storeSvc.Store
	}
	if mainStore != nil || c.Queue.Provider == queue.ProviderStore {
		start = time.Now()
		queueSvc, err := c.Queue.Initialize(ctx, logging.NewComponentLogger(logger, "queue", ""), mainStore)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize queue: %w", err)
		}
		svc.Queue = queueSvc
		logger.Info("queue initialized", "provider", c.Queue.Provider, "duration", time.Since(start).Round(time.Millisecond))
	}

	// Initialize pubsub
	start = time.Now()
	pubsubSvc, err := c.PubSub.Initialize(ctx, logging.NewComponentLogger(logger, "pubsub", ""))
	if err != nil {
		return nil, fmt.Errorf("failed to initialize pubsub: %w", err)
	}
	svc.PubSub = pubsubSvc
	logger.Info("pubsub initialized", "duration", time.Since(start).Round(time.Millisecond))

	// Initialize JungleBus client (shared by multiple services)
	start = time.Now()
	jbOpts := []junglebus.ClientOps{
		junglebus.WithHTTP(c.JungleBus.URL),
		junglebus.WithSSL(c.JungleBus.SSL),
		junglebus.WithVersion(c.JungleBus.Version),
		junglebus.WithDebugging(c.JungleBus.Debug),
	}
	if c.JungleBus.Token != "" {
		jbOpts = append(jbOpts, junglebus.WithToken(c.JungleBus.Token))
	}
	jbClient, err := junglebus.New(jbOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize junglebus client: %w", err)
	}
	svc.JungleBus = jbClient
	logger.Info("junglebus client initialized", "url", c.JungleBus.URL, "duration", time.Since(start).Round(time.Millisecond))

	// Initialize P2P client (used by chaintracks)
	if c.Chaintracks.Mode == chaintracksconfig.ModeEmbedded {
		start = time.Now()
		// Set network on P2P config
		c.P2P.Network = c.Network
		c.P2P.MsgBus.Logger = p2p.NewSlogLogger(logging.NewComponentLogger(logger, "p2p", ""))
		p2pClient, err := c.P2P.Initialize(ctx, "1sat-stack")
		if err != nil {
			return nil, fmt.Errorf("failed to initialize p2p client: %w", err)
		}
		svc.P2PClient = p2pClient
		logger.Info("p2p client initialized", "network", c.P2P.Network, "duration", time.Since(start).Round(time.Millisecond))
	}

	// Initialize Chaintracks (primitive for blockchain state - must be before beef)
	if c.Chaintracks.Mode != "" && c.Chaintracks.Mode != "disabled" {
		start = time.Now()
		chaintracker, err := c.Chaintracks.Initialize(ctx, "1sat-stack", svc.P2PClient)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize chaintracks: %w", err)
		}
		svc.Chaintracks = chaintracker
		svc.ChaintracksRoutes = chaintracksroutes.NewRoutes(ctx, chaintracker)
		logger.Info("chaintracks initialized", "mode", c.Chaintracks.Mode, "duration", time.Since(start).Round(time.Millisecond))
	}

	// Initialize beef storage (pass JungleBus client for fallback lookups)
	start = time.Now()
	beefSvc, err := c.Beef.Initialize(ctx, logging.NewComponentLogger(logger, "beef", ""), svc.Chaintracks, svc.JungleBus)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize beef: %w", err)
	}
	svc.Beef = beefSvc
	if c.runsService("beef") && c.Beef.Routes.Enabled && svc.Chaintracks != nil {
		svc.Beef.Routes = beef.NewRoutes(beefSvc.Storage, svc.Chaintracks.GetHeight)
	}
	logger.Info("beef initialized", "duration", time.Since(start).Round(time.Millisecond))

	// Initialize external arcade HTTP client + event broker + broadcast handler.
	if runtimeCfg.ArcadeURL != "" && runtimeCfg.ArcadeCallbackToken != "" {
		start = time.Now()
		arcadeLogger := logging.NewComponentLogger(logger, "arcade", "")
		svc.ArcadeClient = arcadeclient.New(runtimeCfg.ArcadeURL, runtimeCfg.ArcadeCallbackToken, nil, arcadeLogger)
		svc.ArcadeBroker = arcadeclient.NewEventBroker(svc.ArcadeClient, arcadeLogger)
		svc.ArcadeBroker.SetCheckpointStore(&arcadeCheckpointStore{cs: svc.ConfigStore}, "progress:arcade_sse")

		waitTimeout := broadcast.DefaultWaitTimeout
		if d, perr := time.ParseDuration(runtimeCfg.ArcadeWaitTimeout); perr == nil && d > 0 {
			waitTimeout = d
		}
		if c.runsService("index") {
			svc.BroadcastHandler = broadcast.NewHandler(svc.ArcadeBroker, svc.Beef.Storage, waitTimeout, arcadeLogger)
			svc.BroadcastRoutes = broadcast.NewRoutes(svc.BroadcastHandler, svc.ArcadeClient, arcadeLogger)
		}

		// Bridge arcade SSE events to the local "arc" pubsub topic so the
		// existing StatusHandler (and any other arc-pubsub consumer) keeps
		// working without changes. For terminal statuses, we fetch the full
		// status to populate MerklePath / ExtraInfo (SSE payload is slim).
		if svc.PubSub != nil {
			indexer.StartArcBridge(svc.ArcadeBroker, svc.PubSub.PubSub, svc.ArcadeClient, arcadeLogger)
		}

		logger.Info("external arcade client initialized",
			"url", runtimeCfg.ArcadeURL, "wait_timeout", waitTimeout, "duration", time.Since(start).Round(time.Millisecond))
	}

	// Initialize TXO storage with shared dependencies. txo is the index
	// service's storage domain; overlay-only processes never build it.
	if c.runsService("index") && c.TXO.Mode != txo.ModeDisabled {
		start = time.Now()
		txoSvc, err := c.TXO.Initialize(ctx, logging.NewComponentLogger(logger, "txo", ""), storeSvc, pubsubSvc, beefSvc)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize txo: %w", err)
		}
		svc.TXO = txoSvc
		logger.Info("txo initialized", "duration", time.Since(start).Round(time.Millisecond))
	}

	// Initialize overlay engine FIRST (BSV21 needs it for topic/lookup registration).
	// The overlay engine carries no txo: overlay-submitted txs are ingested via
	// the broadcast→arcade→arc-status feedback loop, not the adapter, so the
	// adapter IngestTx stays nil in every mode (decision 9).
	if c.Overlay.Mode != overlay.ModeDisabled && c.Overlay.Mode != "" {
		start = time.Now()
		overlayDeps := &overlay.InitializeDeps{
			IngestTx:     nil,
			ChainTracker: svc.Chaintracks,
			Queue:        svc.Queue,
		}
		// Add optional dependencies if available
		if svc.Store != nil {
			overlayDeps.Store = svc.Store.Store
		}
		if svc.Beef != nil {
			overlayDeps.BeefStorage = svc.Beef.Storage
		}
		if svc.ArcadeClient != nil {
			overlayDeps.Broadcaster = broadcast.NewBroadcaster(svc.ArcadeClient, logger)
		}
		overlayLogger := logging.NewComponentLogger(logger, "overlay", "")
		// Create overlay P2P bus if enabled
		if c.Overlay.P2P.Enabled && svc.Store != nil {
			p2pBus, err := createOverlayP2PBus(c.Overlay.P2P, c.Wallet.ServerPrivateKey, svc.Queue, overlayLogger)
			if err != nil {
				return nil, fmt.Errorf("failed to create overlay P2P bus: %w", err)
			}
			overlayDeps.P2PBus = p2pBus
		}
		overlaySvc, err := c.Overlay.Initialize(ctx, overlayLogger, overlayDeps)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize overlay: %w", err)
		}
		svc.Overlay = overlaySvc

		logger.Info("overlay initialized", "duration", time.Since(start).Round(time.Millisecond))
	}

	// Get shared module deps for overlay modules
	var moduleDeps *overlay.ModuleDeps
	if svc.Overlay != nil {
		moduleDeps = svc.Overlay.ModuleDeps
	}

	// Initialize BSV21. Fee-address sync and balance resolve either against the
	// in-process index (embedded) or a remote index service over HTTP (remote),
	// so bsv21 can run standalone with no co-located txo/indexer.
	if c.runsService("bsv21") && c.BSV21.Mode != bsv21.ModeDisabled && moduleDeps != nil {
		start = time.Now()

		var txoStore *txo.OutputStore
		if svc.TXO != nil {
			txoStore = svc.TXO.OutputStore
		}

		var ownerSync bsv21.OwnerSyncer
		var balance bsv21.BalanceLookup
		if c.BSV21.Sync != nil && c.BSV21.Sync.Enabled {
			switch c.BSV21.Owner.Mode {
			case bsv21.OwnerModeRemote:
				if c.BSV21.Owner.URL == "" {
					return nil, fmt.Errorf("bsv21 owner.mode=remote requires bsv21.owner.url")
				}
				ownerClient := bsv21.NewHTTPOwnerClient(c.BSV21.Owner.URL, nil)
				ownerSync = ownerClient
				balance = ownerClient.Balance
				logger.Info("bsv21 owner resolution: remote", "url", c.BSV21.Owner.URL)
			case bsv21.OwnerModeEmbedded, "":
				if !c.runsService("index") || svc.TXO == nil {
					return nil, fmt.Errorf("bsv21 owner.mode=embedded requires the index service in-process; set bsv21.owner.mode=remote and bsv21.owner.url to point at an index service")
				}
				ownerLogger := logging.NewComponentLogger(logger, "owner", "")
				idx := indexer.NewIngestCtx(svc.TXO.OutputStore, svc.Beef.Storage, ownerLogger)
				ownerSync = owner.NewOwnerSync(svc.JungleBus, svc.Beef.Storage, idx, svc.TXO.OutputStore, svc.ConfigStore, ownerLogger)
				outputStore := svc.TXO.OutputStore
				balance = func(ctx context.Context, address string) (int64, error) {
					bal, _, err := outputStore.SearchBalance(ctx, &txo.OutputSearchCfg{
						SearchCfg: store.SearchCfg{Keys: [][]byte{[]byte("own:" + address)}},
					})
					return int64(bal), err
				}
				logger.Info("bsv21 owner resolution: embedded")
			default:
				return nil, fmt.Errorf("unknown bsv21 owner.mode: %s", c.BSV21.Owner.Mode)
			}
		}

		bsv21Svc, err := c.BSV21.Initialize(ctx, logging.NewComponentLogger(logger, "bsv21", c.BSV21.LogLevel), txoStore, moduleDeps, svc.ConfigStore, svc.Chaintracks, svc.Beef.Storage, svc.JungleBus, svc.Queue, ownerSync, balance)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize bsv21: %w", err)
		}
		svc.BSV21 = bsv21Svc
		logger.Info("bsv21 initialized", "duration", time.Since(start).Round(time.Millisecond))
	}

	// Initialize MongoDB (used by BSocial)
	if c.MongoDB.URL != "" && c.runsService("bsocial") && c.BSocial.Mode != bsocial.ModeDisabled {
		start = time.Now()
		mongoClient, err := mongo.Connect(mongooptions.Client().ApplyURI(c.MongoDB.URL))
		if err != nil {
			return nil, fmt.Errorf("failed to connect to mongodb: %w", err)
		}
		if err := mongoClient.Ping(ctx, nil); err != nil {
			return nil, fmt.Errorf("failed to ping mongodb: %w", err)
		}
		svc.MongoDB = mongoClient
		logger.Info("mongodb initialized", "duration", time.Since(start).Round(time.Millisecond))
	}

	// Initialize BAP
	if c.runsService("bap") && c.BAP.Mode != bap.ModeDisabled && moduleDeps != nil {
		start = time.Now()
		bapLogger := logging.NewComponentLogger(logger, "bap", c.BAP.LogLevel)
		bapSvc, err := c.BAP.Initialize(ctx, bapLogger, moduleDeps)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize bap: %w", err)
		}
		svc.BAP = bapSvc

		if svc.Beef != nil {
			syncCfg := c.BAP.Sync
			if syncCfg == nil {
				syncCfg = &overlay.OverlaySyncConfig{}
			}
			if syncCfg.QueueName == "" {
				syncCfg.QueueName = "bap"
			}
			svc.BAP.Sync = overlay.NewOverlaySync(syncCfg, "tm_bap", svc.Queue, svc.Beef.Storage, svc.BAP.Engine, bapLogger)
		}
		logger.Info("bap initialized", "duration", time.Since(start).Round(time.Millisecond))
	}

	// Initialize BSocial
	if c.runsService("bsocial") && c.BSocial.Mode != bsocial.ModeDisabled && svc.MongoDB != nil {
		start = time.Now()
		bsocialDB := svc.MongoDB.Database("bsocial")
		bsocialLogger := logging.NewComponentLogger(logger, "bsocial", c.BSocial.LogLevel)
		bsocialSvc, err := c.BSocial.Initialize(ctx, bsocialLogger, bsocialDB, moduleDeps)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize bsocial: %w", err)
		}
		svc.BSocial = bsocialSvc

		if svc.Beef != nil && moduleDeps != nil {
			syncCfg := c.BSocial.Sync
			if syncCfg == nil {
				syncCfg = &overlay.OverlaySyncConfig{}
			}
			if syncCfg.QueueName == "" {
				syncCfg.QueueName = "bsocial"
			}
			svc.BSocial.Sync = overlay.NewOverlaySync(syncCfg, "tm_bsocial", svc.Queue, svc.Beef.Storage, svc.BSocial.Engine, bsocialLogger)
		}
		logger.Info("bsocial initialized", "duration", time.Since(start).Round(time.Millisecond))
	}

	// Initialize OPNS
	if c.runsService("opns") && c.OPNS.Mode != opns.ModeDisabled && moduleDeps != nil {
		start = time.Now()
		opnsLogger := logging.NewComponentLogger(logger, "opns", c.OPNS.LogLevel)
		opnsSvc, err := c.OPNS.Initialize(ctx, opnsLogger, moduleDeps)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize opns: %w", err)
		}
		svc.OPNS = opnsSvc

		if c.OPNS.Crawl.Enabled && svc.Beef != nil {
			c.OPNS.Crawl.JungleBusURL = c.JungleBus.URL
			svc.OPNS.Crawl = opns.NewGenesisCrawl(c.OPNS.Crawl, svc.Beef.Storage, svc.OPNS.Engine, opnsLogger)
		}

		if svc.Beef != nil {
			opnsSyncCfg := c.OPNS.Sync
			if opnsSyncCfg == nil {
				opnsSyncCfg = &overlay.OverlaySyncConfig{}
			}
			opnsSyncCfg.QueueName = "opns"
			svc.OPNS.Sync = overlay.NewOverlaySync(opnsSyncCfg, "tm_opns", svc.Queue, svc.Beef.Storage, svc.OPNS.Engine, opnsLogger)
		}
		logger.Info("opns initialized", "duration", time.Since(start).Round(time.Millisecond))
	}

	// Initialize OrdLock
	if c.runsService("ordlock") && c.OrdLock.Mode != ordlockpkg.ModeDisabled && moduleDeps != nil {
		start = time.Now()
		ordlockLogger := logging.NewComponentLogger(logger, "ordlock", c.OrdLock.LogLevel)
		ordlockSvc, err := c.OrdLock.Initialize(ctx, ordlockLogger, moduleDeps)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize ordlock: %w", err)
		}
		svc.OrdLock = ordlockSvc

		if svc.Beef != nil {
			syncCfg := c.OrdLock.Sync
			if syncCfg == nil {
				syncCfg = &overlay.OverlaySyncConfig{}
			}
			if syncCfg.QueueName == "" {
				syncCfg.QueueName = ordlockpkg.QueueName
			}
			svc.OrdLock.Sync = overlay.NewOverlaySync(syncCfg, ordlockpkg.TopicName, svc.Queue, svc.Beef.Storage, svc.OrdLock.Engine, ordlockLogger)
		}
		logger.Info("ordlock initialized", "duration", time.Since(start).Round(time.Millisecond))
	}

	// Initialize Spends
	if c.runsService("index") && c.Spends.Mode != spends.ModeDisabled && c.Spends.Mode != "" && svc.Store != nil {
		start = time.Now()
		spendsSvc, err := c.Spends.Initialize(ctx, logging.NewComponentLogger(logger, "spends", ""), svc.Store.Store, svc.JungleBus)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize spends: %w", err)
		}
		svc.Spends = spendsSvc
		if svc.TXO != nil && spendsSvc != nil {
			svc.TXO.OutputStore.SpendService = spendsSvc.Storage
			if svc.TXO.Routes != nil {
				svc.TXO.Routes.Spends = spendsSvc.Storage
			}
		}
		logger.Info("spends initialized", "duration", time.Since(start).Round(time.Millisecond))
	}

	// Initialize ORDFS content serving
	if c.runsService("ordfs") && c.ORDFS.Enabled {
		start = time.Now()
		var spendsStorage *spends.Storage
		if svc.Spends != nil {
			spendsStorage = svc.Spends.Storage
		}
		var beefStorage *beef.Storage
		if svc.Beef != nil {
			beefStorage = svc.Beef.Storage
		}
		ordfsSvc, err := c.ORDFS.Initialize(ctx, logging.NewComponentLogger(logger, "ordfs", ""), c.DataDir, spendsStorage, beefStorage)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize ordfs: %w", err)
		}
		svc.ORDFS = ordfsSvc

		// Wire ORDFS into OrdLock for origin resolution on transferred ordinals
		if svc.OrdLock != nil && svc.OrdLock.Lookup != nil {
			svc.OrdLock.Lookup.SetOrdfs(ordfsSvc.Ordfs)
		}

		logger.Info("ordfs initialized", "duration", time.Since(start).Round(time.Millisecond))
	}

	// Initialize indexer services (shared IngestCtx used by owner sync and ingest sync)
	if c.runsService("index") && c.Indexer.Mode != indexer.ModeDisabled && svc.TXO != nil && svc.Beef != nil {
		start = time.Now()
		indexerDeps := &indexer.InitializeDeps{
			Store:       svc.Store.Store,
			Queue:       svc.Queue,
			BeefStorage: svc.Beef.Storage,
			OutputStore: svc.TXO.OutputStore,
		}
		if svc.ORDFS != nil {
			indexerDeps.Ordfs = svc.ORDFS.Ordfs
		}
		indexerSvc, err := c.Indexer.Initialize(ctx, logger, indexerDeps)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize indexer: %w", err)
		}
		svc.Indexer = indexerSvc

		// Setup status handler to process all arc events (from the SSE bridge below)
		if svc.PubSub != nil {
			statusDeps := &indexer.StatusHandlerDeps{
				PubSub:       svc.PubSub.PubSub,
				ChainTracker: svc.Chaintracks,
			}
			if svc.Overlay != nil {
				statusDeps.OverlayStorage = svc.Overlay.NewStorageAdapter()
				statusDeps.TopicIndex = svc.Overlay.TxTopicIndex()
			}
			statusDeps.LookupServices = svc.overlayLookupServices()
			svc.Indexer.SetupStatusHandler(statusDeps)
		}

		// Setup pending auditor to verify proofs on each new block
		if svc.Chaintracks != nil {
			auditorDeps := &indexer.PendingAuditorDeps{
				Chaintracks:  svc.Chaintracks,
				ArcadeClient: svc.ArcadeClient,
			}
			if svc.PubSub != nil {
				auditorDeps.PubSub = svc.PubSub.PubSub
			}
			svc.Indexer.SetupPendingAuditor(auditorDeps)
		}

		// Wire indexer to TXO for overlay flow integration
		if svc.TXO != nil && svc.TXO.OutputStore != nil {
			ingestCtx := svc.Indexer.Indexer
			svc.TXO.OutputStore.IngestTx = func(ctx context.Context, tx *transaction.Transaction) error {
				_, err := ingestCtx.IngestTx(ctx, tx)
				return err
			}
			logger.Debug("wired indexer.IngestTx to OutputStore for overlay flow")
		}
		logger.Info("indexer initialized", "mode", c.Indexer.Mode, "duration", time.Since(start).Round(time.Millisecond))
	}

	// Overlay-only processes (no index) still consume arc status events to roll
	// back their overlay topic storage on rejected/stale txs. Wire a StatusHandler
	// with the overlay storage, topic index, and running modules' lookups, but no
	// txo-dependent pieces (no indexer, no ingest).
	if !c.runsService("index") && svc.Overlay != nil && svc.PubSub != nil && svc.Beef != nil {
		var processStore store.Store
		if svc.Store != nil {
			processStore = svc.Store.Store
		}
		svc.StatusHandler = indexer.NewStatusHandler(
			svc.PubSub.PubSub,
			processStore,
			svc.Beef.Storage,
			svc.Overlay.NewStorageAdapter(),
			svc.Overlay.TxTopicIndex(),
			svc.overlayLookupServices(),
			svc.Chaintracks,
			nil,
			&indexer.StatusHandlerConfig{IngestEnabled: false},
			logging.NewComponentLogger(logger, "status", ""),
		)
		logger.Info("overlay status handler initialized (no index)")
	}

	// Initialize owner services (depends on TXO, Beef, Indexer)
	if c.runsService("index") && c.Owner.Mode != owner.ModeDisabled && svc.TXO != nil && svc.Beef != nil && svc.Indexer != nil {
		start = time.Now()
		ownSvc, err := c.Owner.Initialize(ctx, logger, &owner.InitializeDeps{
			JungleBus:   svc.JungleBus,
			BeefStorage: svc.Beef.Storage,
			Indexer:     svc.Indexer.Indexer, // Use shared IngestCtx from indexer services
			OutputStore: svc.TXO.OutputStore,
			ConfigStore: svc.ConfigStore,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to initialize own: %w", err)
		}
		svc.Own = ownSvc
		logger.Info("owner initialized", "mode", c.Owner.Mode, "duration", time.Since(start).Round(time.Millisecond))
	} else {
		logger.Debug("own service not initialized", "mode", c.Owner.Mode, "txoNil", svc.TXO == nil, "indexerNil", svc.Indexer == nil)
	}

	// Initialize admin UI
	if c.Admin.Mode != admin.ModeDisabled && svc.Store != nil {
		start = time.Now()
		// Build engines map for admin topic/lookup listing
		engines := make(map[string]*engine.Engine)
		if svc.BAP != nil {
			engines["bap"] = svc.BAP.Engine
		}
		if svc.BSocial != nil {
			engines["bsocial"] = svc.BSocial.Engine
		}
		if svc.OPNS != nil {
			engines["opns"] = svc.OPNS.Engine
		}
		if svc.OrdLock != nil {
			engines["ordlock"] = svc.OrdLock.Engine
		}
		if svc.BSV21 != nil {
			engines["bsv21"] = svc.BSV21.Engine
		}

		adminDeps := &admin.InitializeDeps{
			Overlay:        svc.Overlay,
			Engines:        engines,
			Store:          svc.Store.Store,
			ConfigStore:    svc.ConfigStore,
			RequestRestart: c.RequestRestart,
			LogStore:       c.LogStore,
		}
		if svc.BSV21 != nil {
			adminDeps.BSV21Sync = svc.BSV21.Sync
		}
		// Wire OpNS crawl trigger if dependencies are available
		if svc.OPNS != nil && svc.Beef != nil && svc.Overlay != nil {
			adminDeps.TriggerOpnsCrawl = func(triggerCtx context.Context) error {
				if svc.OPNS.Crawl != nil {
					return fmt.Errorf("crawl already running")
				}
				crawlCfg := c.OPNS.Crawl
				crawlCfg.Enabled = true
				crawlCfg.JungleBusURL = c.JungleBus.URL
				opnsLogger := logging.NewComponentLogger(logger, "opns", c.OPNS.LogLevel)
				svc.OPNS.Crawl = opns.NewGenesisCrawl(crawlCfg, svc.Beef.Storage, svc.OPNS.Engine, opnsLogger)
				go func() {
					if err := svc.OPNS.Crawl.Start(ctx); err != nil {
						opnsLogger.Error("OpNS crawl error", "error", err)
					}
					svc.OPNS.Crawl = nil
				}()
				return nil
			}
		}
		adminSvc, err := c.Admin.Initialize(ctx, logging.NewComponentLogger(logger, "admin", ""), adminDeps)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize admin: %w", err)
		}
		svc.Admin = adminSvc
		logger.Info("admin initialized", "mode", c.Admin.Mode, "duration", time.Since(start).Round(time.Millisecond))
	}

	// Initialize Sweep UI
	if c.Sweep.Mode != sweep.ModeDisabled {
		sweepSvc, err := c.Sweep.Initialize(ctx, logging.NewComponentLogger(logger, "sweep", ""))
		if err != nil {
			return nil, fmt.Errorf("failed to initialize sweep: %w", err)
		}
		svc.Sweep = sweepSvc
	}

	// Initialize Landing page
	landingSvc, err := c.Landing.Initialize(ctx, logging.NewComponentLogger(logger, "landing", ""))
	if err != nil {
		return nil, fmt.Errorf("landing init: %w", err)
	}
	svc.Landing = landingSvc

	// Initialize Wallet service
	if c.Wallet.ServerPrivateKey != "" {
		walletSvc, err := c.Wallet.Initialize(ctx, logger)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize wallet: %w", err)
		}
		svc.Wallet = walletSvc

		// Create persistent session manager
		sessionTTL, err := time.ParseDuration(c.Auth.SessionTTL)
		if err != nil {
			logger.Warn("invalid session_ttl, using default 24h", "value", c.Auth.SessionTTL, "error", err)
			sessionTTL = 24 * time.Hour
		}
		sessionManager, err := auth.NewBadgerSessionManager(c.Auth.SessionPath, sessionTTL, logger)
		if err != nil {
			return nil, fmt.Errorf("failed to create session manager: %w", err)
		}

		// Create auth middleware
		svc.AuthMiddleware = auth.NewMiddleware(
			walletSvc.Wallet,
			sessionManager,
			logger,
			c.Auth.AllowUnauthenticated,
			c.Auth.ApiKey,
		)

		logger.Info("auth middleware initialized",
			"allowUnauthenticated", c.Auth.AllowUnauthenticated,
			"apiKeyConfigured", c.Auth.ApiKey != "",
			"sessionPath", c.Auth.SessionPath,
			"sessionTTL", sessionTTL,
		)

	}

	// Initialize Paymail service (requires OpNS + ORDFS + BroadcastHandler, optionally remote MessageBox)
	if c.runsService("paymail") && c.Paymail.Mode != paymail.ModeDisabled && c.Paymail.Mode != "" {
		paymailDeps := &paymail.InitializeDeps{}
		if svc.OPNS != nil && svc.OPNS.Lookup != nil {
			paymailDeps.OpnsLookup = svc.OPNS.Lookup
		}
		if svc.ORDFS != nil && svc.ORDFS.Ordfs != nil {
			paymailDeps.Ordfs = svc.ORDFS.Ordfs
		}
		paymailDeps.BroadcastHandler = svc.BroadcastHandler
		if svc.Beef != nil && svc.Beef.Storage != nil {
			paymailDeps.BeefStorage = svc.Beef.Storage
		}
		if c.MessageBoxURL != "" && svc.Wallet != nil {
			paymailDeps.MessageBoxClient = paymail.NewMessageBoxClient(
				c.MessageBoxURL, svc.Wallet.Wallet, logger,
			)
		}
		paymailSvc, err := c.Paymail.Initialize(ctx, logging.NewComponentLogger(logger, "paymail", ""), paymailDeps)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize paymail: %w", err)
		}
		svc.Paymail = paymailSvc
	}

	// Initialize JungleBus subscribers from per-module subscription configs
	if svc.Store != nil && svc.JungleBus != nil {
		start = time.Now()

		// BSV21 subscriber (if subscription_id configured)
		if svc.BSV21 != nil && svc.BSV21.Sync != nil && c.BSV21.Sync != nil && c.BSV21.Sync.SubscriptionID != "" {
			subCfg := c.BSV21.Sync.SubscriberConfig()
			sub, err := jbsync.NewSubscriber(subCfg, svc.Queue, svc.ConfigStore, svc.Chaintracks, svc.JungleBus, logger)
			if err != nil {
				return nil, fmt.Errorf("failed to create bsv21 subscriber: %w", err)
			}
			svc.JBSubscribers = append(svc.JBSubscribers, sub)
			logger.Info("BSV21 JungleBus subscriber initialized", "queue", "bsv21", "from_block", subCfg.FromBlock)
		}

		// BAP subscriber (if subscription_id configured)
		if svc.BAP != nil && c.BAP.Sync != nil && c.BAP.Sync.SubscriptionID != "" {
			subCfg := c.BAP.Sync.SubscriberConfig()
			sub, err := jbsync.NewSubscriber(subCfg, svc.Queue, svc.ConfigStore, svc.Chaintracks, svc.JungleBus, logger)
			if err != nil {
				return nil, fmt.Errorf("failed to create bap subscriber: %w", err)
			}
			svc.JBSubscribers = append(svc.JBSubscribers, sub)
			logger.Info("BAP JungleBus subscriber initialized", "queue", subCfg.QueueName, "from_block", subCfg.FromBlock)
		}

		// BSocial subscriber (if subscription_id configured)
		if svc.BSocial != nil && c.BSocial.Sync != nil && c.BSocial.Sync.SubscriptionID != "" {
			subCfg := c.BSocial.Sync.SubscriberConfig()
			sub, err := jbsync.NewSubscriber(subCfg, svc.Queue, svc.ConfigStore, svc.Chaintracks, svc.JungleBus, logger)
			if err != nil {
				return nil, fmt.Errorf("failed to create bsocial subscriber: %w", err)
			}
			svc.JBSubscribers = append(svc.JBSubscribers, sub)
			logger.Info("BSocial JungleBus subscriber initialized", "queue", subCfg.QueueName, "from_block", subCfg.FromBlock)
		}

		// OrdLock subscriber (if subscription_id configured)
		if svc.OrdLock != nil && c.OrdLock.Sync != nil && c.OrdLock.Sync.SubscriptionID != "" {
			subCfg := c.OrdLock.Sync.SubscriberConfig()
			sub, err := jbsync.NewSubscriber(subCfg, svc.Queue, svc.ConfigStore, svc.Chaintracks, svc.JungleBus, logger)
			if err != nil {
				return nil, fmt.Errorf("failed to create ordlock subscriber: %w", err)
			}
			svc.JBSubscribers = append(svc.JBSubscribers, sub)
			logger.Info("OrdLock JungleBus subscriber initialized", "queue", subCfg.QueueName, "from_block", subCfg.FromBlock)
		}

		// Ingest subscribers (multiple subscription_ids filling q:ingest)
		if c.runsService("index") {
			for _, subID := range c.Indexer.Sync.SubscriptionIDs {
				if subID == "" {
					continue
				}
				subCfg := &jbsync.SubscriberConfig{
					AutoStart:      true,
					SubscriptionID: subID,
					QueueName:      c.Indexer.Sync.QueueName,
					FromBlock:      c.Indexer.Sync.FromBlock,
					BatchSize:      c.Indexer.Sync.BatchSize,
					ReorgDepth:     c.Indexer.Sync.ReorgDepth,
					EnableMempool:  c.Indexer.Sync.EnableMempool,
				}
				sub, err := jbsync.NewSubscriber(subCfg, svc.Queue, svc.ConfigStore, svc.Chaintracks, svc.JungleBus, logger)
				if err != nil {
					return nil, fmt.Errorf("failed to create ingest subscriber %s: %w", subID, err)
				}
				svc.JBSubscribers = append(svc.JBSubscribers, sub)
				logger.Info("Ingest JungleBus subscriber initialized", "subscription_id", subID, "from_block", c.Indexer.Sync.FromBlock)
			}
		}

		if len(svc.JBSubscribers) > 0 {
			logger.Info("JungleBus subscribers initialized", "count", len(svc.JBSubscribers), "duration", time.Since(start).Round(time.Millisecond))
		}
	}

	logger.Info("all services initialized", "total_duration", time.Since(initStart).Round(time.Millisecond))
	return svc, nil
}

// Close closes all services
func (svc *Services) Close() error {
	var errs []error

	// Close in reverse order of initialization
	if svc.Wallet != nil {
		if err := svc.Wallet.Close(); err != nil {
			errs = append(errs, fmt.Errorf("wallet close: %w", err))
		}
	}

	if svc.Admin != nil {
		if err := svc.Admin.Close(); err != nil {
			errs = append(errs, fmt.Errorf("admin close: %w", err))
		}
	}

	if svc.Own != nil {
		if err := svc.Own.Close(); err != nil {
			errs = append(errs, fmt.Errorf("own close: %w", err))
		}
	}

	if svc.Indexer != nil {
		if err := svc.Indexer.Close(); err != nil {
			errs = append(errs, fmt.Errorf("indexer close: %w", err))
		}
	}

	if svc.StatusHandler != nil {
		svc.StatusHandler.Stop()
	}

	if svc.ORDFS != nil {
		if err := svc.ORDFS.Close(); err != nil {
			errs = append(errs, fmt.Errorf("ordfs close: %w", err))
		}
	}

	if svc.Spends != nil {
		if err := svc.Spends.Close(); err != nil {
			errs = append(errs, fmt.Errorf("spends close: %w", err))
		}
	}

	if svc.OPNS != nil {
		if err := svc.OPNS.Close(); err != nil {
			errs = append(errs, fmt.Errorf("opns close: %w", err))
		}
	}

	if svc.OrdLock != nil {
		if err := svc.OrdLock.Close(); err != nil {
			errs = append(errs, fmt.Errorf("ordlock close: %w", err))
		}
	}

	if svc.BSocial != nil {
		if err := svc.BSocial.Close(); err != nil {
			errs = append(errs, fmt.Errorf("bsocial close: %w", err))
		}
	}

	if svc.BAP != nil {
		if err := svc.BAP.Close(); err != nil {
			errs = append(errs, fmt.Errorf("bap close: %w", err))
		}
	}

	if svc.MongoDB != nil {
		if err := svc.MongoDB.Disconnect(context.Background()); err != nil {
			errs = append(errs, fmt.Errorf("mongodb close: %w", err))
		}
	}

	if svc.BSV21 != nil {
		if err := svc.BSV21.Close(); err != nil {
			errs = append(errs, fmt.Errorf("bsv21 close: %w", err))
		}
	}

	if svc.Overlay != nil {
		if err := svc.Overlay.Close(); err != nil {
			errs = append(errs, fmt.Errorf("overlay close: %w", err))
		}
	}

	if svc.TXO != nil {
		if err := svc.TXO.Close(); err != nil {
			errs = append(errs, fmt.Errorf("txo close: %w", err))
		}
	}

	// Close P2P client (also stops chaintracks via shared context)
	if svc.P2PClient != nil {
		if err := svc.P2PClient.Close(); err != nil {
			errs = append(errs, fmt.Errorf("p2p close: %w", err))
		}
	}

	if svc.Beef != nil {
		if err := svc.Beef.Close(); err != nil {
			errs = append(errs, fmt.Errorf("beef close: %w", err))
		}
	}

	if svc.PubSub != nil {
		if err := svc.PubSub.Close(); err != nil {
			errs = append(errs, fmt.Errorf("pubsub close: %w", err))
		}
	}

	// Close the queue before the main store. For provider "inherit" this is a
	// no-op; for a dedicated backend it closes the queue's own store.
	if svc.Queue != nil {
		if err := svc.Queue.Close(); err != nil {
			errs = append(errs, fmt.Errorf("queue close: %w", err))
		}
	}

	if svc.Store != nil {
		if err := svc.Store.Close(); err != nil {
			errs = append(errs, fmt.Errorf("store close: %w", err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("close errors: %v", errs)
	}
	return nil
}

// overlayLookupServices builds the topic→lookup service map for arc status
// block-height routing and rollback, covering whichever overlay modules run.
func (svc *Services) overlayLookupServices() map[string]engine.LookupService {
	lookups := make(map[string]engine.LookupService)
	if svc.BAP != nil {
		lookups["tm_bap"] = svc.BAP.Lookup
	}
	if svc.BSocial != nil {
		lookups["tm_bsocial"] = svc.BSocial.Lookup
	}
	if svc.OPNS != nil {
		lookups["tm_opns"] = svc.OPNS.Lookup
	}
	if svc.OrdLock != nil {
		lookups[ordlockpkg.TopicName] = svc.OrdLock.Lookup
	}
	if svc.BSV21 != nil {
		lookups["bsv21"] = svc.BSV21.Lookup
		lookups["tm_bsv21"] = svc.BSV21.Lookup
	}
	return lookups
}

// createOverlayP2PBus creates the overlay P2P bus from configuration.
// If walletKeyHex is provided, it's used as the libp2p identity (secp256k1).
// This makes the peer ID encode the BRC-100 wallet's public key, enabling
// BRC-42 payment derivation directly from peer IDs.
func createOverlayP2PBus(cfg overlay.P2PConfig, walletKeyHex string, q queue.Queue, logger *slog.Logger) (*overlay.P2PBus, error) {
	var privKey crypto.PrivKey
	var err error

	if walletKeyHex != "" {
		privKey, err = secp256k1KeyFromHex(walletKeyHex)
		if err != nil {
			return nil, fmt.Errorf("parse wallet key for P2P identity: %w", err)
		}
		logger.Info("using wallet identity key for overlay P2P")
	} else {
		privKey, err = loadOrGenerateP2PKey(cfg.StoragePath)
		if err != nil {
			return nil, fmt.Errorf("load P2P key: %w", err)
		}
		logger.Warn("using generated key for overlay P2P (no wallet key configured)")
	}

	client, err := msgbus.NewClient(msgbus.Config{
		Name:           "1sat-overlay",
		Logger:         p2p.NewSlogLogger(logger.With("subsystem", "overlay-p2p")),
		PrivateKey:     privKey,
		Port:           cfg.Port,
		DHTMode:        cfg.DHTMode,
		BootstrapPeers: cfg.BootstrapPeers,
		PeerCacheFile:  filepath.Join(expandHome(cfg.StoragePath), "peers.json"),
	})
	if err != nil {
		return nil, fmt.Errorf("create msgbus client: %w", err)
	}

	logger.Info("overlay P2P bus started",
		"port", cfg.Port,
		"dht_mode", cfg.DHTMode,
		"peer_id", client.GetID(),
	)

	return overlay.NewP2PBus(client, q, logger), nil
}

// secp256k1KeyFromHex converts a hex-encoded secp256k1 private key to a libp2p crypto.PrivKey.
func secp256k1KeyFromHex(keyHex string) (crypto.PrivKey, error) {
	keyBytes, err := hex.DecodeString(keyHex)
	if err != nil {
		return nil, fmt.Errorf("decode hex: %w", err)
	}
	return crypto.UnmarshalSecp256k1PrivateKey(keyBytes)
}

// loadOrGenerateP2PKey loads or generates a persistent P2P identity key.
// Fallback when no wallet key is configured.
func loadOrGenerateP2PKey(storagePath string) (crypto.PrivKey, error) {
	dir := expandHome(storagePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	keyFile := filepath.Join(dir, "identity.key")
	data, err := os.ReadFile(keyFile)
	if err == nil {
		return msgbus.PrivateKeyFromHex(string(data))
	}

	privKey, err := msgbus.GeneratePrivateKey()
	if err != nil {
		return nil, err
	}

	keyHex, err := msgbus.PrivateKeyToHex(privKey)
	if err != nil {
		return nil, err
	}

	if err := os.WriteFile(keyFile, []byte(keyHex), 0600); err != nil {
		return nil, err
	}

	return privKey, nil
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}

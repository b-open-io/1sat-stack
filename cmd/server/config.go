package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/b-open-io/1sat-stack/admin"
	"github.com/b-open-io/1sat-stack/pkg/bap"
	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/bsocial"
	"github.com/b-open-io/1sat-stack/pkg/bsv21"
	"github.com/b-open-io/1sat-stack/pkg/indexer"
	"github.com/b-open-io/1sat-stack/pkg/jbsync"
	"github.com/b-open-io/1sat-stack/pkg/logging"
	"github.com/b-open-io/1sat-stack/pkg/opns"
	"github.com/b-open-io/1sat-stack/pkg/ordfs"
	"github.com/b-open-io/1sat-stack/pkg/overlay"
	"github.com/b-open-io/1sat-stack/pkg/owner"
	"github.com/b-open-io/1sat-stack/pkg/pubsub"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/b-open-io/1sat-stack/pkg/wallet"
	"github.com/b-open-io/go-junglebus"
	arcadeconfig "github.com/bsv-blockchain/arcade/config"
	arcaderoutes "github.com/bsv-blockchain/arcade/routes/fiber"
	"github.com/bsv-blockchain/arcade/service"
	"github.com/bsv-blockchain/go-chaintracks/chaintracks"
	chaintracksconfig "github.com/bsv-blockchain/go-chaintracks/config"
	chaintracksroutes "github.com/bsv-blockchain/go-chaintracks/routes/fiber"
	"github.com/bsv-blockchain/go-sdk/transaction"
	p2p "github.com/bsv-blockchain/go-teranode-p2p-client"
	"github.com/gofiber/fiber/v2"
	"github.com/spf13/viper"
	"go.mongodb.org/mongo-driver/v2/mongo"
	mongooptions "go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Config holds the complete server configuration
type Config struct {
	// Network: "main" or "test"
	Network string `mapstructure:"network"`

	// Logging configuration
	Logging logging.Config `mapstructure:"logging"`

	// Server settings
	Server ServerConfig `mapstructure:"server"`

	// JungleBus client configuration
	JungleBus JungleBusConfig `mapstructure:"junglebus"`

	// Core services - these are the shared dependencies
	Store  store.Config  `mapstructure:"store"`
	PubSub pubsub.Config `mapstructure:"pubsub"`
	Beef   beef.Config   `mapstructure:"beef"`
	TXO    txo.Config    `mapstructure:"txo"`

	// External services
	P2P         p2p.Config               `mapstructure:"p2p"`
	Chaintracks chaintracksconfig.Config `mapstructure:"chaintracks"`
	Arcade      arcadeconfig.Config      `mapstructure:"arcade"`

	// Transaction services
	Merkle MerkleConfig `mapstructure:"merkle"`

	// Indexer service
	Indexer indexer.Config `mapstructure:"indexer"`

	// BSV21 token support
	BSV21 bsv21.Config `mapstructure:"bsv21"`

	// BAP identity overlay
	BAP bap.Config `mapstructure:"bap"`

	// BSocial overlay
	BSocial bsocial.Config `mapstructure:"bsocial"`

	// OPNS domain name overlay
	OPNS opns.Config `mapstructure:"opns"`

	// MongoDB (shared by BAP and BSocial)
	MongoDB MongoDBConfig `mapstructure:"mongodb"`

	// Overlay engine
	Overlay overlay.Config `mapstructure:"overlay"`

	// Content serving
	ORDFS ordfs.Config `mapstructure:"ordfs"`

	// Owner services
	Owner owner.Config `mapstructure:"owner"`

	// Admin UI
	Admin admin.Config `mapstructure:"admin"`

	// Wallet service
	Wallet wallet.Config `mapstructure:"wallet"`
}

// MongoDBConfig holds shared MongoDB connection configuration.
type MongoDBConfig struct {
	URL string `mapstructure:"url"`
}

// JungleBusConfig holds JungleBus client configuration
type JungleBusConfig struct {
	URL     string `mapstructure:"url"`     // Server URL
	Token   string `mapstructure:"token"`   // API token (optional)
	SSL     bool   `mapstructure:"ssl"`     // Use SSL (default: true)
	Version string `mapstructure:"version"` // API version (default: v1)
	Debug   bool   `mapstructure:"debug"`   // Enable debug logging
}

// MerkleConfig holds merkle service configuration
type MerkleConfig struct {
	Mode   string       `mapstructure:"mode"` // disabled, embedded, remote
	Routes RoutesConfig `mapstructure:"routes"`
}

// RoutesConfig holds common route configuration
type RoutesConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Prefix  string `mapstructure:"prefix"`
}

// ServerConfig holds HTTP server settings
type ServerConfig struct {
	Port      int         `mapstructure:"port"`
	Host      string      `mapstructure:"host"`
	BasePath  string      `mapstructure:"base_path"`
	BodyLimit string      `mapstructure:"body_limit"` // Max request body size (e.g., "100mb", "1gb")
	Pprof     PprofConfig `mapstructure:"pprof"`
}

// PprofConfig holds pprof profiling server settings
type PprofConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Port    int    `mapstructure:"port"`
	Host    string `mapstructure:"host"`
}

// ParseBodyLimit parses a body limit string like "100mb" or "1gb" into bytes.
// Supports: b, kb, mb, gb (case-insensitive). Defaults to 4MB if invalid.
func ParseBodyLimit(limit string) int {
	if limit == "" {
		return 4 * 1024 * 1024 // 4MB default (Fiber's default)
	}

	limit = strings.ToLower(strings.TrimSpace(limit))
	multiplier := 1

	if strings.HasSuffix(limit, "gb") {
		multiplier = 1024 * 1024 * 1024
		limit = strings.TrimSuffix(limit, "gb")
	} else if strings.HasSuffix(limit, "mb") {
		multiplier = 1024 * 1024
		limit = strings.TrimSuffix(limit, "mb")
	} else if strings.HasSuffix(limit, "kb") {
		multiplier = 1024
		limit = strings.TrimSuffix(limit, "kb")
	} else if strings.HasSuffix(limit, "b") {
		limit = strings.TrimSuffix(limit, "b")
	}

	var value int
	if _, err := fmt.Sscanf(limit, "%d", &value); err != nil {
		return 4 * 1024 * 1024 // default on parse error
	}

	return value * multiplier
}

// CreateLogger creates a logger from the logging configuration.
// If logLevelOverride is non-empty, it overrides the config level.
func (c *Config) CreateLogger(logLevelOverride string) *slog.Logger {
	// Use override if provided (from command line)
	level := c.Logging.Level
	if logLevelOverride != "" {
		level = logLevelOverride
	}

	// Update config with effective level
	c.Logging.Level = level
	c.Logging.SetDefaults()

	return logging.NewLogger(level)
}

// Services holds all initialized services
type Services struct {
	Store   *store.Services
	PubSub  *pubsub.Services
	Beef    *beef.Services
	TXO     *txo.Services
	Indexer *indexer.Services
	BSV21   *bsv21.Services
	BAP     *bap.Services
	BSocial *bsocial.Services
	OPNS    *opns.Services
	Overlay *overlay.Services
	ORDFS   *ordfs.Services
	Own     *owner.Services
	Admin   *admin.Services
	Wallet  *wallet.Services

	// MongoDB client (shared by BAP and BSocial)
	MongoDB *mongo.Client

	// JungleBus subscriptions
	JBSubscribers []*jbsync.Subscriber

	// External services
	JungleBus         *junglebus.Client
	P2PClient         *p2p.Client
	Chaintracks       chaintracks.Chaintracks
	ChaintracksRoutes *chaintracksroutes.Routes
	Arcade            *arcadeconfig.Services
	ArcadeRoutes      *arcaderoutes.Routes
}

// SetDefaults configures viper defaults for all settings
func (c *Config) SetDefaults(v *viper.Viper) {
	// Network default
	v.SetDefault("network", "main")

	// Logging defaults
	v.SetDefault("logging.level", "info")
	v.SetDefault("logging.components", map[string]string{})

	// JungleBus defaults
	v.SetDefault("junglebus.url", "https://junglebus.gorillapool.io")
	v.SetDefault("junglebus.token", "")
	v.SetDefault("junglebus.ssl", true)
	v.SetDefault("junglebus.version", "v1")
	v.SetDefault("junglebus.debug", false)

	// Server defaults
	v.SetDefault("server.port", 8080)
	v.SetDefault("server.host", "0.0.0.0")
	v.SetDefault("server.base_path", "/1sat")
	v.SetDefault("server.body_limit", "100mb") // Max request body size
	v.SetDefault("server.pprof.enabled", false)
	v.SetDefault("server.pprof.port", 6060)
	v.SetDefault("server.pprof.host", "localhost")

	// Cascade to package configs
	c.Store.SetDefaults(v, "store")
	c.PubSub.SetDefaults(v, "pubsub")
	c.Beef.SetDefaults(v, "beef")
	c.TXO.SetDefaults(v, "txo")

	// External services defaults - use library SetDefaults methods
	c.P2P.SetDefaults(v, "p2p")
	v.SetDefault("p2p.storage_path", "~/.1sat/p2p")

	c.Chaintracks.SetDefaults(v, "chaintracks")
	v.SetDefault("chaintracks.storage_path", "~/.1sat/chaintracks")

	c.Arcade.SetDefaults(v, "arcade")
	v.SetDefault("arcade.storage_path", "~/.1sat/arcade")
	v.SetDefault("arcade.database.sqlite_path", "~/.1sat/arcade/arcade.db")

	// Merkle service defaults
	v.SetDefault("merkle.mode", "disabled")
	v.SetDefault("merkle.routes.enabled", true)
	v.SetDefault("merkle.routes.prefix", "")

	// MongoDB defaults
	v.SetDefault("mongodb.url", "")

	// Package configs
	c.Indexer.SetDefaults(v, "indexer")
	c.BSV21.SetDefaults(v, "bsv21")
	c.BAP.SetDefaults(v, "bap")
	c.BSocial.SetDefaults(v, "bsocial")
	c.OPNS.SetDefaults(v, "opns")
	c.Overlay.SetDefaults(v, "overlay")
	c.ORDFS.SetDefaults(v, "ordfs")
	c.Owner.SetDefaults(v, "owner")
	c.Admin.SetDefaults(v, "admin")
	c.Wallet.SetDefaults(v, "wallet")

}

// Initialize creates all services from the configuration
func (c *Config) Initialize(ctx context.Context, logger *slog.Logger) (*Services, error) {
	if logger == nil {
		logger = slog.Default()
	}

	initStart := time.Now()
	svc := &Services{}

	// Initialize store (foundational - other services may depend on it)
	start := time.Now()
	storeSvc, err := c.Store.Initialize(ctx, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize store: %w", err)
	}
	svc.Store = storeSvc
	logger.Info("store initialized", "duration", time.Since(start).Round(time.Millisecond))

	// Initialize pubsub
	start = time.Now()
	pubsubSvc, err := c.PubSub.Initialize(ctx, logger)
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

	// Initialize beef storage (pass JungleBus client for fallback lookups)
	start = time.Now()
	beefSvc, err := c.Beef.Initialize(ctx, logger, nil, svc.JungleBus)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize beef: %w", err)
	}
	svc.Beef = beefSvc
	logger.Info("beef initialized", "duration", time.Since(start).Round(time.Millisecond))

	// Initialize P2P client (shared by chaintracks and arcade)
	if c.Chaintracks.Mode == chaintracksconfig.ModeEmbedded || c.Arcade.Mode == arcadeconfig.ModeEmbedded {
		start = time.Now()
		// Set network on P2P config
		c.P2P.Network = c.Network
		p2pClient, err := c.P2P.Initialize(ctx, "1sat-stack")
		if err != nil {
			return nil, fmt.Errorf("failed to initialize p2p client: %w", err)
		}
		svc.P2PClient = p2pClient
		logger.Info("p2p client initialized", "network", c.P2P.Network, "duration", time.Since(start).Round(time.Millisecond))
	}

	// Initialize Chaintracks
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

	// Initialize Arcade
	if c.Arcade.Mode != "" && c.Arcade.Mode != "disabled" {
		start = time.Now()
		// Set network from main config
		c.Arcade.Network = c.Network
		arcadeSvc, err := c.Arcade.Initialize(ctx, logger, svc.Chaintracks, svc.P2PClient)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize arcade: %w", err)
		}
		svc.Arcade = arcadeSvc

		// Wrap arcade service with BEEF capture (saves raw tx at submission time)
		wrappedService := NewBeefCapturingArcadeService(
			arcadeSvc.ArcadeService,
			svc.Beef.Storage,
			logger,
		)

		// Create routes with wrapped service for BEEF capture
		svc.ArcadeRoutes = arcaderoutes.NewRoutes(arcaderoutes.Config{
			Service:        wrappedService,
			Store:          arcadeSvc.Store,
			EventPublisher: arcadeSvc.EventPublisher,
			Arcade:         arcadeSvc.Arcade,
			Logger:         logger,
		})
		logger.Info("arcade initialized", "mode", c.Arcade.Mode, "duration", time.Since(start).Round(time.Millisecond))
	}

	// Initialize TXO storage with shared dependencies
	if c.TXO.Mode != txo.ModeDisabled {
		start = time.Now()
		txoSvc, err := c.TXO.Initialize(ctx, logger, storeSvc, pubsubSvc, beefSvc)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize txo: %w", err)
		}
		svc.TXO = txoSvc
		logger.Info("txo initialized", "duration", time.Since(start).Round(time.Millisecond))
	}

	// Initialize overlay engine FIRST (BSV21 needs it for topic/lookup registration)
	if c.Overlay.Mode != overlay.ModeDisabled && svc.TXO != nil {
		start = time.Now()
		overlayDeps := &overlay.InitializeDeps{
			OutputStore:  svc.TXO.OutputStore,
			ChainTracker: svc.Chaintracks,
		}
		// Add optional dependencies if available
		if svc.Store != nil {
			overlayDeps.Store = svc.Store.Store
		}
		if svc.Beef != nil {
			overlayDeps.BeefStorage = svc.Beef.Storage
		}
		overlaySvc, err := c.Overlay.Initialize(ctx, logger, overlayDeps)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize overlay: %w", err)
		}
		svc.Overlay = overlaySvc

		// Set topic whitelist/blacklist from config
		svc.Overlay.SetTopicWhitelist(c.Overlay.TopicWhitelist)
		svc.Overlay.SetTopicBlacklist(c.Overlay.TopicBlacklist)
		logger.Info("overlay initialized", "duration", time.Since(start).Round(time.Millisecond))
	}

	// Initialize BSV21 AFTER overlay so we can wire them together
	if c.BSV21.Mode != bsv21.ModeDisabled && svc.TXO != nil {
		start = time.Now()
		bsv21Svc, err := c.BSV21.Initialize(ctx, logger, svc.TXO.OutputStore, svc.Chaintracks, svc.Beef.Storage, svc.Overlay, svc.JungleBus)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize bsv21: %w", err)
		}
		svc.BSV21 = bsv21Svc

		// Wire BSV21 to overlay engine
		if svc.Overlay != nil {
			// Register BSV21 lookup service
			svc.Overlay.RegisterLookupService("bsv21", svc.BSV21.Lookup)
			logger.Info("BSV21 lookup service registered with overlay engine")

			// Activate tm_bsv21 discovery topic (admits all deploy+mint operations)
			// This is a registration-only topic - no worker needed as it just identifies outputs
			discoveryTopic := &overlay.Topic{
				Name:    "tm_bsv21",
				Manager: bsv21.NewBsv21DiscoveryTopicManager("tm_bsv21", svc.TXO.OutputStore, logger),
			}
			if err := svc.Overlay.ActivateTopic(ctx, discoveryTopic); err != nil {
				logger.Error("failed to activate BSV21 discovery topic", "error", err)
			} else {
				logger.Info("BSV21 discovery topic (tm_bsv21) activated")
			}
		}
		logger.Info("bsv21 initialized", "duration", time.Since(start).Round(time.Millisecond))
	}

	// Initialize MongoDB (shared by BAP and BSocial)
	if c.MongoDB.URL != "" && (c.BAP.Mode != bap.ModeDisabled || c.BSocial.Mode != bsocial.ModeDisabled) {
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
	if c.BAP.Mode != bap.ModeDisabled && svc.MongoDB != nil {
		start = time.Now()
		bapDB := svc.MongoDB.Database("bap")
		bapSvc, err := c.BAP.Initialize(ctx, logger, bapDB)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize bap: %w", err)
		}
		svc.BAP = bapSvc

		if svc.Overlay != nil {
			svc.Overlay.RegisterLookupService("bap", svc.BAP.Lookup)

			bapTopic := &overlay.Topic{
				Name:    "tm_bap",
				Manager: svc.BAP.TopicManager,
			}
			if err := svc.Overlay.ActivateTopic(ctx, bapTopic); err != nil {
				logger.Error("failed to activate BAP topic", "error", err)
			} else {
				logger.Info("BAP topic (tm_bap) activated")
			}

			if c.BAP.Sync != nil && c.BAP.Sync.Enabled && svc.Beef != nil {
				svc.BAP.Sync = overlay.NewOverlaySync(c.BAP.Sync, "tm_bap", svc.Store.Store, svc.Beef.Storage, svc.Overlay, logger)
			}
		}
		logger.Info("bap initialized", "duration", time.Since(start).Round(time.Millisecond))
	}

	// Initialize BSocial
	if c.BSocial.Mode != bsocial.ModeDisabled && svc.MongoDB != nil {
		start = time.Now()
		bsocialDB := svc.MongoDB.Database("bsocial")
		bsocialSvc, err := c.BSocial.Initialize(ctx, logger, bsocialDB)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize bsocial: %w", err)
		}
		svc.BSocial = bsocialSvc

		if svc.Overlay != nil {
			svc.Overlay.RegisterLookupService("bsocial", svc.BSocial.Lookup)

			bsocialTopic := &overlay.Topic{
				Name:    "tm_bsocial",
				Manager: svc.BSocial.TopicManager,
			}
			if err := svc.Overlay.ActivateTopic(ctx, bsocialTopic); err != nil {
				logger.Error("failed to activate BSocial topic", "error", err)
			} else {
				logger.Info("BSocial topic (tm_bsocial) activated")
			}

			if c.BSocial.Sync != nil && c.BSocial.Sync.Enabled && svc.Beef != nil {
				svc.BSocial.Sync = overlay.NewOverlaySync(c.BSocial.Sync, "tm_bsocial", svc.Store.Store, svc.Beef.Storage, svc.Overlay, logger)
			}
		}
		logger.Info("bsocial initialized", "duration", time.Since(start).Round(time.Millisecond))
	}

	// Initialize OPNS
	if c.OPNS.Mode != opns.ModeDisabled && svc.Store != nil {
		start = time.Now()
		opnsSvc, err := c.OPNS.Initialize(ctx, logger, svc.Store.Store)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize opns: %w", err)
		}
		svc.OPNS = opnsSvc

		if svc.Overlay != nil {
			svc.Overlay.RegisterLookupService("opns", svc.OPNS.Lookup)

			opnsTopic := &overlay.Topic{
				Name:    "tm_opns",
				Manager: svc.OPNS.TopicManager,
			}
			if err := svc.Overlay.ActivateTopic(ctx, opnsTopic); err != nil {
				logger.Error("failed to activate OPNS topic", "error", err)
			} else {
				logger.Info("OPNS topic (tm_opns) activated")
			}

			if c.OPNS.Sync != nil && c.OPNS.Sync.Enabled && svc.Beef != nil {
				svc.OPNS.Sync = overlay.NewOverlaySync(c.OPNS.Sync, "tm_opns", svc.Store.Store, svc.Beef.Storage, svc.Overlay, logger)
			}
		}
		logger.Info("opns initialized", "duration", time.Since(start).Round(time.Millisecond))
	}

	// Activate whitelisted topics after all factories are registered
	if svc.Overlay != nil {
		svc.Overlay.ActivateConfiguredTopics()
	}

	// Initialize ORDFS content serving
	if c.ORDFS.Enabled {
		start = time.Now()
		ordfsSvc, err := c.ORDFS.Initialize(ctx, logger, svc.JungleBus)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize ordfs: %w", err)
		}
		svc.ORDFS = ordfsSvc
		logger.Info("ordfs initialized", "duration", time.Since(start).Round(time.Millisecond))
	}

	// Initialize indexer services (shared IngestCtx used by owner sync and ingest sync)
	if c.Indexer.Mode != indexer.ModeDisabled && svc.TXO != nil && svc.Beef != nil {
		start = time.Now()
		indexerSvc, err := c.Indexer.Initialize(ctx, logger, &indexer.InitializeDeps{
			Store:       svc.Store.Store,
			BeefStorage: svc.Beef.Storage,
			OutputStore: svc.TXO.OutputStore,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to initialize indexer: %w", err)
		}
		svc.Indexer = indexerSvc

		// Setup arcade listener to bridge arcade events to pubsub
		if svc.Arcade != nil && svc.PubSub != nil {
			svc.Indexer.SetupArcadeListener(&indexer.ArcadeListenerDeps{
				EventPublisher: svc.Arcade.EventPublisher,
				PubSub:         svc.PubSub.PubSub,
			})
		}

		// Setup status handler to process all arc events (from arcade or webhooks)
		if svc.PubSub != nil {
			svc.Indexer.SetupStatusHandler(&indexer.StatusHandlerDeps{
				PubSub:       svc.PubSub.PubSub,
				ChainTracker: svc.Chaintracks,
			})
		}

		// Setup pending auditor to verify proofs on each new block
		if svc.Chaintracks != nil {
			var arcadeService service.ArcadeService
			if svc.Arcade != nil {
				arcadeService = svc.Arcade.ArcadeService
			}
			svc.Indexer.SetupPendingAuditor(&indexer.PendingAuditorDeps{
				Chaintracks:   svc.Chaintracks,
				ArcadeService: arcadeService,
			})
		}

		// Setup routes for webhook callbacks
		if svc.PubSub != nil {
			svc.Indexer.SetupRoutes(svc.PubSub.PubSub)
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

	// Initialize owner services (depends on TXO, Beef, Indexer)
	if c.Owner.Mode != owner.ModeDisabled && svc.TXO != nil && svc.Beef != nil && svc.Indexer != nil {
		start = time.Now()
		ownSvc, err := c.Owner.Initialize(ctx, logger, &owner.InitializeDeps{
			JungleBus:   svc.JungleBus,
			BeefStorage: svc.Beef.Storage,
			Indexer:     svc.Indexer.Indexer, // Use shared IngestCtx from indexer services
			OutputStore: svc.TXO.OutputStore,
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
		adminDeps := &admin.InitializeDeps{
			Overlay: svc.Overlay,
			Store:   svc.Store.Store,
		}
		// Pass BSV21 sync services if available
		if svc.BSV21 != nil {
			adminDeps.BSV21Sync = svc.BSV21.Sync
		}
		adminSvc, err := c.Admin.Initialize(ctx, logger, adminDeps)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize admin: %w", err)
		}
		svc.Admin = adminSvc
		logger.Info("admin initialized", "mode", c.Admin.Mode, "duration", time.Since(start).Round(time.Millisecond))
	}

	// Initialize Wallet service
	if c.Wallet.Mode != wallet.ModeDisabled {
		walletDeps := &wallet.InitializeDeps{
			Network: c.Network,
			Arcade:  svc.Arcade,
		}
		walletSvc, err := c.Wallet.Initialize(ctx, logger, walletDeps)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize wallet: %w", err)
		}
		svc.Wallet = walletSvc
	}

	// Initialize JungleBus subscribers from per-module subscription configs
	if svc.Store != nil && svc.JungleBus != nil {
		start = time.Now()

		// BSV21 subscriber (if subscription_id configured)
		if svc.BSV21 != nil && svc.BSV21.Sync != nil && c.BSV21.Sync != nil && c.BSV21.Sync.SubscriptionID != "" {
			subCfg := c.BSV21.Sync.SubscriberConfig()
			sub, err := jbsync.NewSubscriber(subCfg, svc.Store.Store, svc.Chaintracks, svc.JungleBus, logger)
			if err != nil {
				return nil, fmt.Errorf("failed to create bsv21 subscriber: %w", err)
			}
			svc.JBSubscribers = append(svc.JBSubscribers, sub)
			logger.Info("BSV21 JungleBus subscriber initialized", "queue", "bsv21", "from_block", subCfg.FromBlock)
		}

		// BAP subscriber (if subscription_id configured)
		if svc.BAP != nil && c.BAP.Sync != nil && c.BAP.Sync.SubscriptionID != "" {
			subCfg := c.BAP.Sync.SubscriberConfig()
			sub, err := jbsync.NewSubscriber(subCfg, svc.Store.Store, svc.Chaintracks, svc.JungleBus, logger)
			if err != nil {
				return nil, fmt.Errorf("failed to create bap subscriber: %w", err)
			}
			svc.JBSubscribers = append(svc.JBSubscribers, sub)
			logger.Info("BAP JungleBus subscriber initialized", "queue", subCfg.QueueName, "from_block", subCfg.FromBlock)
		}

		// BSocial subscriber (if subscription_id configured)
		if svc.BSocial != nil && c.BSocial.Sync != nil && c.BSocial.Sync.SubscriptionID != "" {
			subCfg := c.BSocial.Sync.SubscriberConfig()
			sub, err := jbsync.NewSubscriber(subCfg, svc.Store.Store, svc.Chaintracks, svc.JungleBus, logger)
			if err != nil {
				return nil, fmt.Errorf("failed to create bsocial subscriber: %w", err)
			}
			svc.JBSubscribers = append(svc.JBSubscribers, sub)
			logger.Info("BSocial JungleBus subscriber initialized", "queue", subCfg.QueueName, "from_block", subCfg.FromBlock)
		}

		// OPNS subscriber (if subscription_id configured)
		if svc.OPNS != nil && c.OPNS.Sync != nil && c.OPNS.Sync.SubscriptionID != "" {
			subCfg := c.OPNS.Sync.SubscriberConfig()
			sub, err := jbsync.NewSubscriber(subCfg, svc.Store.Store, svc.Chaintracks, svc.JungleBus, logger)
			if err != nil {
				return nil, fmt.Errorf("failed to create opns subscriber: %w", err)
			}
			svc.JBSubscribers = append(svc.JBSubscribers, sub)
			logger.Info("OPNS JungleBus subscriber initialized", "queue", subCfg.QueueName, "from_block", subCfg.FromBlock)
		}

		// Ingest subscribers (multiple subscription_ids filling q:ingest)
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
			sub, err := jbsync.NewSubscriber(subCfg, svc.Store.Store, svc.Chaintracks, svc.JungleBus, logger)
			if err != nil {
				return nil, fmt.Errorf("failed to create ingest subscriber %s: %w", subID, err)
			}
			svc.JBSubscribers = append(svc.JBSubscribers, sub)
			logger.Info("Ingest JungleBus subscriber initialized", "subscription_id", subID, "from_block", c.Indexer.Sync.FromBlock)
		}

		if len(svc.JBSubscribers) > 0 {
			logger.Info("JungleBus subscribers initialized", "count", len(svc.JBSubscribers), "duration", time.Since(start).Round(time.Millisecond))
		}
	}

	logger.Info("all services initialized", "total_duration", time.Since(initStart).Round(time.Millisecond))
	return svc, nil
}

// RegisterRoutes registers all HTTP routes on the Fiber app
func (c *Config) RegisterRoutes(app *fiber.App, svc *Services) {
	// Create API group with base path
	api := app.Group(c.Server.BasePath)

	slog.Debug("registering routes", "basePath", c.Server.BasePath)

	// Track enabled capabilities as routes are registered
	capabilities := []string{}

	// Register beef routes
	if svc.Beef != nil && svc.Beef.Routes != nil {
		beefGroup := api.Group("/beef")
		svc.Beef.Routes.Register(beefGroup)
		capabilities = append(capabilities, "beef")
	}

	// Register pubsub/SSE routes
	if svc.PubSub != nil && svc.PubSub.Routes != nil {
		sseGroup := api.Group("/sse")
		svc.PubSub.Routes.Register(sseGroup)
		capabilities = append(capabilities, "pubsub")
	}

	// Register TXO routes
	if svc.TXO != nil && svc.TXO.Routes != nil {
		txoGroup := api.Group("/txo")
		svc.TXO.Routes.Register(txoGroup)
		capabilities = append(capabilities, "txo")
	}

	// Register owner routes
	if svc.Own != nil && svc.Own.Routes != nil {
		prefix := c.Owner.Routes.Prefix
		if prefix == "" {
			prefix = "/owner"
		}
		ownGroup := api.Group(prefix)
		svc.Own.Routes.Register(ownGroup)
		capabilities = append(capabilities, "owner")
		slog.Debug("registered owner routes", "prefix", prefix)
	} else {
		slog.Debug("owner routes not registered", "ownNil", svc.Own == nil, "ownMode", c.Owner.Mode)
	}

	// Register BSV21 routes
	if svc.BSV21 != nil && svc.BSV21.Routes != nil {
		prefix := c.BSV21.Routes.Prefix
		if prefix == "" {
			prefix = "/bsv21"
		}
		bsv21Group := api.Group(prefix)
		svc.BSV21.Routes.Register(bsv21Group)

		capabilities = append(capabilities, "bsv21")
	}

	// Register BAP routes
	if svc.BAP != nil && svc.BAP.Routes != nil {
		prefix := c.BAP.Routes.Prefix
		if prefix == "" {
			prefix = "/bap"
		}
		bapGroup := api.Group(prefix)
		svc.BAP.Routes.Register(bapGroup)
		capabilities = append(capabilities, "bap")
	}

	// Register BSocial routes
	if svc.BSocial != nil && svc.BSocial.Routes != nil {
		prefix := c.BSocial.Routes.Prefix
		if prefix == "" {
			prefix = "/bsocial"
		}
		bsocialGroup := api.Group(prefix)
		svc.BSocial.Routes.Register(bsocialGroup)
		capabilities = append(capabilities, "bsocial")
	}

	// Register OPNS routes
	if svc.OPNS != nil && svc.OPNS.Routes != nil {
		prefix := c.OPNS.Routes.Prefix
		if prefix == "" {
			prefix = "/opns"
		}
		opnsGroup := api.Group(prefix)
		svc.OPNS.Routes.Register(opnsGroup)
		capabilities = append(capabilities, "opns")
	}

	// Register overlay routes
	if svc.Overlay != nil && svc.Overlay.Routes != nil {
		prefix := c.Overlay.Routes.Prefix
		if prefix == "" {
			prefix = "/overlay"
		}
		overlayGroup := api.Group(prefix)
		svc.Overlay.Routes.Register(overlayGroup)
		capabilities = append(capabilities, "overlay")
	}

	// Register ORDFS routes
	if svc.ORDFS != nil && svc.ORDFS.Routes != nil {
		prefix := c.ORDFS.Routes.Prefix
		if prefix == "" {
			prefix = "/ordfs"
		}
		ordfsGroup := api.Group(prefix)
		svc.ORDFS.Routes.Register(ordfsGroup)
		capabilities = append(capabilities, "ordfs")

		// Also register content at root level for compatibility with ordfs protocol
		contentGroup := app.Group("/content")
		svc.ORDFS.Routes.RegisterContent(contentGroup)
	}

	// Register Chaintracks routes (block headers, chain tip, etc.)
	if svc.ChaintracksRoutes != nil {
		blockGroup := api.Group("/chaintracks")
		svc.ChaintracksRoutes.Register(blockGroup)
		capabilities = append(capabilities, "chaintracks")
		slog.Debug("registered chaintracks routes", "prefix", "/chaintracks")
	}

	// Register Arcade routes (transaction broadcast, status)
	if svc.ArcadeRoutes != nil {
		arcGroup := api.Group("/arcade")
		svc.ArcadeRoutes.Register(arcGroup)
		capabilities = append(capabilities, "arcade")
		slog.Debug("registered arcade routes", "prefix", "/arcade")
	}

	// Register Arc callback route (for webhook callbacks from broadcasters)
	if svc.Indexer != nil && svc.Indexer.Routes != nil {
		arcGroup := api.Group("/arc")
		svc.Indexer.Routes.RegisterCallback(arcGroup)
		slog.Debug("registered arc callback routes", "prefix", "/arc")
	}

	// Register Admin routes
	if svc.Admin != nil && svc.Admin.Routes != nil {
		prefix := c.Admin.Routes.Prefix
		if prefix == "" {
			prefix = "/admin"
		}
		adminGroup := api.Group(prefix)
		svc.Admin.Routes.Register(adminGroup)
		capabilities = append(capabilities, "admin")
		slog.Debug("registered admin routes", "prefix", prefix)
	}

	// Register Wallet routes
	if svc.Wallet != nil && svc.Wallet.Routes != nil {
		prefix := c.Wallet.Routes.Prefix
		if prefix == "" {
			prefix = "/wallet"
		}
		walletGroup := api.Group(prefix)
		svc.Wallet.Routes.Register(walletGroup)
		capabilities = append(capabilities, "wallet")
		slog.Debug("registered wallet routes", "prefix", c.Server.BasePath+prefix)

		// Also register at /.well-known/auth for BRC-100 authentication handshake
		// The wallet middleware expects this route at the root level
		svc.Wallet.Routes.RegisterWellKnown(app)
		slog.Debug("registered wallet .well-known/auth route")
	}

	// Health check endpoint
	api.Get("/health", handleHealth)

	// Capabilities endpoint - returns list of enabled services
	api.Get("/capabilities", handleCapabilities(capabilities))

	// Setup API documentation routes
	registerDocsRoutes(app)
}

// handleHealth returns the health status
// @Summary Health check
// @Description Returns the health status of the service
// @Tags system
// @Produce json
// @Success 200 {object} map[string]string "status: ok"
// @Router /health [get]
func handleHealth(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"status": "ok",
	})
}

// handleCapabilities returns the list of enabled capabilities
// @Summary Get capabilities
// @Description Returns the list of enabled service capabilities
// @Tags system
// @Produce json
// @Success 200 {array} string "List of enabled capabilities"
// @Router /capabilities [get]
func handleCapabilities(capabilities []string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		return c.JSON(capabilities)
	}
}

// registerDocsRoutes sets up Swagger/Scalar API documentation
func registerDocsRoutes(app *fiber.App) {
	// Get current working directory
	cwd, _ := os.Getwd()

	// Try to find docs folder in multiple locations
	possiblePaths := []string{
		"./docs",
		"../../docs",
		filepath.Join(cwd, "docs"),
	}

	var docsPath string
	for _, p := range possiblePaths {
		absPath, _ := filepath.Abs(p)
		swaggerPath := filepath.Join(absPath, "swagger.json")
		if _, err := os.Stat(swaggerPath); err == nil {
			docsPath = absPath
			slog.Info("found swagger.json", "path", swaggerPath)
			break
		}
	}

	if docsPath == "" {
		slog.Warn("swagger.json not found", "searchPaths", possiblePaths)
		docsPath = "./docs" // fallback
	}

	// Serve swagger files at /api-spec
	app.Static("/api-spec", docsPath)

	// Serve Scalar API reference UI at /1sat/docs
	app.Get("/1sat/docs", func(c *fiber.Ctx) error {
		html := `<!doctype html>
<html>
<head>
    <title>1Sat Stack API</title>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
</head>
<body>
    <script id="api-reference" data-url="/api-spec/swagger.json" data-configuration='{"defaultOpenAllTags": false}'></script>
    <script src="https://cdn.jsdelivr.net/npm/@scalar/api-reference"></script>
</body>
</html>`
		c.Set("Content-Type", "text/html")
		return c.SendString(html)
	})
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

	if svc.ORDFS != nil {
		if err := svc.ORDFS.Close(); err != nil {
			errs = append(errs, fmt.Errorf("ordfs close: %w", err))
		}
	}

	if svc.OPNS != nil {
		if err := svc.OPNS.Close(); err != nil {
			errs = append(errs, fmt.Errorf("opns close: %w", err))
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

	// Close Arcade (depends on chaintracks and P2P)
	if svc.Arcade != nil {
		if err := svc.Arcade.Close(); err != nil {
			errs = append(errs, fmt.Errorf("arcade close: %w", err))
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

// StartSubscribers starts all JungleBus subscribers in background goroutines.
// The subscribers will run until the context is cancelled.
func (svc *Services) StartSubscribers(ctx context.Context, logger *slog.Logger) {
	for _, sub := range svc.JBSubscribers {
		go func(s *jbsync.Subscriber) {
			if err := s.Start(ctx); err != nil {
				logger.Error("JungleBus subscriber error", "error", err)
			}
		}(sub)
	}
	if len(svc.JBSubscribers) > 0 {
		logger.Info("started JungleBus subscribers", "count", len(svc.JBSubscribers))
	}

	// Start BSV21 sync services
	if svc.BSV21 != nil && svc.BSV21.Sync != nil {
		go func() {
			if err := svc.BSV21.Sync.Start(ctx); err != nil {
				logger.Error("BSV21 sync error", "error", err)
			}
		}()
		logger.Info("started BSV21 sync services")
	}

	// Start overlay sync workers (BAP, BSocial, OPNS)
	if svc.BAP != nil && svc.BAP.Sync != nil {
		go func() {
			if err := svc.BAP.Sync.Start(ctx); err != nil {
				logger.Error("BAP sync error", "error", err)
			}
		}()
		logger.Info("started BAP overlay sync")
	}
	if svc.BSocial != nil && svc.BSocial.Sync != nil {
		go func() {
			if err := svc.BSocial.Sync.Start(ctx); err != nil {
				logger.Error("BSocial sync error", "error", err)
			}
		}()
		logger.Info("started BSocial overlay sync")
	}
	if svc.OPNS != nil && svc.OPNS.Sync != nil {
		go func() {
			if err := svc.OPNS.Sync.Start(ctx); err != nil {
				logger.Error("OPNS sync error", "error", err)
			}
		}()
		logger.Info("started OPNS overlay sync")
	}

	// Start arcade event handlers (arcade listener + status handler)
	if svc.Indexer != nil {
		if err := svc.Indexer.StartEventHandlers(ctx); err != nil {
			logger.Error("Failed to start event handlers", "error", err)
		} else {
			logger.Info("started arcade event handlers")
		}
	}

	// Start JungleBus sync (if configured)
	if svc.Indexer != nil && svc.Indexer.Sync != nil {
		go func() {
			if err := svc.Indexer.Start(ctx); err != nil {
				logger.Error("Indexer sync error", "error", err)
			}
		}()
		logger.Info("started indexer sync")
	}
}

// LoadConfig loads configuration from file and environment.
// Configuration is loaded from YAML files in order of precedence:
// 1. Explicit configPath argument (if provided)
// 2. ./config.yaml
// 3. ~/.1sat/config.yaml
// 4. /etc/1sat/config.yaml
// Environment variables with prefix ONESAT_ override config file values.
func LoadConfig(configPath string) (*Config, error) {
	v := viper.New()

	// Set defaults
	cfg := &Config{}
	cfg.SetDefaults(v)

	// Configure viper
	v.SetConfigType("yaml")
	v.SetConfigName("config")
	v.SetEnvPrefix("ONESAT")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Manually read indexer sync subscription_ids from env var (Viper doesn't reliably
	// parse comma-separated env vars into string slices)
	if ids := os.Getenv("ONESAT_INDEXER_SYNC_SUBSCRIPTION_IDS"); ids != "" {
		v.SetDefault("indexer.sync.subscription_ids", strings.Split(ids, ","))
	}

	// Load config file
	if configPath != "" {
		// Explicit path provided
		v.SetConfigFile(configPath)
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
	} else {
		// Search in standard locations (order of precedence)
		v.AddConfigPath(".")           // Current directory
		v.AddConfigPath("$HOME/.1sat") // User home directory
		v.AddConfigPath("/etc/1sat")   // System directory

		// Attempt to read config, ignore if not found
		if err := v.ReadInConfig(); err != nil {
			if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
				return nil, fmt.Errorf("failed to read config file: %w", err)
			}
			// Config file not found - use defaults
		}
	}

	// Unmarshal config
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return cfg, nil
}

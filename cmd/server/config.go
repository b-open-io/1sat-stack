package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/b-open-io/1sat-stack/admin"
	"github.com/b-open-io/1sat-stack/pkg/auth"
	"github.com/b-open-io/1sat-stack/pkg/bap"
	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/bsocial"
	"github.com/b-open-io/1sat-stack/pkg/bsv21"
	"github.com/b-open-io/1sat-stack/pkg/indexer"
	"github.com/b-open-io/1sat-stack/pkg/jbsync"
	"github.com/b-open-io/1sat-stack/pkg/logging"
	"github.com/b-open-io/1sat-stack/pkg/opns"
	"github.com/b-open-io/1sat-stack/pkg/ordfs"
	ordlockpkg "github.com/b-open-io/1sat-stack/pkg/ordlock"
	"github.com/b-open-io/1sat-stack/pkg/overlay"
	overlaystorage "github.com/b-open-io/1sat-stack/pkg/overlay/storage"
	"github.com/b-open-io/1sat-stack/pkg/owner"
	"github.com/b-open-io/1sat-stack/pkg/messagebox"
	"github.com/b-open-io/1sat-stack/pkg/paymail"
	"github.com/b-open-io/1sat-stack/pkg/pubsub"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/b-open-io/1sat-stack/pkg/wallet"
	"github.com/b-open-io/go-junglebus"
	arcadeconfig "github.com/bsv-blockchain/arcade/config"
	arcaderoutes "github.com/bsv-blockchain/arcade/routes/fiber"
	"github.com/bsv-blockchain/arcade/service"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-chaintracks/chaintracks"
	chaintracksconfig "github.com/bsv-blockchain/go-chaintracks/config"
	chaintracksroutes "github.com/bsv-blockchain/go-chaintracks/routes/fiber"
	msgbus "github.com/bsv-blockchain/go-p2p-message-bus"
	"github.com/bsv-blockchain/go-sdk/transaction"
	p2p "github.com/bsv-blockchain/go-teranode-p2p-client"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/adaptor"
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

	// OrdLock marketplace overlay
	OrdLock ordlockpkg.Config `mapstructure:"ordlock"`

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

	// Auth middleware
	Auth auth.Config `mapstructure:"auth"`

	// Paymail service
	Paymail paymail.Config `mapstructure:"paymail"`

	// Message box service
	MessageBox messagebox.Config `mapstructure:"messagebox"`
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
	OrdLock *ordlockpkg.Services
	Overlay *overlay.Services
	ORDFS   *ordfs.Services
	Own     *owner.Services
	Admin   *admin.Services
	Wallet  *wallet.Services
	Paymail    *paymail.Services
	MessageBox *messagebox.Services

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
	Arcade            *arcadeconfig.Services
	ArcadeWrapped     service.ArcadeService
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
	c.OrdLock.SetDefaults(v, "ordlock")
	c.Overlay.SetDefaults(v, "overlay")
	c.ORDFS.SetDefaults(v, "ordfs")
	c.Owner.SetDefaults(v, "owner")
	c.Admin.SetDefaults(v, "admin")
	c.Wallet.SetDefaults(v, "wallet")
	c.Auth.SetDefaults(v, "auth")
	c.Paymail.SetDefaults(v, "paymail")
	c.MessageBox.SetDefaults(v, "messagebox")
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
	beefSvc, err := c.Beef.Initialize(ctx, logger, svc.Chaintracks, svc.JungleBus)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize beef: %w", err)
	}
	svc.Beef = beefSvc
	logger.Info("beef initialized", "duration", time.Since(start).Round(time.Millisecond))

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

		svc.ArcadeWrapped = wrappedService

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
		// Create overlay P2P bus if enabled
		if c.Overlay.P2P.Enabled && svc.Store != nil {
			p2pBus, err := createOverlayP2PBus(c.Overlay.P2P, c.Wallet.ServerPrivateKey, svc.Store.Store, logger)
			if err != nil {
				return nil, fmt.Errorf("failed to create overlay P2P bus: %w", err)
			}
			overlayDeps.P2PBus = p2pBus
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
		var topicDB overlaystorage.Factory
		if svc.Overlay != nil {
			topicDB = svc.Overlay.TopicDBFactory()
		}
		bsv21Svc, err := c.BSV21.Initialize(ctx, logger, svc.TXO.OutputStore, topicDB, svc.Chaintracks, svc.Beef.Storage, svc.Overlay, svc.JungleBus)
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
				Manager: bsv21.NewBsv21DiscoveryTopicManager("tm_bsv21", logger),
			}
			if err := svc.Overlay.ActivateTopic(ctx, discoveryTopic); err != nil {
				logger.Error("failed to activate BSV21 discovery topic", "error", err)
			} else {
				logger.Info("BSV21 discovery topic (tm_bsv21) activated")
			}
		}
		logger.Info("bsv21 initialized", "duration", time.Since(start).Round(time.Millisecond))
	}

	// Initialize MongoDB (used by BSocial)
	if c.MongoDB.URL != "" && c.BSocial.Mode != bsocial.ModeDisabled {
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
	if c.BAP.Mode != bap.ModeDisabled && svc.Overlay != nil {
		start = time.Now()
		bapSvc, err := c.BAP.Initialize(ctx, logger, svc.Overlay.TopicDBFactory())
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

			if svc.Beef != nil {
				syncCfg := c.BAP.Sync
				if syncCfg == nil {
					syncCfg = &overlay.OverlaySyncConfig{}
				}
				if syncCfg.QueueName == "" {
					syncCfg.QueueName = "bap"
				}
				svc.BAP.Sync = overlay.NewOverlaySync(syncCfg, "tm_bap", svc.Store.Store, svc.Beef.Storage, svc.Overlay, logger)
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

			if svc.Beef != nil {
				syncCfg := c.BSocial.Sync
				if syncCfg == nil {
					syncCfg = &overlay.OverlaySyncConfig{}
				}
				if syncCfg.QueueName == "" {
					syncCfg.QueueName = "bsocial"
				}
				svc.BSocial.Sync = overlay.NewOverlaySync(syncCfg, "tm_bsocial", svc.Store.Store, svc.Beef.Storage, svc.Overlay, logger)
			}
		}
		logger.Info("bsocial initialized", "duration", time.Since(start).Round(time.Millisecond))
	}

	// Initialize OPNS
	if c.OPNS.Mode != opns.ModeDisabled && svc.Overlay != nil {
		start = time.Now()
		opnsDB, err := svc.Overlay.TopicDB("tm_opns")
		if err != nil {
			return nil, fmt.Errorf("failed to get opns topic db: %w", err)
		}
		opnsSvc, err := c.OPNS.Initialize(ctx, logger, opnsDB)
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

			if c.OPNS.Crawl.Enabled && svc.Beef != nil {
				c.OPNS.Crawl.JungleBusURL = c.JungleBus.URL
				svc.OPNS.Crawl = opns.NewGenesisCrawl(c.OPNS.Crawl, svc.Beef.Storage, svc.Overlay, logger)
			}

			if svc.Beef != nil {
				opnsSyncCfg := &overlay.OverlaySyncConfig{
					QueueName:           "opns",
					ResolveDependencies: true,
				}
				svc.OPNS.Sync = overlay.NewOverlaySync(opnsSyncCfg, "tm_opns", svc.Store.Store, svc.Beef.Storage, svc.Overlay, logger)
			}
		}
		logger.Info("opns initialized", "duration", time.Since(start).Round(time.Millisecond))
	}

	// Initialize OrdLock
	if c.OrdLock.Mode != ordlockpkg.ModeDisabled && svc.Beef != nil {
		start = time.Now()
		var ps pubsub.PubSub
		if svc.PubSub != nil {
			ps = svc.PubSub.PubSub
		}
		ordlockSvc, err := c.OrdLock.Initialize(ctx, logger, svc.Beef.Storage, svc.Store.Store, ps)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize ordlock: %w", err)
		}
		svc.OrdLock = ordlockSvc
		logger.Info("ordlock initialized", "duration", time.Since(start).Round(time.Millisecond))
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

		// Wire ORDFS into OrdLock for origin resolution on transferred ordinals
		if svc.OrdLock != nil && svc.OrdLock.OrdLock != nil {
			svc.OrdLock.OrdLock.SetOrdfs(ordfsSvc.Ordfs)
		}

		logger.Info("ordfs initialized", "duration", time.Since(start).Round(time.Millisecond))
	}

	// Initialize indexer services (shared IngestCtx used by owner sync and ingest sync)
	if c.Indexer.Mode != indexer.ModeDisabled && svc.TXO != nil && svc.Beef != nil {
		start = time.Now()
		indexerDeps := &indexer.InitializeDeps{
			Store:       svc.Store.Store,
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

		// Setup arcade listener to bridge arcade events to pubsub
		if svc.Arcade != nil && svc.PubSub != nil {
			svc.Indexer.SetupArcadeListener(&indexer.ArcadeListenerDeps{
				EventPublisher: svc.Arcade.EventPublisher,
				PubSub:         svc.PubSub.PubSub,
			})
		}

		// Setup status handler to process all arc events (from arcade or webhooks)
		if svc.PubSub != nil {
			var overlayStorage engine.Storage
			if svc.Overlay != nil && svc.Overlay.Engine != nil {
				overlayStorage = svc.Overlay.Engine.Storage
			}
			svc.Indexer.SetupStatusHandler(&indexer.StatusHandlerDeps{
				PubSub:         svc.PubSub.PubSub,
				ChainTracker:   svc.Chaintracks,
				OverlayStorage: overlayStorage,
			})
		}

		// Setup pending auditor to verify proofs on each new block
		if svc.Chaintracks != nil {
			var arcadeService service.ArcadeService
			if svc.ArcadeWrapped != nil {
				arcadeService = svc.ArcadeWrapped
			} else if svc.Arcade != nil {
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
		// Wire OpNS crawl trigger if dependencies are available
		if svc.OPNS != nil && svc.Beef != nil && svc.Overlay != nil {
			adminDeps.TriggerOpnsCrawl = func(triggerCtx context.Context) error {
				if svc.OPNS.Crawl != nil {
					return fmt.Errorf("crawl already running")
				}
				crawlCfg := c.OPNS.Crawl
				crawlCfg.Enabled = true
				crawlCfg.JungleBusURL = c.JungleBus.URL
				svc.OPNS.Crawl = opns.NewGenesisCrawl(crawlCfg, svc.Beef.Storage, svc.Overlay, logger)
				go func() {
					if err := svc.OPNS.Crawl.Start(ctx); err != nil {
						logger.Error("OpNS crawl error", "error", err)
					}
					svc.OPNS.Crawl = nil
				}()
				return nil
			}
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
		}
		if svc.Chaintracks != nil {
			walletDeps.Chaintracks = svc.Chaintracks
		}
		if svc.ArcadeWrapped != nil {
			walletDeps.Arcade = svc.ArcadeWrapped
		} else if svc.Arcade != nil && svc.Arcade.ArcadeService != nil {
			walletDeps.Arcade = svc.Arcade.ArcadeService
		}
		if svc.Beef != nil && svc.Beef.Storage != nil {
			walletDeps.BeefStorage = svc.Beef.Storage
		}
		walletSvc, err := c.Wallet.Initialize(ctx, logger, walletDeps)
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

		// Create auth middleware (requires authentication)
		svc.AuthMiddleware = auth.NewMiddleware(
			walletSvc.Wallet,
			sessionManager,
			logger,
			false, // AllowUnauthenticated = false
			c.Auth.ApiKey,
		)

		logger.Info("auth middleware initialized",
			"allowUnauthenticated", c.Auth.AllowUnauthenticated,
			"apiKeyConfigured", c.Auth.ApiKey != "",
			"sessionPath", c.Auth.SessionPath,
			"sessionTTL", sessionTTL,
		)

	}

	// Initialize MessageBox service (must be before Paymail, which depends on it)
	if c.MessageBox.Mode != messagebox.ModeDisabled {
		mbSvc, err := c.MessageBox.Initialize(ctx, logger)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize messagebox: %w", err)
		}
		svc.MessageBox = mbSvc
	}

	// Initialize Paymail service (requires MessageBox + OpNS + ORDFS + Arcade)
	if c.Paymail.Mode != paymail.ModeDisabled {
		paymailDeps := &paymail.InitializeDeps{}
		if svc.OPNS != nil && svc.OPNS.Lookup != nil {
			paymailDeps.OpnsLookup = svc.OPNS.Lookup
		}
		if svc.ORDFS != nil && svc.ORDFS.Ordfs != nil {
			paymailDeps.Ordfs = svc.ORDFS.Ordfs
		}
		if svc.ArcadeWrapped != nil {
			paymailDeps.Arcade = svc.ArcadeWrapped
		} else if svc.Arcade != nil && svc.Arcade.ArcadeService != nil {
			paymailDeps.Arcade = svc.Arcade.ArcadeService
		}
		if svc.MessageBox != nil && svc.MessageBox.DB != nil {
			paymailDeps.MessageBoxDB = svc.MessageBox.DB
		}
		if svc.Beef != nil && svc.Beef.Storage != nil {
			paymailDeps.BeefStorage = svc.Beef.Storage
		}
		paymailSvc, err := c.Paymail.Initialize(ctx, logger, paymailDeps)
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

	// Register OrdLock routes
	if svc.OrdLock != nil && svc.OrdLock.Routes != nil {
		prefix := c.OrdLock.Routes.Prefix
		if prefix == "" {
			prefix = "/market"
		}
		ordlockGroup := api.Group(prefix)
		svc.OrdLock.Routes.Register(ordlockGroup)
		capabilities = append(capabilities, "market")
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

	// Register Admin routes: static UI files are public, API endpoints are guarded.
	// Setup routes (status, setup) need identity but not AdminGuard.
	// API routes are mounted under {prefix}/api/ with AdminGuard middleware.
	// Static UI files are mounted directly at {prefix}/ without auth so the
	// browser can load the app before performing the BRC-103/104 handshake.
	if svc.Admin != nil && svc.Admin.Routes != nil && svc.AuthMiddleware != nil {
		prefix := c.Admin.Routes.Prefix
		if prefix == "" {
			prefix = "/admin"
		}
		guardedGroup := api.Group(prefix+"/api",
			svc.AuthMiddleware.Handler(),
			auth.AdminGuard(svc.Store.Store, slog.Default()),
		)
		publicGroup := api.Group(prefix)
		svc.Admin.Routes.Register(guardedGroup, publicGroup, svc.AuthMiddleware.Handler())
		capabilities = append(capabilities, "admin")
		slog.Debug("registered admin routes", "prefix", prefix)
	}

	// Register Wallet routes (with auth required)
	if svc.Wallet != nil && svc.Wallet.Routes != nil {
		prefix := c.Wallet.Routes.Prefix
		if prefix == "" {
			prefix = "/wallet"
		}
		// Compose auth middleware with wallet handler at HTTP layer,
		// then adapt to Fiber once. This ensures auth context flows correctly
		// to the RPC handler without context conversion issues.
		walletHandler := svc.Wallet.Routes.Handler()
		authWrappedHandler := svc.AuthMiddleware.HTTPHandler(walletHandler)

		// Register wallet routes with auth-wrapped handler
		api.Group(prefix).All("/", adaptor.HTTPHandler(authWrappedHandler))

		// Register /.well-known/auth at app root for BRC-103/104 handshake
		// (auth middleware handles the handshake internally)
		app.All("/.well-known/auth", adaptor.HTTPHandler(authWrappedHandler))

		// Serve /manifest.json for WalletPermissionsManager grouped permission flow.
		// Declares protocol permissions so dApps get a single grouped prompt.
		app.Get("/manifest.json", handleManifest())

		capabilities = append(capabilities, "wallet")
		slog.Debug("registered wallet routes", "prefix", c.Server.BasePath+prefix)
	}

	// Register Paymail routes
	if svc.Paymail != nil && svc.Paymail.Routes != nil {
		prefix := c.Paymail.Routes.Prefix
		if prefix == "" {
			prefix = "/bsvalias"
		}
		fullPrefix := c.Server.BasePath + prefix
		svc.Paymail.Routes.SetPathPrefix(fullPrefix)
		paymailGroup := api.Group(prefix)
		svc.Paymail.Routes.Register(paymailGroup)
		capabilities = append(capabilities, "paymail")
		slog.Debug("registered paymail routes", "prefix", fullPrefix)

		// Register /.well-known/bsvalias at app root for capability discovery
		svc.Paymail.Routes.RegisterWellKnown(app)
		slog.Debug("registered paymail .well-known/bsvalias route")
	}

	// Register MessageBox routes
	if svc.MessageBox != nil && svc.MessageBox.Routes != nil && svc.AuthMiddleware != nil {
		prefix := c.MessageBox.Routes.Prefix
		if prefix == "" {
			prefix = "/messagebox"
		}
		mbHandler := svc.MessageBox.Routes.Handler()
		authWrappedHandler := svc.AuthMiddleware.HTTPHandler(mbHandler)
		api.Group(prefix).All("/*", adaptor.HTTPHandler(authWrappedHandler))
		capabilities = append(capabilities, "messagebox")
		slog.Debug("registered messagebox routes", "prefix", c.Server.BasePath+prefix)
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

func handleManifest() fiber.Handler {
	manifest := fiber.Map{
		"babbage": fiber.Map{
			"groupPermissions": fiber.Map{
				"description": "1Sat Stack Admin",
				"protocolPermissions": []fiber.Map{
					{"protocolID": []any{1, "identity key retrieval"}, "counterparty": "self"},
					{"protocolID": []any{2, "server hmac"}, "counterparty": "self"},
				},
			},
			"counterpartyPermissions": fiber.Map{
				"protocols": []fiber.Map{
					{"protocolID": []any{2, "auth message signature"}, "description": "BRC-103/104 authentication signatures"},
				},
			},
		},
	}
	return func(c *fiber.Ctx) error {
		return c.JSON(manifest)
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

	// Start EventBridges (PubSub → overlay queues)
	if svc.PubSub != nil {
		if svc.BAP != nil && svc.BAP.Sync != nil {
			bridge := overlay.NewEventBridge(&overlay.EventBridgeConfig{
				PubSub:   svc.PubSub.PubSub,
				Store:    svc.Store.Store,
				Patterns: []string{"bap:*"},
				QueueFunc: func(ev pubsub.Event) string {
					return string(txo.KeyQueue("bap"))
				},
				Logger: logger,
			})
			if err := bridge.Start(ctx); err != nil {
				logger.Error("failed to start BAP event bridge", "error", err)
			}
		}
		if svc.BSocial != nil && svc.BSocial.Sync != nil {
			bridge := overlay.NewEventBridge(&overlay.EventBridgeConfig{
				PubSub:   svc.PubSub.PubSub,
				Store:    svc.Store.Store,
				Patterns: []string{"map:type:*"},
				QueueFunc: func(ev pubsub.Event) string {
					return string(txo.KeyQueue("bsocial"))
				},
				Logger: logger,
			})
			if err := bridge.Start(ctx); err != nil {
				logger.Error("failed to start BSocial event bridge", "error", err)
			}
		}
		if svc.OPNS != nil && svc.OPNS.Sync != nil {
			bridge := overlay.NewEventBridge(&overlay.EventBridgeConfig{
				PubSub:   svc.PubSub.PubSub,
				Store:    svc.Store.Store,
				Patterns: []string{"opns:mine"},
				QueueFunc: func(ev pubsub.Event) string {
					return string(txo.KeyQueue("opns"))
				},
				Logger: logger,
			})
			if err := bridge.Start(ctx); err != nil {
				logger.Error("failed to start OPNS event bridge", "error", err)
			}
		}
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
	if svc.OrdLock != nil {
		svc.OrdLock.Start(ctx)
		logger.Info("started OrdLock worker")
	}
	if svc.OPNS != nil && svc.OPNS.Sync != nil {
		go func() {
			if err := svc.OPNS.Sync.Start(ctx); err != nil {
				logger.Error("OPNS sync error", "error", err)
			}
		}()
		logger.Info("started OPNS overlay sync")
	}
	if svc.OPNS != nil && svc.OPNS.Crawl != nil {
		go func() {
			if err := svc.OPNS.Crawl.Start(ctx); err != nil {
				logger.Error("OPNS crawl error", "error", err)
			}
		}()
		logger.Info("started OPNS genesis crawl")
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

// createOverlayP2PBus creates the overlay P2P bus from configuration.
// If walletKeyHex is provided, it's used as the libp2p identity (secp256k1).
// This makes the peer ID encode the BRC-100 wallet's public key, enabling
// BRC-42 payment derivation directly from peer IDs.
func createOverlayP2PBus(cfg overlay.P2PConfig, walletKeyHex string, s store.Store, logger *slog.Logger) (*overlay.P2PBus, error) {
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

	return overlay.NewP2PBus(client, s, logger), nil
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

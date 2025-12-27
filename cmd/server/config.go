package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/b-open-io/1sat-stack/admin"
	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/bsv21"
	"github.com/b-open-io/1sat-stack/pkg/indexer"
	"github.com/b-open-io/1sat-stack/pkg/jbsync"
	"github.com/b-open-io/1sat-stack/pkg/logging"
	"github.com/b-open-io/1sat-stack/pkg/ordfs"
	"github.com/b-open-io/1sat-stack/pkg/overlay"
	"github.com/b-open-io/1sat-stack/pkg/owner"
	"github.com/b-open-io/1sat-stack/pkg/pubsub"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/topic"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/b-open-io/go-junglebus"
	arcadeconfig "github.com/bsv-blockchain/arcade/config"
	arcaderoutes "github.com/bsv-blockchain/arcade/routes/fiber"
	"github.com/bsv-blockchain/go-chaintracks/chaintracks"
	chaintracksconfig "github.com/bsv-blockchain/go-chaintracks/config"
	chaintracksroutes "github.com/bsv-blockchain/go-chaintracks/routes/fiber"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	p2p "github.com/bsv-blockchain/go-teranode-p2p-client"
	"github.com/gofiber/fiber/v2"
	"github.com/spf13/viper"
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

	// Overlay engine
	Overlay overlay.Config `mapstructure:"overlay"`

	// Content serving
	ORDFS ordfs.Config `mapstructure:"ordfs"`

	// Owner services
	Owner owner.Config `mapstructure:"owner"`

	// Admin UI
	Admin admin.Config `mapstructure:"admin"`

	// JungleBus Sync subscriptions
	JBSync JBSyncConfig `mapstructure:"jbsync"`
}

// JBSyncConfig holds configuration for JungleBus sync subscriptions
type JBSyncConfig struct {
	// Subscribers is the list of subscription configurations
	Subscribers []jbsync.SubscriberConfig `mapstructure:"subscribers"`
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
	Port     int    `mapstructure:"port"`
	Host     string `mapstructure:"host"`
	BasePath string `mapstructure:"base_path"`
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
	Overlay *overlay.Services
	ORDFS   *ordfs.Services
	Own     *owner.Services
	Admin   *admin.Services

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
	v.SetDefault("server.base_path", "/api")

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

	// Package configs
	c.Indexer.SetDefaults(v, "indexer")
	c.BSV21.SetDefaults(v, "bsv21")
	c.Overlay.SetDefaults(v, "overlay")
	c.ORDFS.SetDefaults(v, "ordfs")
	c.Owner.SetDefaults(v, "owner")
	c.Admin.SetDefaults(v, "admin")
}

// Initialize creates all services from the configuration
func (c *Config) Initialize(ctx context.Context, logger *slog.Logger) (*Services, error) {
	if logger == nil {
		logger = slog.Default()
	}

	svc := &Services{}

	// Initialize store (foundational - other services may depend on it)
	storeSvc, err := c.Store.Initialize(ctx, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize store: %w", err)
	}
	svc.Store = storeSvc

	// Initialize pubsub
	pubsubSvc, err := c.PubSub.Initialize(ctx, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize pubsub: %w", err)
	}
	svc.PubSub = pubsubSvc

	// Initialize JungleBus client (shared by multiple services)
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
	logger.Info("junglebus client initialized", "url", c.JungleBus.URL)

	// Initialize beef storage (pass JungleBus client for fallback lookups)
	beefSvc, err := c.Beef.Initialize(ctx, logger, nil, svc.JungleBus)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize beef: %w", err)
	}
	svc.Beef = beefSvc

	// Initialize P2P client (shared by chaintracks and arcade)
	if c.Chaintracks.Mode == chaintracksconfig.ModeEmbedded || c.Arcade.Mode == arcadeconfig.ModeEmbedded {
		// Set network on P2P config
		c.P2P.Network = c.Network
		p2pClient, err := c.P2P.Initialize(ctx, "1sat-stack")
		if err != nil {
			return nil, fmt.Errorf("failed to initialize p2p client: %w", err)
		}
		svc.P2PClient = p2pClient
		logger.Info("p2p client initialized", "network", c.P2P.Network)
	}

	// Initialize Chaintracks
	if c.Chaintracks.Mode != "" && c.Chaintracks.Mode != "disabled" {
		chaintracker, err := c.Chaintracks.Initialize(ctx, "1sat-stack", svc.P2PClient)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize chaintracks: %w", err)
		}
		svc.Chaintracks = chaintracker
		svc.ChaintracksRoutes = chaintracksroutes.NewRoutes(ctx, chaintracker)

		logger.Info("chaintracks initialized", "mode", c.Chaintracks.Mode)
	}

	// Initialize Arcade
	if c.Arcade.Mode != "" && c.Arcade.Mode != "disabled" {
		// Set network from main config
		c.Arcade.Network = c.Network
		arcadeSvc, err := c.Arcade.Initialize(ctx, logger, svc.Chaintracks, svc.P2PClient)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize arcade: %w", err)
		}
		svc.Arcade = arcadeSvc
		// Create routes with all needed components
		svc.ArcadeRoutes = arcaderoutes.NewRoutes(arcaderoutes.Config{
			Service:        arcadeSvc.ArcadeService,
			Store:          arcadeSvc.Store,
			EventPublisher: arcadeSvc.EventPublisher,
			Arcade:         arcadeSvc.Arcade,
			Logger:         logger,
		})
		logger.Info("arcade initialized", "mode", c.Arcade.Mode)
	}

	// Initialize TXO storage with shared dependencies
	if c.TXO.Mode != txo.ModeDisabled {
		txoSvc, err := c.TXO.Initialize(ctx, logger, storeSvc, pubsubSvc, beefSvc)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize txo: %w", err)
		}
		svc.TXO = txoSvc
	}

	// Initialize overlay engine FIRST (BSV21 needs it for topic/lookup registration)
	if c.Overlay.Mode != overlay.ModeDisabled && svc.TXO != nil {
		overlaySvc, err := c.Overlay.Initialize(ctx, logger, &overlay.InitializeDeps{
			OutputStore:  svc.TXO.OutputStore,
			ChainTracker: svc.Chaintracks,
			// Storage: nil for now - Fee service can provide this later
		})
		if err != nil {
			return nil, fmt.Errorf("failed to initialize overlay: %w", err)
		}
		svc.Overlay = overlaySvc

		// Set topic whitelist/blacklist from config
		svc.Overlay.SetTopicWhitelist(c.Overlay.TopicWhitelist)
		svc.Overlay.SetTopicBlacklist(c.Overlay.TopicBlacklist)
	}

	// Initialize BSV21 AFTER overlay so we can wire them together
	if c.BSV21.Mode != bsv21.ModeDisabled && svc.TXO != nil {
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

			// Register tm_bsv21 discovery topic (admits all deploy+mint operations)
			svc.Overlay.RegisterTopic("tm_bsv21", func(topicName string) (engine.TopicManager, error) {
				return topic.NewBsv21DiscoveryTopicManager(topicName, svc.TXO.OutputStore, logger), nil
			})
			logger.Info("BSV21 discovery topic (tm_bsv21) registered with overlay engine")
		}
	}

	// Activate whitelisted topics after all factories are registered
	if svc.Overlay != nil {
		svc.Overlay.ActivateConfiguredTopics()
	}

	// Initialize ORDFS content serving
	if c.ORDFS.Enabled {
		ordfsSvc, err := c.ORDFS.Initialize(ctx, logger, svc.JungleBus)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize ordfs: %w", err)
		}
		svc.ORDFS = ordfsSvc
	}

	// Initialize indexer services (shared IngestCtx used by owner sync and ingest sync)
	if c.Indexer.Mode != indexer.ModeDisabled && svc.TXO != nil && svc.Beef != nil {
		indexerSvc, err := c.Indexer.Initialize(ctx, logger, &indexer.InitializeDeps{
			Store:       svc.Store.Store,
			BeefStorage: svc.Beef.Storage,
			OutputStore: svc.TXO.OutputStore,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to initialize indexer: %w", err)
		}
		svc.Indexer = indexerSvc
		logger.Debug("indexer service initialized", "mode", c.Indexer.Mode, "syncEnabled", c.Indexer.Sync.Enabled)
	}

	// Initialize owner services (depends on TXO, Beef, Indexer)
	if c.Owner.Mode != owner.ModeDisabled && svc.TXO != nil && svc.Beef != nil && svc.Indexer != nil {
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
		logger.Debug("own service initialized", "mode", c.Owner.Mode)
	} else {
		logger.Debug("own service not initialized", "mode", c.Owner.Mode, "txoNil", svc.TXO == nil, "indexerNil", svc.Indexer == nil)
	}

	// Initialize admin UI
	if c.Admin.Mode != admin.ModeDisabled && svc.Store != nil {
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
	}

	// Initialize JungleBus subscriptions from config (only those with autostart enabled)
	if svc.Store != nil && svc.JungleBus != nil && len(c.JBSync.Subscribers) > 0 {
		for i := range c.JBSync.Subscribers {
			subCfg := &c.JBSync.Subscribers[i]
			if !subCfg.AutoStart {
				continue
			}
			sub, err := jbsync.NewSubscriber(subCfg, svc.Store.Store, svc.Chaintracks, svc.JungleBus, logger)
			if err != nil {
				return nil, fmt.Errorf("failed to create subscriber %s: %w", subCfg.SubscriptionID, err)
			}
			svc.JBSubscribers = append(svc.JBSubscribers, sub)
			logger.Info("JungleBus subscriber initialized",
				"subscription_id", subCfg.SubscriptionID,
				"queue", subCfg.GetQueueName(),
				"from_block", subCfg.FromBlock)
		}
	}

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
		svc.BSV21.Routes.Register(api, prefix)
		capabilities = append(capabilities, "bsv21")
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
		svc.ORDFS.Routes.Register(api, prefix)
		capabilities = append(capabilities, "ordfs")

		// Also register content at root level for compatibility with ordfs protocol
		svc.ORDFS.Routes.RegisterContent(app, "/content")
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

	// Health check endpoint
	api.Get("/health", handleHealth)

	// Capabilities endpoint - returns list of enabled services
	api.Get("/capabilities", handleCapabilities(capabilities))

	// Capabilities endpoint - returns list of enabled services
	api.Get("/capabilities", func(c *fiber.Ctx) error {
		return c.JSON(capabilities)
	})

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

	// Serve Scalar API reference UI at /docs
	app.Get("/docs", func(c *fiber.Ctx) error {
		html := `<!doctype html>
<html>
<head>
    <title>1Sat Stack API</title>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
</head>
<body>
    <script id="api-reference" data-url="/api-spec/swagger.json"></script>
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

	// Start indexer sync services
	if svc.Indexer != nil && svc.Indexer.Sync != nil {
		go func() {
			if err := svc.Indexer.Start(ctx); err != nil {
				logger.Error("Indexer sync error", "error", err)
			}
		}()
		logger.Info("started indexer sync services")
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

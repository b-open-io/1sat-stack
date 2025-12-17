package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/bsv21"
	"github.com/b-open-io/1sat-stack/pkg/indexer"
	"github.com/b-open-io/1sat-stack/pkg/indexer/parse/bitcom"
	"github.com/b-open-io/1sat-stack/pkg/indexer/parse/cosign"
	"github.com/b-open-io/1sat-stack/pkg/indexer/parse/lock"
	"github.com/b-open-io/1sat-stack/pkg/indexer/parse/onesat"
	"github.com/b-open-io/1sat-stack/pkg/indexer/parse/p2pkh"
	"github.com/b-open-io/1sat-stack/pkg/indexer/parse/shrug"
	"github.com/b-open-io/1sat-stack/pkg/ordfs"
	"github.com/b-open-io/1sat-stack/pkg/ovr"
	"github.com/b-open-io/1sat-stack/pkg/own"
	"github.com/b-open-io/1sat-stack/pkg/pubsub"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	arcadeconfig "github.com/bsv-blockchain/arcade/config"
	arcaderoutes "github.com/bsv-blockchain/arcade/routes/fiber"
	"github.com/bsv-blockchain/go-chaintracks/chaintracks"
	chaintracksconfig "github.com/bsv-blockchain/go-chaintracks/config"
	chaintracksroutes "github.com/bsv-blockchain/go-chaintracks/routes/fiber"
	p2p "github.com/bsv-blockchain/go-teranode-p2p-client"
	"github.com/gofiber/fiber/v2"
	"github.com/spf13/viper"
)

// Config holds the complete server configuration
type Config struct {
	// Network settings
	Network NetworkConfig `mapstructure:"network"`

	// Server settings
	Server ServerConfig `mapstructure:"server"`

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
	Merkle  MerkleConfig   `mapstructure:"merkle"`
	Indexer indexer.Config `mapstructure:"indexer"`

	// BSV21 token support
	BSV21 bsv21.Config `mapstructure:"bsv21"`

	// Overlay engine
	Overlay ovr.Config `mapstructure:"overlay"`

	// Content serving
	ORDFS ordfs.Config `mapstructure:"ordfs"`

	// Owner services
	Own own.Config `mapstructure:"own"`
}

// NetworkConfig holds network settings
type NetworkConfig struct {
	Type      string `mapstructure:"type"`      // main or test
	JungleBus string `mapstructure:"junglebus"` // JungleBus service URL
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

// Services holds all initialized services
type Services struct {
	Store   *store.Services
	PubSub  *pubsub.Services
	Beef    *beef.Services
	TXO     *txo.Services
	Indexer *indexer.Services
	BSV21   *bsv21.Services
	Overlay *ovr.Services
	ORDFS   *ordfs.Services
	Own     *own.Services

	// External services
	P2PClient         *p2p.Client
	Chaintracks       chaintracks.Chaintracks
	ChaintracksRoutes *chaintracksroutes.Routes
	Arcade            *arcadeconfig.Services
	ArcadeRoutes      *arcaderoutes.Routes
}

// SetDefaults configures viper defaults for all settings
func (c *Config) SetDefaults(v *viper.Viper) {
	// Network defaults
	v.SetDefault("network.type", "main")
	v.SetDefault("network.junglebus", "https://junglebus.gorillapool.io")

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
	c.Own.SetDefaults(v, "own")
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

	// Initialize beef storage
	beefSvc, err := c.Beef.Initialize(ctx, logger, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize beef: %w", err)
	}
	svc.Beef = beefSvc

	// Initialize P2P client (shared by chaintracks and arcade)
	if c.Chaintracks.Mode == chaintracksconfig.ModeEmbedded || c.Arcade.Mode == arcadeconfig.ModeEmbedded {
		// Set network on P2P config
		c.P2P.Network = c.Network.Type
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
		c.Arcade.Network = c.Network.Type
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
		txoSvc, err := c.TXO.InitializeWithDeps(ctx, storeSvc, pubsubSvc, beefSvc, logger)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize txo: %w", err)
		}
		svc.TXO = txoSvc
	}

	// Initialize indexer with shared dependencies
	if c.Indexer.Mode != indexer.ModeDisabled && svc.TXO != nil && svc.Beef != nil {
		var ps pubsub.PubSub
		if svc.PubSub != nil {
			ps = svc.PubSub.PubSub
		}
		// Create protocol indexers - order matters as some indexers depend on others
		indexers := []indexer.Indexer{
			&p2pkh.P2PKHIndexer{},        // P2PKH address extraction
			&lock.LockIndexer{},          // Lock protocol
			&onesat.InscriptionIndexer{}, // 1Sat ordinals inscriptions
			&onesat.OriginIndexer{},      // Origin tracking
			&onesat.Bsv20Indexer{},       // BSV-20 fungible tokens
			&onesat.Bsv21Indexer{},       // BSV-21 fungible tokens
			&onesat.OrdLockIndexer{},     // Ordinal locks
			&bitcom.BitcomIndexer{},      // Bitcom protocols (B, MAP, SIGMA)
			&cosign.CosignIndexer{},      // Cosign protocol
			&shrug.ShrugIndexer{},        // Shrug tokens
		}
		indexerSvc, err := c.Indexer.Initialize(ctx, logger, svc.TXO.OutputStore, svc.Beef.Storage, ps, indexers)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize indexer: %w", err)
		}
		svc.Indexer = indexerSvc
	}

	// Initialize BSV21 with chaintracks for merkle proof validation
	if c.BSV21.Mode != bsv21.ModeDisabled && svc.TXO != nil {
		bsv21Svc, err := c.BSV21.Initialize(ctx, logger, svc.TXO.OutputStore, svc.Chaintracks)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize bsv21: %w", err)
		}
		svc.BSV21 = bsv21Svc

		// Register BSV21 lookup service with overlay engine
		if svc.Overlay != nil {
			svc.Overlay.RegisterLookupService("bsv21", svc.BSV21.Lookup)
		}
	}

	// Initialize overlay engine (before BSV21 so we can pass it for topic registration)
	if c.Overlay.Mode != ovr.ModeDisabled && svc.TXO != nil {
		overlaySvc, err := c.Overlay.Initialize(ctx, logger, &ovr.InitializeDeps{
			OutputStore:  svc.TXO.OutputStore,
			ChainTracker: svc.Chaintracks,
			// Storage: nil for now - BSV21 can provide this later via SetStorage
		})
		if err != nil {
			return nil, fmt.Errorf("failed to initialize overlay: %w", err)
		}
		svc.Overlay = overlaySvc
	}

	// Initialize ORDFS content serving
	if c.ORDFS.Mode != ordfs.ModeDisabled && svc.Beef != nil {
		loader := ordfs.NewBeefLoader(ctx, svc.Beef.Storage)
		ordfsSvc, err := c.ORDFS.Initialize(ctx, logger, loader)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize ordfs: %w", err)
		}
		svc.ORDFS = ordfsSvc
	}

	// Initialize owner services (depends on TXO and Indexer)
	if c.Own.Mode != own.ModeDisabled && svc.TXO != nil {
		var ingestCtx *indexer.IngestCtx
		if svc.Indexer != nil {
			ingestCtx = svc.Indexer.IngestCtx
		}
		ownSvc, err := c.Own.Initialize(ctx, logger, svc.TXO.OutputStore, ingestCtx)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize own: %w", err)
		}
		svc.Own = ownSvc
		logger.Debug("own service initialized", "mode", c.Own.Mode)
	} else {
		logger.Debug("own service not initialized", "mode", c.Own.Mode, "txoNil", svc.TXO == nil)
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
		prefix := c.TXO.Routes.Prefix
		if prefix == "" {
			prefix = "/txo"
		}
		txoGroup := api.Group(prefix)
		svc.TXO.Routes.Register(txoGroup)
		capabilities = append(capabilities, "txo")
	}

	// Register owner routes
	if svc.Own != nil && svc.Own.Routes != nil {
		prefix := c.Own.Routes.Prefix
		if prefix == "" {
			prefix = "/own"
		}
		ownGroup := api.Group(prefix)
		svc.Own.Routes.Register(ownGroup)
		capabilities = append(capabilities, "own")
		slog.Debug("registered own routes", "prefix", prefix)
	} else {
		slog.Debug("own routes not registered", "ownNil", svc.Own == nil, "ownMode", c.Own.Mode)
	}

	// Register indexer routes
	if svc.Indexer != nil && svc.Indexer.Routes != nil {
		prefix := c.Indexer.Routes.Prefix
		if prefix == "" {
			prefix = "/idx"
		}
		svc.Indexer.Routes.Register(api, prefix)
		capabilities = append(capabilities, "idx")
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
		blockGroup := api.Group("/blk")
		svc.ChaintracksRoutes.Register(blockGroup)
		capabilities = append(capabilities, "chaintracks")
		slog.Debug("registered chaintracks routes", "prefix", "/blk")
	}

	// Register Arcade routes (transaction broadcast, status)
	if svc.ArcadeRoutes != nil {
		arcGroup := api.Group("/arc")
		svc.ArcadeRoutes.Register(arcGroup)
		capabilities = append(capabilities, "arcade")
		slog.Debug("registered arcade routes", "prefix", "/arc")
	}

	// Health check endpoint
	api.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"status": "ok",
		})
	})

	// Capabilities endpoint - returns list of enabled services
	api.Get("/capabilities", func(c *fiber.Ctx) error {
		return c.JSON(capabilities)
	})

	// Setup API documentation routes
	registerDocsRoutes(app)
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
	if svc.Own != nil {
		if err := svc.Own.Close(); err != nil {
			errs = append(errs, fmt.Errorf("own close: %w", err))
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

	if svc.Indexer != nil {
		if err := svc.Indexer.Close(); err != nil {
			errs = append(errs, fmt.Errorf("indexer close: %w", err))
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

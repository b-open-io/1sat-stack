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
	"github.com/b-open-io/1sat-stack/pkg/ordfs"
	"github.com/b-open-io/1sat-stack/pkg/pubsub"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/txo"
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
	P2P         P2PConfig         `mapstructure:"p2p"`
	Chaintracks ChaintracksConfig `mapstructure:"chaintracks"`
	Arcade      ArcadeConfig      `mapstructure:"arcade"`

	// Transaction services
	Merkle  MerkleConfig   `mapstructure:"merkle"`
	Indexer indexer.Config `mapstructure:"indexer"`

	// BSV21 token support
	BSV21 bsv21.Config `mapstructure:"bsv21"`

	// Content serving
	ORDFS ordfs.Config `mapstructure:"ordfs"`
}

// NetworkConfig holds network settings
type NetworkConfig struct {
	Type      string `mapstructure:"type"`      // main or test
	JungleBus string `mapstructure:"junglebus"` // JungleBus service URL
}

// P2PConfig holds P2P client configuration
type P2PConfig struct {
	Mode        string `mapstructure:"mode"`         // disabled, embedded, remote
	StoragePath string `mapstructure:"storage_path"` // Path for P2P storage
}

// ChaintracksConfig holds chaintracks configuration
type ChaintracksConfig struct {
	Mode         string `mapstructure:"mode"`          // disabled, embedded, remote
	StoragePath  string `mapstructure:"storage_path"`  // Path for chaintracks storage
	BootstrapURL string `mapstructure:"bootstrap_url"` // Bootstrap node URL
}

// ArcadeConfig holds arcade configuration
type ArcadeConfig struct {
	Mode        string              `mapstructure:"mode"`         // disabled, embedded, remote
	Network     string              `mapstructure:"network"`      // main or test
	StoragePath string              `mapstructure:"storage_path"` // Path for arcade storage
	Database    ArcadeDatabaseConfig `mapstructure:"database"`
	Events      ArcadeEventsConfig   `mapstructure:"events"`
}

// ArcadeDatabaseConfig holds arcade database settings
type ArcadeDatabaseConfig struct {
	Type       string `mapstructure:"type"`        // sqlite, postgres
	SQLitePath string `mapstructure:"sqlite_path"` // Path for SQLite database
}

// ArcadeEventsConfig holds arcade events settings
type ArcadeEventsConfig struct {
	Type       string `mapstructure:"type"`        // memory, redis
	BufferSize int    `mapstructure:"buffer_size"` // Event buffer size
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
	ORDFS   *ordfs.Services
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

	// External services defaults
	v.SetDefault("p2p.mode", "disabled")
	v.SetDefault("p2p.storage_path", "~/.1sat")

	v.SetDefault("chaintracks.mode", "disabled")
	v.SetDefault("chaintracks.storage_path", "~/.1sat/chaintracks")
	v.SetDefault("chaintracks.bootstrap_url", "https://mainnet.gorillanode.io/api/v1")

	v.SetDefault("arcade.mode", "disabled")
	v.SetDefault("arcade.network", "main")
	v.SetDefault("arcade.storage_path", "~/.1sat/arcade")
	v.SetDefault("arcade.database.type", "sqlite")
	v.SetDefault("arcade.database.sqlite_path", "~/.1sat/arcade/arcade.db")
	v.SetDefault("arcade.events.type", "memory")
	v.SetDefault("arcade.events.buffer_size", 1000)

	// Merkle service defaults
	v.SetDefault("merkle.mode", "disabled")
	v.SetDefault("merkle.routes.enabled", true)
	v.SetDefault("merkle.routes.prefix", "")

	// Package configs
	c.Indexer.SetDefaults(v, "indexer")
	c.BSV21.SetDefaults(v, "bsv21")
	c.ORDFS.SetDefaults(v, "ordfs")
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
		// Note: indexers will be provided by the application code
		indexerSvc, err := c.Indexer.Initialize(ctx, logger, svc.TXO.OutputStore, svc.Beef.Storage, ps, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize indexer: %w", err)
		}
		svc.Indexer = indexerSvc
	}

	// Initialize BSV21
	if c.BSV21.Mode != bsv21.ModeDisabled && svc.TXO != nil {
		bsv21Svc, err := c.BSV21.Initialize(ctx, logger, svc.TXO.OutputStore, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize bsv21: %w", err)
		}
		svc.BSV21 = bsv21Svc
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

	return svc, nil
}

// RegisterRoutes registers all HTTP routes on the Fiber app
func (c *Config) RegisterRoutes(app *fiber.App, svc *Services) {
	// Create API group with base path
	api := app.Group(c.Server.BasePath)

	// Register beef routes
	if svc.Beef != nil && svc.Beef.Routes != nil {
		beefGroup := api.Group("/beef")
		svc.Beef.Routes.Register(beefGroup)
	}

	// Register pubsub/SSE routes
	if svc.PubSub != nil && svc.PubSub.Routes != nil {
		sseGroup := api.Group("/sse")
		svc.PubSub.Routes.Register(sseGroup)
	}

	// Register indexer routes
	if svc.Indexer != nil && svc.Indexer.Routes != nil {
		prefix := c.Indexer.Routes.Prefix
		if prefix == "" {
			prefix = "/indexer"
		}
		svc.Indexer.Routes.Register(api, prefix)
	}

	// Register BSV21 routes
	if svc.BSV21 != nil && svc.BSV21.Routes != nil {
		prefix := c.BSV21.Routes.Prefix
		if prefix == "" {
			prefix = "/bsv21"
		}
		svc.BSV21.Routes.Register(api, prefix)
	}

	// Register ORDFS routes
	if svc.ORDFS != nil && svc.ORDFS.Routes != nil {
		prefix := c.ORDFS.Routes.Prefix
		if prefix == "" {
			prefix = "/ordfs"
		}
		svc.ORDFS.Routes.Register(api, prefix)

		// Also register content at root level for compatibility with ordfs protocol
		svc.ORDFS.Routes.RegisterContent(app, "/content")
	}

	// Health check endpoint
	api.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"status": "ok",
		})
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

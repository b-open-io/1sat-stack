package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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
	"github.com/b-open-io/1sat-stack/pkg/httputil"
	"github.com/b-open-io/1sat-stack/sweep"

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
	"github.com/b-open-io/1sat-stack/pkg/spends"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/b-open-io/1sat-stack/pkg/wallet"
	"github.com/b-open-io/go-junglebus"
	"github.com/bsv-blockchain/go-chaintracks/chaintracks"
	chaintracksconfig "github.com/bsv-blockchain/go-chaintracks/config"
	chaintracksroutes "github.com/bsv-blockchain/go-chaintracks/routes/fiber"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	msgbus "github.com/bsv-blockchain/go-p2p-message-bus"
	"github.com/bsv-blockchain/go-sdk/transaction"
	p2p "github.com/bsv-blockchain/go-teranode-p2p-client"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/adaptor"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/spf13/viper"
	"go.mongodb.org/mongo-driver/v2/mongo"
	mongooptions "go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Config holds the complete server configuration
type Config struct {
	// DataDir is the base directory for all data files.
	// Resolved at startup from --data-dir flag, ONESAT_DATA_DIR env, or default ~/.1sat/
	// Not a Viper field — set directly before Initialize.
	DataDir string `mapstructure:"-"`

	// LogStore is set by main before Initialize, passed to admin for log queries.
	LogStore *logging.SQLiteHandler `mapstructure:"-"`

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

	// Spends service
	Spends spends.Config `mapstructure:"spends"`

	// Content serving
	ORDFS ordfs.Config `mapstructure:"ordfs"`

	// Owner services
	Owner owner.Config `mapstructure:"owner"`

	// Admin UI
	Admin admin.Config `mapstructure:"admin"`

	// Sweep UI
	Sweep sweep.Config `mapstructure:"sweep"`

	// Landing page
	Landing landing.Config `mapstructure:"landing"`

	// Wallet service
	Wallet wallet.Config `mapstructure:"wallet"`

	// Auth middleware
	Auth auth.Config `mapstructure:"auth"`

	// Paymail service
	Paymail paymail.Config `mapstructure:"paymail"`

	// MessageBox URL for remote messagebox server (used by paymail)
	MessageBoxURL string `mapstructure:"messagebox_url"`
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
	Spends  *spends.Services
	ORDFS   *ordfs.Services
	Own     *owner.Services
	Admin   *admin.Services
	Sweep   *sweep.Services
	Landing *landing.Services
	Wallet  *wallet.Services
	Paymail *paymail.Services

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
	v.SetDefault("p2p.storage_path", "p2p")

	c.Chaintracks.SetDefaults(v, "chaintracks")
	v.SetDefault("chaintracks.mode", "embedded")
	v.SetDefault("chaintracks.storage_path", "chaintracks")

	v.SetDefault("arcade.mode", "embedded")
	v.SetDefault("arcade.storage_path", "arcade")
	v.SetDefault("arcade.database.sqlite_path", "arcade/arcade.db")
	v.SetDefault("arcade.teranode.broadcast_urls", []string{
		"http://mainnet.gorillanode.io:8833",
	})
	v.SetDefault("arcade.teranode.datahub_urls", []string{
		"https://mainnet.gorillanode.io/api/v1",
	})
	v.SetDefault("arcade.teranode.timeout", "30s")

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
	c.Spends.SetDefaults(v, "spends")
	c.ORDFS.SetDefaults(v, "ordfs")
	c.Owner.SetDefaults(v, "owner")
	c.Admin.SetDefaults(v, "admin")
	c.Sweep.SetDefaults(v, "sweep")
	c.Landing.SetDefaults(v, "landing")
	c.Wallet.SetDefaults(v, "wallet")
	c.Auth.SetDefaults(v, "auth")
	c.Paymail.SetDefaults(v, "paymail")
	v.SetDefault("messagebox_url", "")
}

// resolvePath resolves a path relative to the data dir.
// Absolute paths are returned as-is. Empty strings return empty.
// Relative paths are joined with DataDir.
func (c *Config) resolvePath(p string) string {
	if p == "" {
		return ""
	}
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		p = filepath.Join(home, p[2:])
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.DataDir, p)
}

// resolveAllPaths resolves all path fields on Config against DataDir.
// Called once after Viper Unmarshal and applyRuntimeConfig, so all paths
// (whether from Viper defaults or config store) are resolved in one place.
func (c *Config) resolveAllPaths() {
	c.Store.Badger.Path = c.resolvePath(c.Store.Badger.Path)
	c.Auth.SessionPath = c.resolvePath(c.Auth.SessionPath)
	c.Chaintracks.StoragePath = c.resolvePath(c.Chaintracks.StoragePath)
	c.Overlay.StoragePath = c.resolvePath(c.Overlay.StoragePath)
	c.Overlay.P2P.StoragePath = c.resolvePath(c.Overlay.P2P.StoragePath)
	c.P2P.StoragePath = c.resolvePath(c.P2P.StoragePath)
	c.Paymail.DBPath = c.resolvePath(c.Paymail.DBPath)

	for i := range c.Beef.Chain {
		c.Beef.Chain[i].Filesystem.Path = c.resolvePath(c.Beef.Chain[i].Filesystem.Path)
		c.Beef.Chain[i].Badger.Path = c.resolvePath(c.Beef.Chain[i].Badger.Path)
	}
}

// applyRuntimeConfig populates Config fields from the config store.
// The config store is the sole source of truth for all operational settings.
// Only values actually present in the store are applied — zero values from
// missing keys leave the Viper defaults in place.
func (c *Config) applyRuntimeConfig(rc *configpkg.RuntimeConfig) {
	if !rc.SetupComplete {
		return
	}

	// Server
	if rc.ServerPort > 0 {
		c.Server.Port = rc.ServerPort
	}
	if rc.ServerHost != "" {
		c.Server.Host = rc.ServerHost
	}
	if rc.ServerBasePath != "" {
		c.Server.BasePath = rc.ServerBasePath
	}
	if rc.ServerBodyLimit != "" {
		c.Server.BodyLimit = rc.ServerBodyLimit
	}

	// Network
	if rc.Network != "" {
		c.Network = rc.Network
	}

	// Logging
	if rc.LogLevel != "" {
		c.Logging.Level = rc.LogLevel
	}

	// Auth
	if rc.AuthMode == "local" {
		c.Auth.AllowUnauthenticated = true
	} else if rc.AuthMode == "authenticated" {
		c.Auth.AllowUnauthenticated = false
	}
	if rc.AuthAPIKey != "" {
		c.Auth.ApiKey = rc.AuthAPIKey
	}
	if rc.AuthSessionPath != "" {
		c.Auth.SessionPath = rc.AuthSessionPath
	}
	if rc.AuthSessionTTL != "" {
		c.Auth.SessionTTL = rc.AuthSessionTTL
	}

	// Store
	if rc.StoreMode != "" {
		c.Store.Mode = rc.StoreMode
	}
	if rc.StoreProvider != "" {
		c.Store.Provider = rc.StoreProvider
	}
	if rc.StoreBadgerPath != "" {
		c.Store.Badger.Path = rc.StoreBadgerPath
	}
	if rc.StoreRedisURL != "" {
		c.Store.Redis.URL = rc.StoreRedisURL
	}

	// MessageBox
	if rc.MessageBoxURL != "" {
		c.MessageBoxURL = rc.MessageBoxURL
	}

	// Chaintracks
	if rc.ChaintracksMode != "" {
		c.Chaintracks.Mode = chaintracksconfig.Mode(rc.ChaintracksMode)
	}
	if rc.ChaintracksPath != "" {
		c.Chaintracks.StoragePath = rc.ChaintracksPath
	}
	if rc.ChaintracksURL != "" {
		c.Chaintracks.URL = rc.ChaintracksURL
	}

	// JungleBus
	if rc.JungleBusURL != "" {
		c.JungleBus.URL = rc.JungleBusURL
	}
	if rc.JungleBusToken != "" {
		c.JungleBus.Token = rc.JungleBusToken
	}

	// Worker defaults (applied before per-service overrides)
	if rc.WorkerConcurrency > 0 {
		c.Indexer.Sync.Concurrency = rc.WorkerConcurrency
	}
	if rc.WorkerPageSize > 0 {
		c.Indexer.Sync.PageSize = uint32(rc.WorkerPageSize)
	}
	if rc.WorkerPollDelay != "" {
		if d, err := time.ParseDuration(rc.WorkerPollDelay); err == nil {
			c.Indexer.Sync.PollDelay = d
		}
	}

	// Indexer
	if rc.IndexerMode != "" {
		c.Indexer.Mode = rc.IndexerMode
	}
	if rc.IndexerLogLevel != "" {
		c.Indexer.LogLevel = rc.IndexerLogLevel
	}
	if rc.IndexerVerbose {
		c.Indexer.Verbose = true
	}
	if rc.IndexerParsers != "" {
		var tags []string
		if err := json.Unmarshal([]byte(rc.IndexerParsers), &tags); err == nil {
			c.Indexer.Tags = tags
		}
	}
	if rc.IndexerSyncEnabled {
		c.Indexer.Sync.Enabled = true
	}
	if rc.IndexerSyncSubscriptionIDs != "" {
		c.Indexer.Sync.SubscriptionIDs = splitMultiDelim(rc.IndexerSyncSubscriptionIDs)
	}
	if rc.IndexerSyncConcurrency > 0 {
		c.Indexer.Sync.Concurrency = rc.IndexerSyncConcurrency
	}
	if rc.IndexerSyncBatchSize > 0 {
		c.Indexer.Sync.BatchSize = rc.IndexerSyncBatchSize
	}
	if rc.IndexerSyncMempool {
		c.Indexer.Sync.EnableMempool = true
	}

	// Overlay engine (shared)
	if rc.OverlayStorageBackend != "" {
		c.Overlay.StorageBackend = rc.OverlayStorageBackend
	}
	if rc.OverlayStoragePath != "" {
		if c.Overlay.StorageBackend == "postgres" {
			c.Overlay.StorageURL = rc.OverlayStoragePath
		} else {
			c.Overlay.StoragePath = rc.OverlayStoragePath
		}
	}
	if rc.OverlayP2PEnabled {
		c.Overlay.P2P.Enabled = true
	}
	if rc.OverlayP2PPort != "" {
		if port, err := strconv.Atoi(rc.OverlayP2PPort); err == nil {
			c.Overlay.P2P.Port = port
		}
	}
	if rc.OverlayP2PDHTMode != "" {
		c.Overlay.P2P.DHTMode = rc.OverlayP2PDHTMode
	}
	if rc.OverlayP2PBootstrapPeers != "" {
		c.Overlay.P2P.BootstrapPeers = splitMultiDelim(rc.OverlayP2PBootstrapPeers)
	}

	// BAP overlay
	if rc.BAPLogLevel != "" {
		c.BAP.LogLevel = rc.BAPLogLevel
	}
	if rc.BAPEnabled {
		c.BAP.Mode = "embedded"
		c.Overlay.Mode = "embedded"
		if c.BAP.Sync == nil {
			c.BAP.Sync = &overlay.OverlaySyncConfig{}
		}
		if rc.BAPSyncSubID != "" {
			c.BAP.Sync.SubscriptionID = rc.BAPSyncSubID
			c.BAP.Sync.Enabled = true
		}
		if rc.BAPSyncConcurrency > 0 {
			c.BAP.Sync.Concurrency = rc.BAPSyncConcurrency
		}
		if rc.BAPSyncBatchSize > 0 {
			c.BAP.Sync.BatchSize = rc.BAPSyncBatchSize
		}
	}

	// BSocial overlay
	if rc.BSocialLogLevel != "" {
		c.BSocial.LogLevel = rc.BSocialLogLevel
	}
	if rc.BSocialEnabled {
		c.BSocial.Mode = "embedded"
		c.Overlay.Mode = "embedded"
		if c.BSocial.Sync == nil {
			c.BSocial.Sync = &overlay.OverlaySyncConfig{}
		}
		if rc.BSocialSyncSubID != "" {
			c.BSocial.Sync.SubscriptionID = rc.BSocialSyncSubID
			c.BSocial.Sync.Enabled = true
		}
		if rc.BSocialSyncConcurrency > 0 {
			c.BSocial.Sync.Concurrency = rc.BSocialSyncConcurrency
		}
		if rc.BSocialSyncBatchSize > 0 {
			c.BSocial.Sync.BatchSize = rc.BSocialSyncBatchSize
		}
	}

	// OPNS overlay
	if rc.OPNSLogLevel != "" {
		c.OPNS.LogLevel = rc.OPNSLogLevel
	}
	if rc.OPNSEnabled {
		c.OPNS.Mode = "embedded"
		c.Overlay.Mode = "embedded"
		if c.OPNS.Sync == nil {
			c.OPNS.Sync = &overlay.OverlaySyncConfig{}
		}
		if rc.OPNSSyncSubID != "" {
			c.OPNS.Sync.SubscriptionID = rc.OPNSSyncSubID
			c.OPNS.Sync.Enabled = true
		}
		if rc.OPNSCrawlConcurrency > 0 {
			c.OPNS.Crawl.Concurrency = rc.OPNSCrawlConcurrency
		}
		if rc.OPNSSyncBatchSize > 0 {
			c.OPNS.Sync.BatchSize = rc.OPNSSyncBatchSize
		}
	}
	if rc.OPNSPaymail {
		c.Paymail.Mode = "enabled"
	}

	// OrdLock overlay
	if rc.OrdLockLogLevel != "" {
		c.OrdLock.LogLevel = rc.OrdLockLogLevel
	}
	if rc.OrdLockEnabled {
		c.OrdLock.Mode = "embedded"
		c.Overlay.Mode = "embedded"
		if c.OrdLock.Sync == nil {
			c.OrdLock.Sync = &overlay.OverlaySyncConfig{}
		}
		if rc.OrdLockSyncSubID != "" {
			c.OrdLock.Sync.SubscriptionID = rc.OrdLockSyncSubID
			c.OrdLock.Sync.Enabled = true
		}
		if rc.OrdLockSyncConcurrency > 0 {
			c.OrdLock.Sync.Concurrency = rc.OrdLockSyncConcurrency
		}
		if rc.OrdLockSyncBatchSize > 0 {
			c.OrdLock.Sync.BatchSize = rc.OrdLockSyncBatchSize
		}
	}

	// BSV21
	if rc.BSV21LogLevel != "" {
		c.BSV21.LogLevel = rc.BSV21LogLevel
	}
	if rc.BSV21Enabled {
		c.BSV21.Mode = "embedded"
		c.Overlay.Mode = "embedded"
		if c.BSV21.Sync == nil {
			c.BSV21.Sync = &bsv21.SyncConfig{}
		}
		if rc.BSV21SyncSubID != "" {
			c.BSV21.Sync.SubscriptionID = rc.BSV21SyncSubID
			c.BSV21.Sync.Enabled = true
		}
		if rc.BSV21SyncConcurrency > 0 {
			c.BSV21.Sync.DispatchWorkers = rc.BSV21SyncConcurrency
		}
		if rc.BSV21SyncBatchSize > 0 {
			c.BSV21.Sync.BatchSize = rc.BSV21SyncBatchSize
		}
		if rc.BSV21TokenWorkers > 0 {
			c.BSV21.Sync.TokenWorkers = rc.BSV21TokenWorkers
		}
	}

	// ORDFS
	if rc.ORDFSEnabled {
		c.ORDFS.Enabled = true
	}
	if rc.ORDFSLRUSize > 0 {
		c.ORDFS.Cache.LRUSize = rc.ORDFSLRUSize
	}
	if rc.ORDFSRedisURL != "" {
		c.ORDFS.Cache.RedisURL = rc.ORDFSRedisURL
	}
	if rc.ORDFSRedisTTL != "" {
		c.ORDFS.Cache.RedisTTL = rc.ORDFSRedisTTL
	}

	// Owner
	if rc.OwnerMode != "" {
		c.Owner.Mode = rc.OwnerMode
	}

	// Paymail
	if rc.PaymailMode != "" {
		c.Paymail.Mode = rc.PaymailMode
	}
	if rc.PaymailDBPath != "" {
		c.Paymail.DBPath = rc.PaymailDBPath
	}

	// MongoDB
	if rc.MongoDBURL != "" {
		c.MongoDB.URL = rc.MongoDBURL
	}

	// Beef chain
	if rc.BeefChain != "" {
		if chain, err := convertBeefChain(rc.BeefChain); err == nil && len(chain) > 0 {
			c.Beef.Chain = chain
		}
	}

	// Spends chain
	if rc.SpendsChain != "" {
		if chain, err := convertSpendsChain(rc.SpendsChain); err == nil && len(chain) > 0 {
			c.Spends.Chain = chain
		}
	}

	// PubSub
	if rc.PubSubProvider != "" {
		c.PubSub.Provider = rc.PubSubProvider
	}
	if rc.PubSubBufferSize > 0 {
		c.PubSub.Channels.BufferSize = rc.PubSubBufferSize
	}
	if rc.PubSubRedisURL != "" {
		c.PubSub.Redis.URL = rc.PubSubRedisURL
	}
}

// splitMultiDelim splits a string by newlines, commas, or both, trimming whitespace
// and dropping empty entries. The admin UI sends newline-separated textareas but
// config files may use commas.
func splitMultiDelim(s string) []string {
	// Normalize newlines to commas then split
	s = strings.ReplaceAll(s, "\r\n", ",")
	s = strings.ReplaceAll(s, "\n", ",")
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// convertBeefChain converts the flat admin UI format to the nested Go config format.
// Admin UI writes: [{"type":"lru","size":"100mb"},{"type":"filesystem","path":"beef"}]
// Go expects: [{"provider":"lru","lru":{"size":"100mb"}},{"provider":"filesystem","filesystem":{"path":"beef"}}]
func convertBeefChain(jsonStr string) ([]beef.ChainConfig, error) {
	var flat []map[string]string
	if err := json.Unmarshal([]byte(jsonStr), &flat); err != nil {
		return nil, err
	}

	var chain []beef.ChainConfig
	for _, item := range flat {
		provider := item["type"]
		cc := beef.ChainConfig{Provider: provider}
		switch provider {
		case "lru":
			cc.LRU.Size = item["size"]
		case "filesystem":
			cc.Filesystem.Path = item["path"]
		case "redis":
			cc.Redis.URL = item["url"]
		case "badger":
			cc.Badger.Path = item["path"]
		case "junglebus", "store":
			// No additional config needed
		}
		chain = append(chain, cc)
	}
	return chain, nil
}

// convertSpendsChain converts the flat admin UI format to the nested Go config format.
// Admin UI writes: [{"type":"lru","size":"100mb"},{"type":"store"},{"type":"junglebus"}]
// Go expects: [{"provider":"lru","lru":{"size":"100mb"}},{"provider":"store"},{"provider":"junglebus"}]
func convertSpendsChain(jsonStr string) ([]spends.ChainConfig, error) {
	var flat []map[string]string
	if err := json.Unmarshal([]byte(jsonStr), &flat); err != nil {
		return nil, err
	}

	var chain []spends.ChainConfig
	for _, item := range flat {
		provider := item["type"]
		cc := spends.ChainConfig{Provider: provider}
		switch provider {
		case "lru":
			cc.LRU.Size = item["size"]
		case "store", "junglebus":
			// No additional config needed
		}
		chain = append(chain, cc)
	}
	return chain, nil
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
	c.resolveAllPaths()

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
	if c.Beef.Routes.Enabled && svc.Chaintracks != nil {
		svc.Beef.Routes = beef.NewRoutes(beefSvc.Storage, svc.Chaintracks.GetHeight)
	}
	logger.Info("beef initialized", "duration", time.Since(start).Round(time.Millisecond))

	// Initialize external arcade HTTP client + event broker + broadcast handler.
	if runtimeCfg.ArcadeURL != "" && runtimeCfg.ArcadeCallbackToken != "" {
		start = time.Now()
		arcadeLogger := logging.NewComponentLogger(logger, "arcade", "")
		svc.ArcadeClient = arcadeclient.New(runtimeCfg.ArcadeURL, runtimeCfg.ArcadeCallbackToken, nil, arcadeLogger)
		svc.ArcadeBroker = arcadeclient.NewEventBroker(svc.ArcadeClient, arcadeLogger)

		waitTimeout := broadcast.DefaultWaitTimeout
		if d, perr := time.ParseDuration(runtimeCfg.ArcadeWaitTimeout); perr == nil && d > 0 {
			waitTimeout = d
		}
		svc.BroadcastHandler = broadcast.NewHandler(svc.ArcadeBroker, svc.Beef.Storage, waitTimeout, arcadeLogger)
		svc.BroadcastRoutes = broadcast.NewRoutes(svc.BroadcastHandler, svc.ArcadeClient, arcadeLogger)

		// Bridge arcade SSE events to the local "arc" pubsub topic so the
		// existing StatusHandler (and any other arc-pubsub consumer) keeps
		// working without changes. For terminal statuses, we fetch the full
		// status to populate MerklePath / ExtraInfo (SSE payload is slim).
		if svc.PubSub != nil && svc.PubSub.PubSub != nil {
			arcadeLogger.Info("registering SSE → arc pubsub bridge")
			ps := svc.PubSub.PubSub
			ac := svc.ArcadeClient
			svc.ArcadeBroker.AddHandler(func(handlerCtx context.Context, evt *arcadeclient.SSEEvent) {
				arcEvent := indexer.ArcEvent{
					TxID:   evt.Txid,
					Status: evt.TxStatus,
				}
				// Fetch full status for terminal events to capture MerklePath / ExtraInfo.
				if arcadeclient.IsTerminal(evt.TxStatus) {
					fetchCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					if status, err := ac.GetStatus(fetchCtx, evt.Txid); err == nil && status != nil {
						if path, decodeErr := hex.DecodeString(status.MerklePath); decodeErr == nil {
							arcEvent.MerklePath = path
						} else if status.MerklePath != "" {
							arcadeLogger.Warn("invalid merkle path hex from arcade",
								"txid", evt.Txid, "err", decodeErr)
						}
						arcEvent.ExtraInfo = status.ExtraInfo
					} else if err != nil {
						arcadeLogger.Warn("failed to fetch full status for terminal event",
							"txid", evt.Txid, "tx_status", evt.TxStatus, "err", err)
					}
					cancel()
				}
				data, err := json.Marshal(arcEvent)
				if err != nil {
					arcadeLogger.Error("failed to marshal arc event", "txid", evt.Txid, "err", err)
					return
				}
				if err := ps.Publish(handlerCtx, "arc", string(data)); err != nil {
					arcadeLogger.Error("failed to publish arc event", "txid", evt.Txid, "err", err)
					return
				}
				arcadeLogger.Info("arc event bridged", "txid", evt.Txid, "tx_status", evt.TxStatus)
			})
		}

		logger.Info("external arcade client initialized",
			"url", runtimeCfg.ArcadeURL, "wait_timeout", waitTimeout, "duration", time.Since(start).Round(time.Millisecond))
	}

	// Initialize TXO storage with shared dependencies
	if c.TXO.Mode != txo.ModeDisabled {
		start = time.Now()
		txoSvc, err := c.TXO.Initialize(ctx, logging.NewComponentLogger(logger, "txo", ""), storeSvc, pubsubSvc, beefSvc)
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
		if svc.ArcadeClient != nil {
			overlayDeps.Broadcaster = broadcast.NewBroadcaster(svc.ArcadeClient, logger)
		}
		overlayLogger := logging.NewComponentLogger(logger, "overlay", "")
		// Create overlay P2P bus if enabled
		if c.Overlay.P2P.Enabled && svc.Store != nil {
			p2pBus, err := createOverlayP2PBus(c.Overlay.P2P, c.Wallet.ServerPrivateKey, svc.Store.Store, overlayLogger)
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

	// Initialize BSV21
	if c.BSV21.Mode != bsv21.ModeDisabled && svc.TXO != nil && moduleDeps != nil {
		start = time.Now()
		bsv21Svc, err := c.BSV21.Initialize(ctx, logging.NewComponentLogger(logger, "bsv21", c.BSV21.LogLevel), svc.TXO.OutputStore, moduleDeps, svc.ConfigStore, svc.Chaintracks, svc.Beef.Storage, svc.JungleBus)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize bsv21: %w", err)
		}
		svc.BSV21 = bsv21Svc
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
	if c.BAP.Mode != bap.ModeDisabled && moduleDeps != nil {
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
			svc.BAP.Sync = overlay.NewOverlaySync(syncCfg, "tm_bap", svc.Store.Store, svc.Beef.Storage, svc.BAP.Engine, bapLogger)
		}
		logger.Info("bap initialized", "duration", time.Since(start).Round(time.Millisecond))
	}

	// Initialize BSocial
	if c.BSocial.Mode != bsocial.ModeDisabled && svc.MongoDB != nil {
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
			svc.BSocial.Sync = overlay.NewOverlaySync(syncCfg, "tm_bsocial", svc.Store.Store, svc.Beef.Storage, svc.BSocial.Engine, bsocialLogger)
		}
		logger.Info("bsocial initialized", "duration", time.Since(start).Round(time.Millisecond))
	}

	// Initialize OPNS
	if c.OPNS.Mode != opns.ModeDisabled && moduleDeps != nil {
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
			svc.OPNS.Sync = overlay.NewOverlaySync(opnsSyncCfg, "tm_opns", svc.Store.Store, svc.Beef.Storage, svc.OPNS.Engine, opnsLogger)
		}
		logger.Info("opns initialized", "duration", time.Since(start).Round(time.Millisecond))
	}

	// Initialize OrdLock
	if c.OrdLock.Mode != ordlockpkg.ModeDisabled && moduleDeps != nil {
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
			svc.OrdLock.Sync = overlay.NewOverlaySync(syncCfg, ordlockpkg.TopicName, svc.Store.Store, svc.Beef.Storage, svc.OrdLock.Engine, ordlockLogger)
		}
		logger.Info("ordlock initialized", "duration", time.Since(start).Round(time.Millisecond))
	}

	// Initialize Spends
	if c.Spends.Mode != spends.ModeDisabled && c.Spends.Mode != "" && svc.Store != nil {
		start = time.Now()
		spendsSvc, err := c.Spends.Initialize(ctx, logging.NewComponentLogger(logger, "spends", ""), svc.Store.Store, svc.JungleBus)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize spends: %w", err)
		}
		svc.Spends = spendsSvc
		if svc.TXO != nil && spendsSvc != nil {
			svc.TXO.OutputStore.SpendService = spendsSvc.Storage
		}
		logger.Info("spends initialized", "duration", time.Since(start).Round(time.Millisecond))
	}

	// Initialize ORDFS content serving
	if c.ORDFS.Enabled {
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
			// Build topic→lookup service map for block height routing
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
			statusDeps.LookupServices = lookups
			svc.Indexer.SetupStatusHandler(statusDeps)
		}

		// Setup pending auditor to verify proofs on each new block
		if svc.Chaintracks != nil {
			svc.Indexer.SetupPendingAuditor(&indexer.PendingAuditorDeps{
				Chaintracks:  svc.Chaintracks,
				ArcadeClient: svc.ArcadeClient,
			})
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
			RequestRestart: RequestRestart,
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
	if c.Paymail.Mode != paymail.ModeDisabled && c.Paymail.Mode != "" {
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
			sub, err := jbsync.NewSubscriber(subCfg, svc.Store.Store, svc.ConfigStore, svc.Chaintracks, svc.JungleBus, logger)
			if err != nil {
				return nil, fmt.Errorf("failed to create bsv21 subscriber: %w", err)
			}
			svc.JBSubscribers = append(svc.JBSubscribers, sub)
			logger.Info("BSV21 JungleBus subscriber initialized", "queue", "bsv21", "from_block", subCfg.FromBlock)
		}

		// BAP subscriber (if subscription_id configured)
		if svc.BAP != nil && c.BAP.Sync != nil && c.BAP.Sync.SubscriptionID != "" {
			subCfg := c.BAP.Sync.SubscriberConfig()
			sub, err := jbsync.NewSubscriber(subCfg, svc.Store.Store, svc.ConfigStore, svc.Chaintracks, svc.JungleBus, logger)
			if err != nil {
				return nil, fmt.Errorf("failed to create bap subscriber: %w", err)
			}
			svc.JBSubscribers = append(svc.JBSubscribers, sub)
			logger.Info("BAP JungleBus subscriber initialized", "queue", subCfg.QueueName, "from_block", subCfg.FromBlock)
		}

		// BSocial subscriber (if subscription_id configured)
		if svc.BSocial != nil && c.BSocial.Sync != nil && c.BSocial.Sync.SubscriptionID != "" {
			subCfg := c.BSocial.Sync.SubscriberConfig()
			sub, err := jbsync.NewSubscriber(subCfg, svc.Store.Store, svc.ConfigStore, svc.Chaintracks, svc.JungleBus, logger)
			if err != nil {
				return nil, fmt.Errorf("failed to create bsocial subscriber: %w", err)
			}
			svc.JBSubscribers = append(svc.JBSubscribers, sub)
			logger.Info("BSocial JungleBus subscriber initialized", "queue", subCfg.QueueName, "from_block", subCfg.FromBlock)
		}

		// OrdLock subscriber (if subscription_id configured)
		if svc.OrdLock != nil && c.OrdLock.Sync != nil && c.OrdLock.Sync.SubscriptionID != "" {
			subCfg := c.OrdLock.Sync.SubscriberConfig()
			sub, err := jbsync.NewSubscriber(subCfg, svc.Store.Store, svc.ConfigStore, svc.Chaintracks, svc.JungleBus, logger)
			if err != nil {
				return nil, fmt.Errorf("failed to create ordlock subscriber: %w", err)
			}
			svc.JBSubscribers = append(svc.JBSubscribers, sub)
			logger.Info("OrdLock JungleBus subscriber initialized", "queue", subCfg.QueueName, "from_block", subCfg.FromBlock)
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
			sub, err := jbsync.NewSubscriber(subCfg, svc.Store.Store, svc.ConfigStore, svc.Chaintracks, svc.JungleBus, logger)
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
	// Redirect root to base path
	if c.Server.BasePath != "" && c.Server.BasePath != "/" {
		app.Get("/", func(ctx *fiber.Ctx) error {
			return ctx.Redirect(c.Server.BasePath+"/home/", fiber.StatusMovedPermanently)
		})
	}

	// Octet-stream body limit for overlay /submit endpoints. Same source as
	// the outer Fiber BodyLimit so the two stay in sync.
	overlayBodyLimit := int64(ParseBodyLimit(c.Server.BodyLimit))

	// Create API group with base path
	api := app.Group(c.Server.BasePath)

	slog.Debug("registering routes", "basePath", c.Server.BasePath)

	// Always-on capabilities
	capabilities := []string{
		"beef", "pubsub", "txo", "ordfs", "indexer",
		"chaintracks", "arcade", "admin",
	}

	// Overlay capabilities from initialized services
	if svc.Overlay != nil {
		capabilities = append(capabilities, "overlay")
	}
	if svc.BAP != nil {
		capabilities = append(capabilities, "bap")
	}
	if svc.OPNS != nil {
		capabilities = append(capabilities, "opns")
	}
	if svc.BSV21 != nil {
		capabilities = append(capabilities, "bsv21")
	}
	if svc.BSocial != nil {
		capabilities = append(capabilities, "bsocial")
	}
	if svc.OrdLock != nil {
		capabilities = append(capabilities, "market")
	}
	if svc.Own != nil {
		capabilities = append(capabilities, "owner")
	}
	if svc.Paymail != nil {
		capabilities = append(capabilities, "paymail")
	}

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

	// Register TXO routes (mutable — spend status changes)
	if svc.TXO != nil && svc.TXO.Routes != nil {
		txoGroup := api.Group("/txo", httputil.NoStoreMiddleware())
		svc.TXO.Routes.Register(txoGroup)
	}

	// Register owner routes
	if svc.Own != nil && svc.Own.Routes != nil {
		prefix := c.Owner.Routes.Prefix
		if prefix == "" {
			prefix = "/owner"
		}
		ownGroup := api.Group(prefix, httputil.NoStoreMiddleware())
		svc.Own.Routes.Register(ownGroup)
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
		bsv21Group := api.Group(prefix, httputil.NoStoreMiddleware())
		svc.BSV21.Routes.Register(bsv21Group)

	}

	// Register BAP routes
	if svc.BAP != nil && svc.BAP.Routes != nil {
		prefix := c.BAP.Routes.Prefix
		if prefix == "" {
			prefix = "/bap"
		}
		bapGroup := api.Group(prefix, httputil.NoStoreMiddleware())
		svc.BAP.Routes.Register(bapGroup)
	}

	// Register BSocial routes
	if svc.BSocial != nil && svc.BSocial.Routes != nil {
		prefix := c.BSocial.Routes.Prefix
		if prefix == "" {
			prefix = "/bsocial"
		}
		bsocialGroup := api.Group(prefix, httputil.NoStoreMiddleware())
		svc.BSocial.Routes.Register(bsocialGroup)
	}

	// Register OPNS routes
	if svc.OPNS != nil && svc.OPNS.Routes != nil {
		prefix := c.OPNS.Routes.Prefix
		if prefix == "" {
			prefix = "/opns"
		}
		opnsGroup := api.Group(prefix, httputil.NoStoreMiddleware())
		svc.OPNS.Routes.Register(opnsGroup)
	}

	// Register OrdLock routes
	if svc.OrdLock != nil && svc.OrdLock.Routes != nil {
		prefix := c.OrdLock.Routes.Prefix
		if prefix == "" {
			prefix = "/market"
		}
		ordlockGroup := api.Group(prefix, httputil.NoStoreMiddleware())
		svc.OrdLock.Routes.Register(ordlockGroup)
	}

	// Register per-module overlay routes
	if svc.BAP != nil && svc.BAP.OverlayRoutes != nil {
		bapOverlayGroup := api.Group("/bap/overlay", httputil.NoStoreMiddleware())
		svc.BAP.OverlayRoutes.Register(bapOverlayGroup, overlayBodyLimit)
	}
	if svc.BSocial != nil && svc.BSocial.OverlayRoutes != nil {
		bsocialOverlayGroup := api.Group("/bsocial/overlay", httputil.NoStoreMiddleware())
		svc.BSocial.OverlayRoutes.Register(bsocialOverlayGroup, overlayBodyLimit)
	}
	if svc.OPNS != nil && svc.OPNS.OverlayRoutes != nil {
		opnsOverlayGroup := api.Group("/opns/overlay", httputil.NoStoreMiddleware())
		svc.OPNS.OverlayRoutes.Register(opnsOverlayGroup, overlayBodyLimit)
	}
	if svc.OrdLock != nil && svc.OrdLock.OverlayRoutes != nil {
		ordlockOverlayGroup := api.Group("/market/overlay", httputil.NoStoreMiddleware())
		svc.OrdLock.OverlayRoutes.Register(ordlockOverlayGroup, overlayBodyLimit)
	}
	if svc.BSV21 != nil && svc.BSV21.OverlayRoutes != nil {
		bsv21OverlayGroup := api.Group("/bsv21/overlay", httputil.NoStoreMiddleware())
		svc.BSV21.OverlayRoutes.Register(bsv21OverlayGroup, overlayBodyLimit)
	}
	// Register ORDFS routes
	if svc.ORDFS != nil && svc.ORDFS.Routes != nil {
		prefix := c.ORDFS.Routes.Prefix
		if prefix == "" {
			prefix = "/ordfs"
		}
		ordfsGroup := api.Group(prefix)
		svc.ORDFS.Routes.Register(ordfsGroup)

		// Also register content at root level for compatibility with ordfs protocol
		contentGroup := app.Group("/content")
		svc.ORDFS.Routes.RegisterContent(contentGroup)
	}

	// Register Chaintracks routes (block headers, chain tip, etc.)
	if svc.ChaintracksRoutes != nil {
		blockGroup := api.Group("/chaintracks")
		svc.ChaintracksRoutes.Register(blockGroup)
		slog.Debug("registered chaintracks routes", "prefix", "/chaintracks")
	}

	// Register external arcade broadcast routes (POST /tx, GET /tx/:txid)
	if svc.BroadcastRoutes != nil {
		txGroup := api.Group("/tx")
		svc.BroadcastRoutes.Register(txGroup)
		slog.Debug("registered broadcast routes", "prefix", "/tx")
	}

	// Register /.well-known/auth at app root for BRC-103/104 handshake.
	// The auth middleware intercepts handshake requests before they reach the
	// no-op; a 404 is only returned for non-handshake requests hitting this path.
	if svc.AuthMiddleware != nil {
		noOp := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})
		app.All("/.well-known/auth", adaptor.HTTPHandler(svc.AuthMiddleware.HTTPHandler(noOp)))
		slog.Debug("registered /.well-known/auth handshake route")
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
			httputil.PrivateNoStoreMiddleware(),
			svc.AuthMiddleware.Handler(),
			auth.AdminGuard(svc.ConfigStore, c.Auth.AllowUnauthenticated, slog.Default()),
		)
		publicGroup := api.Group(prefix, httputil.PrivateNoStoreMiddleware())
		svc.Admin.Routes.Register(guardedGroup, publicGroup, svc.AuthMiddleware.Handler())
		slog.Debug("registered admin routes", "prefix", prefix)
	}

	// Register Sweep UI routes (no auth required)
	if svc.Sweep != nil && svc.Sweep.Routes != nil {
		prefix := c.Sweep.Routes.Prefix
		if prefix == "" {
			prefix = "/sweep"
		}
		sweepGroup := api.Group(prefix)
		svc.Sweep.Routes.Register(sweepGroup)
		slog.Debug("registered sweep routes", "prefix", prefix)
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
		slog.Debug("registered paymail routes", "prefix", fullPrefix)

		// Register /.well-known/bsvalias at app root for capability discovery
		svc.Paymail.Routes.RegisterWellKnown(app)
		slog.Debug("registered paymail .well-known/bsvalias route")
	}

	// Health check endpoint
	api.Get("/health", handleHealth(svc.Chaintracks))

	// Capabilities endpoint - returns list of enabled services
	api.Get("/capabilities", handleCapabilities(capabilities))

	// Setup API documentation routes
	registerDocsRoutes(app)

	// Register Landing page routes (no auth required)
	if svc.Landing != nil && svc.Landing.Routes != nil {
		prefix := c.Landing.Routes.Prefix
		if prefix == "" {
			prefix = "/home"
		}
		landingGroup := api.Group(prefix)
		svc.Landing.Routes.Register(landingGroup)

		// Redirect base path to landing page
		api.Get("/", func(c *fiber.Ctx) error {
			return c.Redirect(c.Path()+prefix+"/", fiber.StatusTemporaryRedirect)
		})

		slog.Debug("registered landing routes", "prefix", prefix)
	}
}

// handleHealth returns the health status
// @Summary Health check
// @Description Returns health status with version, uptime, and block height
// @Tags system
// @Produce json
// @Success 200 {object} map[string]interface{} "status, version, uptime, height"
// @Router /health [get]
func handleHealth(ct chaintracks.Chaintracks) fiber.Handler {
	return func(c *fiber.Ctx) error {
		resp := fiber.Map{
			"status":  "ok",
			"version": Version,
			"uptime":  int(time.Since(startTime).Seconds()),
		}
		if ct != nil {
			height := ct.GetHeight(c.Context())
			if height > 0 {
				resp["height"] = height
			}
		}
		return c.JSON(resp)
	}
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

	// Start EventBridges (PubSub → overlay queues + direct submit)
	if svc.PubSub != nil && svc.Beef != nil {
		if svc.BAP != nil && svc.BAP.Sync != nil {
			bridge := overlay.NewEventBridge(&overlay.EventBridgeConfig{
				PubSub:   svc.PubSub.PubSub,
				Store:    svc.Store.Store,
				Patterns: []string{"bap:*"},
				QueueFunc: func(ev pubsub.Event) string {
					return string(txo.KeyQueue("bap"))
				},
				Logger:       logger,
				Engine:       svc.BAP.Engine,
				BeefStorage:  svc.Beef.Storage,
				SubmitBuffer: 64,
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
				Logger:       logger,
				Engine:       svc.BSocial.Engine,
				BeefStorage:  svc.Beef.Storage,
				SubmitBuffer: 64,
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
				Logger:       logger,
				Engine:       svc.OPNS.Engine,
				BeefStorage:  svc.Beef.Storage,
				SubmitBuffer: 64,
			})
			if err := bridge.Start(ctx); err != nil {
				logger.Error("failed to start OPNS event bridge", "error", err)
			}
		}
		if svc.OrdLock != nil && svc.OrdLock.Sync != nil {
			bridge := overlay.NewEventBridge(&overlay.EventBridgeConfig{
				PubSub:   svc.PubSub.PubSub,
				Store:    svc.Store.Store,
				Patterns: []string{"ordlock", "spend:ordlock"},
				QueueFunc: func(ev pubsub.Event) string {
					return string(txo.KeyQueue(ordlockpkg.QueueName))
				},
				Logger:       logger,
				Engine:       svc.OrdLock.Engine,
				BeefStorage:  svc.Beef.Storage,
				SubmitBuffer: 64,
			})
			if err := bridge.Start(ctx); err != nil {
				logger.Error("failed to start OrdLock event bridge", "error", err)
			}
		}
		if svc.BSV21 != nil && svc.BSV21.Sync != nil {
			bridge := overlay.NewEventBridge(&overlay.EventBridgeConfig{
				PubSub:   svc.PubSub.PubSub,
				Store:    svc.Store.Store,
				Patterns: []string{"bsv21:*"},
				QueueFunc: func(ev pubsub.Event) string {
					tokenId := strings.TrimPrefix(ev.Topic, "bsv21:")
					if tokenId == "" {
						return ""
					}
					return "q:tm_" + tokenId
				},
				Logger:       logger,
				Engine:       svc.BSV21.Engine,
				BeefStorage:  svc.Beef.Storage,
				SubmitBuffer: 64,
			})
			if err := bridge.Start(ctx); err != nil {
				logger.Error("failed to start BSV21 event bridge", "error", err)
			}
		}
	}

	// Start the always-on arcade SSE consumer (event broker)
	if svc.ArcadeBroker != nil {
		go svc.ArcadeBroker.Run(ctx)
		logger.Info("started arcade event broker")
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
	if svc.OrdLock != nil && svc.OrdLock.Sync != nil {
		go func() {
			if err := svc.OrdLock.Sync.Start(ctx); err != nil {
				logger.Error("OrdLock sync error", "error", err)
			}
		}()
		logger.Info("started OrdLock overlay sync")
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

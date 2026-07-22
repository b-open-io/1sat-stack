package node

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/b-open-io/1sat-stack/admin"
	"github.com/b-open-io/1sat-stack/landing"
	"github.com/b-open-io/1sat-stack/pkg/auth"
	"github.com/b-open-io/1sat-stack/pkg/bap"
	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/bsocial"
	"github.com/b-open-io/1sat-stack/pkg/bsv21"
	configpkg "github.com/b-open-io/1sat-stack/pkg/config"
	"github.com/b-open-io/1sat-stack/pkg/gateway"
	"github.com/b-open-io/1sat-stack/pkg/indexer"
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
	chaintracksconfig "github.com/bsv-blockchain/go-chaintracks/config"
	p2p "github.com/bsv-blockchain/go-teranode-p2p-client"
	"github.com/spf13/viper"
)

// Config holds the complete server configuration
type Config struct {
	// DataDir is the base directory for all data files.
	// Resolved at startup from --data-dir flag, ONESAT_DATA_DIR env, or default ~/.1sat/
	// Not a Viper field — set directly before Initialize.
	DataDir string `mapstructure:"-"`

	// LogStore is set by main before Initialize, passed to admin for log queries.
	LogStore *logging.SQLiteHandler `mapstructure:"-"`

	// RequestRestart is set by main before Initialize, passed to admin to
	// signal the process to re-exec itself.
	RequestRestart func() `mapstructure:"-"`

	// Mode selects which services this process runs: "all" (default) runs
	// every enabled service; a comma-separated list (e.g. "index,opns")
	// runs exactly the named services.
	Mode string `mapstructure:"mode"`

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
	Queue  queue.Config  `mapstructure:"queue"`
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

	// Gateway reverse proxy (multi-process deployments only)
	Gateway gateway.Config `mapstructure:"gateway"`

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

// SetDefaults configures viper defaults for all settings
func (c *Config) SetDefaults(v *viper.Viper) {
	// Mode default
	v.SetDefault("mode", "all")

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
	c.Queue.SetDefaults(v, "queue")
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
	c.Gateway.SetDefaults(v, "gateway")
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
	c.Queue.Store.Badger.Path = c.resolvePath(c.Queue.Store.Badger.Path)
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
		case "http":
			cc.HTTP.URL = item["url"]
		case "store", "junglebus":
			// No additional config needed
		}
		chain = append(chain, cc)
	}
	return chain, nil
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

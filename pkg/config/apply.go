package config

import (
	"context"
	"log/slog"
	"strconv"
)

// RuntimeConfig holds all operational settings read from the config store.
// The config store is the sole source of truth for these values.
// Only data_dir and private key remain outside the config store.
type RuntimeConfig struct {
	// Setup
	SetupComplete bool
	AuthMode      string // "local" or "authenticated"

	// Server
	ServerPort     int
	ServerHost     string
	ServerBasePath string
	ServerBodyLimit string

	// Network
	Network string

	// Logging
	LogLevel string

	// Auth
	AuthAPIKey      string
	AuthSessionPath string
	AuthSessionTTL  string

	// Store
	StoreMode     string // "embedded" or "disabled"
	StoreProvider string // "badger" or "redis"
	StoreBadgerPath string
	StoreRedisURL   string

	// Wallet
	WalletMode        string // "embedded" or "disabled"
	WalletName        string
	WalletDBEngine    string // "sqlite" or "postgres"
	WalletSQLitePath  string
	WalletPostgresURL string

	// Chaintracks
	ChaintracksMode string // "embedded" or "remote"
	ChaintracksPath string
	ChaintracksURL  string

	// Arcade
	ArcadeMode string // "embedded" or "remote"
	ArcadePath string
	ArcadeURL  string

	// JungleBus
	JungleBusURL   string
	JungleBusToken string

	// Indexer
	IndexerMode              string // "embedded" or "disabled"
	IndexerSyncEnabled       bool
	IndexerSyncSubscriptionIDs string // comma-separated
	IndexerSyncConcurrency   int
	IndexerSyncBatchSize     int
	IndexerSyncMempool       bool

	// Overlay engine (shared)
	OverlayStoragePath    string
	OverlayStorageBackend string // "sqlite" or "postgres"
	OverlayP2PEnabled     bool
	OverlayP2PPort        string
	OverlayP2PDHTMode     string
	OverlayP2PBootstrapPeers string

	// BAP overlay
	BAPEnabled          bool
	BAPSyncSubID        string
	BAPSyncConcurrency  int
	BAPSyncBatchSize    int

	// BSocial overlay
	BSocialEnabled          bool
	BSocialSyncSubID        string
	BSocialSyncConcurrency  int
	BSocialSyncBatchSize    int

	// OPNS overlay
	OPNSEnabled          bool
	OPNSCrawlConcurrency int

	// OrdLock overlay
	OrdLockEnabled          bool
	OrdLockSyncSubID        string
	OrdLockSyncConcurrency  int
	OrdLockSyncBatchSize    int

	// BSV21
	BSV21Enabled          bool
	BSV21SyncSubID        string
	BSV21SyncBatchSize    int

	// ORDFS
	ORDFSEnabled  bool
	ORDFSRedisURL string

	// Owner
	OwnerMode string // "embedded" or "disabled"

	// Paymail
	PaymailMode   string // "enabled" or "disabled"
	PaymailDBPath string

	// MessageBox
	MessageBoxMode   string // "enabled" or "disabled"
	MessageBoxDBPath string

	// MongoDB
	MongoDBURL string
}

// LoadRuntimeConfig reads all operational settings from the config store.
func LoadRuntimeConfig(ctx context.Context, cs Store, logger *slog.Logger) (*RuntimeConfig, error) {
	rc := &RuntimeConfig{}

	// Setup
	rc.SetupComplete = getBool(ctx, cs, "setup.complete")
	rc.AuthMode = getString(ctx, cs, "auth.mode")

	// Server
	rc.ServerPort = getInt(ctx, cs, "server.port")
	rc.ServerHost = getString(ctx, cs, "server.host")
	rc.ServerBasePath = getString(ctx, cs, "server.base_path")
	rc.ServerBodyLimit = getString(ctx, cs, "server.body_limit")

	// Network
	rc.Network = getString(ctx, cs, "network")

	// Logging
	rc.LogLevel = getString(ctx, cs, "logging.level")

	// Auth
	rc.AuthMode = getString(ctx, cs, "auth.mode")
	rc.AuthAPIKey = getString(ctx, cs, "auth.api_key")
	rc.AuthSessionPath = getString(ctx, cs, "auth.session_path")
	rc.AuthSessionTTL = getString(ctx, cs, "auth.session_ttl")

	// Store
	rc.StoreMode = getString(ctx, cs, "store.mode")
	rc.StoreProvider = getString(ctx, cs, "store.provider")
	rc.StoreBadgerPath = getString(ctx, cs, "store.badger.path")
	rc.StoreRedisURL = getString(ctx, cs, "store.redis.url")

	// Wallet
	rc.WalletMode = getString(ctx, cs, "wallet.mode")
	rc.WalletName = getString(ctx, cs, "wallet.name")
	rc.WalletDBEngine = getString(ctx, cs, "wallet.db.engine")
	rc.WalletSQLitePath = getString(ctx, cs, "wallet.db.sqlite.connection_string")
	rc.WalletPostgresURL = getString(ctx, cs, "wallet.postgres_connection_string")

	// Chaintracks
	rc.ChaintracksMode = getString(ctx, cs, "chaintracks.mode")
	rc.ChaintracksPath = getString(ctx, cs, "chaintracks.path")
	rc.ChaintracksURL = getString(ctx, cs, "chaintracks.url")

	// Arcade
	rc.ArcadeMode = getString(ctx, cs, "arcade.mode")
	rc.ArcadePath = getString(ctx, cs, "arcade.path")
	rc.ArcadeURL = getString(ctx, cs, "arcade.url")

	// JungleBus
	rc.JungleBusURL = getString(ctx, cs, "junglebus.url")
	rc.JungleBusToken = getString(ctx, cs, "junglebus.token")

	// Indexer
	rc.IndexerMode = getString(ctx, cs, "indexer.mode")
	rc.IndexerSyncEnabled = getBool(ctx, cs, "indexer.sync.enabled")
	rc.IndexerSyncSubscriptionIDs = getString(ctx, cs, "indexer.sync.subscription_ids")
	rc.IndexerSyncConcurrency = getInt(ctx, cs, "indexer.sync.concurrency")
	rc.IndexerSyncBatchSize = getInt(ctx, cs, "indexer.sync.batch_size")
	rc.IndexerSyncMempool = getBool(ctx, cs, "indexer.sync.mempool")

	// Overlay engine
	rc.OverlayStoragePath = getString(ctx, cs, "overlay.engine.storage_path")
	rc.OverlayStorageBackend = getString(ctx, cs, "overlay.engine.storage")
	rc.OverlayP2PEnabled = getBool(ctx, cs, "overlay.engine.p2p.enabled")
	rc.OverlayP2PPort = getString(ctx, cs, "overlay.engine.p2p.port")
	rc.OverlayP2PDHTMode = getString(ctx, cs, "overlay.engine.p2p.dht_mode")
	rc.OverlayP2PBootstrapPeers = getString(ctx, cs, "overlay.engine.p2p.bootstrap_peers")

	// BAP
	rc.BAPEnabled = getBool(ctx, cs, "overlay.bap.enabled")
	rc.BAPSyncSubID = getString(ctx, cs, "overlay.bap.sub_id")
	rc.BAPSyncConcurrency = getInt(ctx, cs, "overlay.bap.concurrency")
	rc.BAPSyncBatchSize = getInt(ctx, cs, "overlay.bap.batch_size")

	// BSocial
	rc.BSocialEnabled = getBool(ctx, cs, "overlay.bsocial.enabled")
	rc.BSocialSyncSubID = getString(ctx, cs, "overlay.bsocial.sub_id")
	rc.BSocialSyncConcurrency = getInt(ctx, cs, "overlay.bsocial.concurrency")
	rc.BSocialSyncBatchSize = getInt(ctx, cs, "overlay.bsocial.batch_size")

	// OPNS
	rc.OPNSEnabled = getBool(ctx, cs, "overlay.opns.enabled")
	rc.OPNSCrawlConcurrency = getInt(ctx, cs, "overlay.opns.concurrency")

	// OrdLock
	rc.OrdLockEnabled = getBool(ctx, cs, "overlay.ordlock.enabled")
	rc.OrdLockSyncSubID = getString(ctx, cs, "overlay.ordlock.sub_id")
	rc.OrdLockSyncConcurrency = getInt(ctx, cs, "overlay.ordlock.concurrency")
	rc.OrdLockSyncBatchSize = getInt(ctx, cs, "overlay.ordlock.batch_size")

	// BSV21
	rc.BSV21Enabled = getBool(ctx, cs, "overlay.bsv21.enabled")
	rc.BSV21SyncSubID = getString(ctx, cs, "overlay.bsv21.sub_id")
	rc.BSV21SyncBatchSize = getInt(ctx, cs, "overlay.bsv21.batch_size")

	// ORDFS
	rc.ORDFSEnabled = getBool(ctx, cs, "ordfs.enabled")
	rc.ORDFSRedisURL = getString(ctx, cs, "ordfs.redis.url")

	// Owner
	if getBool(ctx, cs, "owner.enabled") {
		rc.OwnerMode = "embedded"
	} else if getString(ctx, cs, "owner.enabled") == "false" {
		rc.OwnerMode = "disabled"
	}

	// Paymail
	rc.PaymailMode = getString(ctx, cs, "paymail.mode")
	rc.PaymailDBPath = getString(ctx, cs, "paymail.db_path")

	// MessageBox
	rc.MessageBoxMode = getString(ctx, cs, "messagebox.mode")
	rc.MessageBoxDBPath = getString(ctx, cs, "messagebox.db_path")

	// MongoDB
	rc.MongoDBURL = getString(ctx, cs, "mongodb.url")

	if rc.SetupComplete {
		logger.Info("runtime config loaded from config store")
	} else {
		logger.Info("config store empty — first run, wizard mode")
	}

	return rc, nil
}

func getString(ctx context.Context, cs Store, key string) string {
	val, err := cs.Get(ctx, key)
	if err != nil {
		return ""
	}
	return val
}

func getBool(ctx context.Context, cs Store, key string) bool {
	return getString(ctx, cs, key) == "true"
}

func getInt(ctx context.Context, cs Store, key string) int {
	s := getString(ctx, cs, key)
	if s == "" {
		return 0
	}
	v, _ := strconv.Atoi(s)
	return v
}

func getUint64(ctx context.Context, cs Store, key string) uint64 {
	s := getString(ctx, cs, key)
	if s == "" {
		return 0
	}
	v, _ := strconv.ParseUint(s, 10, 64)
	return v
}

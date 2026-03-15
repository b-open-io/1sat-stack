package overlay

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	overlaystorage "github.com/b-open-io/1sat-stack/pkg/overlay/storage"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/chaintracker"
	"github.com/spf13/viper"
)

// Mode constants for overlay configuration
const (
	ModeDisabled = "disabled"
	ModeEmbedded = "embedded"
)

// Config holds overlay engine configuration
type Config struct {
	Mode           string       `mapstructure:"mode"`
	StoragePath    string       `mapstructure:"storage_path"` // Base path for per-topic SQLite databases
	TopicWhitelist []string     `mapstructure:"topic_whitelist"`
	TopicBlacklist []string     `mapstructure:"topic_blacklist"`
	Routes         RoutesConfig `mapstructure:"routes"`
	P2P            P2PConfig    `mapstructure:"p2p"`
}

// RoutesConfig holds route configuration
type RoutesConfig struct {
	Enabled          bool   `mapstructure:"enabled"`
	Prefix           string `mapstructure:"prefix"`
	AdminBearerToken string `mapstructure:"admin_bearer_token"`
	ARCAPIKey        string `mapstructure:"arc_api_key"`
	ARCCallbackToken string `mapstructure:"arc_callback_token"`
}

// SetDefaults configures viper defaults for overlay settings
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	v.SetDefault(prefix+".mode", ModeDisabled)
	v.SetDefault(prefix+".storage_path", "~/.1sat/overlay")
	v.SetDefault(prefix+".topic_whitelist", []string{})
	v.SetDefault(prefix+".topic_blacklist", []string{})
	v.SetDefault(prefix+".routes.enabled", true)
	v.SetDefault(prefix+".routes.prefix", "/overlay")
	v.SetDefault(prefix+".p2p.enabled", false)
	v.SetDefault(prefix+".p2p.port", 9906)
	v.SetDefault(prefix+".p2p.dht_mode", "off")
	v.SetDefault(prefix+".p2p.storage_path", "~/.1sat/overlay-p2p")
}

// InitializeDeps holds dependencies required for overlay initialization
type InitializeDeps struct {
	OutputStore  *txo.OutputStore
	ChainTracker chaintracker.ChainTracker
	Store        store.Store   // For remote config and queue operations
	BeefStorage  *beef.Storage // For BEEF remote creation
	P2PBus       *P2PBus       // For overlay P2P broadcast (optional)
}

// Initialize creates overlay services from configuration
func (c *Config) Initialize(ctx context.Context, logger *slog.Logger, deps *InitializeDeps) (*Services, error) {
	if c.Mode == ModeDisabled {
		return nil, nil
	}

	if logger == nil {
		logger = slog.Default()
	}

	// Expand ~ in storage path
	storagePath := c.StoragePath
	if strings.HasPrefix(storagePath, "~/") {
		home, _ := os.UserHomeDir()
		storagePath = filepath.Join(home, storagePath[2:])
	}
	if err := os.MkdirAll(storagePath, 0755); err != nil {
		return nil, fmt.Errorf("create overlay storage dir %s: %w", storagePath, err)
	}

	// Create per-topic SQLite factory and engine adapter
	sqliteFactory, err := overlaystorage.NewSQLiteFactory(filepath.Join(storagePath, "topic"))
	if err != nil {
		return nil, fmt.Errorf("create overlay storage factory: %w", err)
	}
	factory := sqliteFactory.Factory()
	adapter := overlaystorage.NewEngineAdapter(factory, deps.BeefStorage, sqliteFactory.TxTopicIndex())

	// Wire IngestTx callback if output store is available
	if deps.OutputStore != nil && deps.OutputStore.IngestTx != nil {
		ingestFn := deps.OutputStore.IngestTx
		adapter.IngestTx = func(ctx context.Context, tx *transaction.Transaction) error {
			return ingestFn(ctx, tx)
		}
	}

	svc := &Services{
		logger:      logger,
		Store:       deps.Store,
		beefStorage: deps.BeefStorage,
		factory:     factory,
	}

	// Create the overlay engine
	eng := engine.NewEngine(&engine.EngineConfig{
		Managers:       make(map[string]engine.TopicManager),
		LookupServices: make(map[string]engine.LookupService),
		Storage:        adapter,
		ChainTracker:   deps.ChainTracker,
	})
	svc.Engine = eng

	// Wire P2P bus if available
	if deps.P2PBus != nil {
		svc.P2P = deps.P2PBus
		eng.OnAdmission = deps.P2PBus.OnAdmission
	}

	// Create routes if enabled
	if c.Routes.Enabled {
		svc.Routes = NewRoutes(eng, &c.Routes, logger)
	}

	logger.Info("overlay engine initialized", "mode", c.Mode, "storage", storagePath)

	return svc, nil
}

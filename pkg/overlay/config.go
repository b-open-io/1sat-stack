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
	StoragePath    string       `mapstructure:"storage_path"`    // Base path for per-topic SQLite databases
	StorageBackend string       `mapstructure:"storage_backend"` // "sqlite" (default) or "postgres"
	StorageURL     string       `mapstructure:"storage_url"`     // PostgreSQL connection string
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
	v.SetDefault(prefix+".storage_backend", "sqlite")
	v.SetDefault(prefix+".storage_url", "")
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

// ModuleDeps holds the shared infrastructure that overlay modules use to create their own engines.
type ModuleDeps struct {
	Factory      overlaystorage.Factory
	TxTopicIndex overlaystorage.TxTopicIndexer
	BeefStorage  *beef.Storage
	ChainTracker chaintracker.ChainTracker
	Store        store.Store
	IngestTx     overlaystorage.IngestTxFunc
	RoutesConfig *RoutesConfig
	P2PBus       *P2PBus
}

// NewModuleEngine creates an engine.Engine for a single overlay module.
func NewModuleEngine(deps *ModuleDeps, managers map[string]engine.TopicManager, lookups map[string]engine.LookupService) *engine.Engine {
	adapter := overlaystorage.NewEngineAdapter(deps.Factory, deps.BeefStorage, deps.TxTopicIndex)
	if deps.IngestTx != nil {
		adapter.IngestTx = deps.IngestTx
	}

	eng := engine.NewEngine(&engine.Config{
		Managers:       managers,
		LookupServices: lookups,
		Storage:        adapter,
		ChainTracker:   deps.ChainTracker,
	})

	if deps.P2PBus != nil {
		eng.OnAdmission = deps.P2PBus.OnAdmission
	}

	return eng
}

// Initialize creates shared overlay infrastructure (storage factory, TxTopicIndex)
// without creating an engine. Each module creates its own engine via NewModuleEngine.
func (c *Config) Initialize(ctx context.Context, logger *slog.Logger, deps *InitializeDeps) (*Services, error) {
	if c.Mode == ModeDisabled {
		return nil, nil
	}

	if logger == nil {
		logger = slog.Default()
	}

	var factory overlaystorage.Factory
	var txTopicIndex overlaystorage.TxTopicIndexer
	var storagePath string

	switch c.StorageBackend {
	case "postgres":
		if c.StorageURL == "" {
			return nil, fmt.Errorf("overlay storage_url is required when storage_backend is postgres")
		}
		pgFactory, err := overlaystorage.NewPostgresFactory(c.StorageURL)
		if err != nil {
			return nil, fmt.Errorf("create postgres overlay storage: %w", err)
		}
		factory = pgFactory.Factory()
		txTopicIndex = pgFactory.TxTopicIndex()
	default:
		storagePath = c.StoragePath
		if strings.HasPrefix(storagePath, "~/") {
			home, _ := os.UserHomeDir()
			storagePath = filepath.Join(home, storagePath[2:])
		}
		if err := os.MkdirAll(storagePath, 0755); err != nil {
			return nil, fmt.Errorf("create overlay storage dir %s: %w", storagePath, err)
		}
		sqliteFactory, err := overlaystorage.NewSQLiteFactory(filepath.Join(storagePath, "topic"))
		if err != nil {
			return nil, fmt.Errorf("create overlay storage factory: %w", err)
		}
		factory = sqliteFactory.Factory()
		txTopicIndex = sqliteFactory.TxTopicIndex()
	}

	var ingestTx overlaystorage.IngestTxFunc
	if deps.OutputStore != nil && deps.OutputStore.IngestTx != nil {
		ingestFn := deps.OutputStore.IngestTx
		ingestTx = func(ctx context.Context, tx *transaction.Transaction) error {
			return ingestFn(ctx, tx)
		}
	}

	svc := &Services{
		logger:       logger,
		Store:        deps.Store,
		beefStorage:  deps.BeefStorage,
		factory:      factory,
		txTopicIndex: txTopicIndex,
		ModuleDeps: &ModuleDeps{
			Factory:      factory,
			TxTopicIndex: txTopicIndex,
			BeefStorage:  deps.BeefStorage,
			ChainTracker: deps.ChainTracker,
			Store:        deps.Store,
			IngestTx:     ingestTx,
			RoutesConfig: &c.Routes,
			P2PBus:       deps.P2PBus,
		},
	}

	if deps.P2PBus != nil {
		svc.P2P = deps.P2PBus
	}

	logger.Info("overlay infrastructure initialized", "mode", c.Mode, "backend", c.StorageBackend, "storage", storagePath)

	return svc, nil
}

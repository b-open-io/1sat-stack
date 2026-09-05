package ecosystemalias

import (
	"context"
	"fmt"
	"log/slog"

	configpkg "github.com/b-open-io/1sat-stack/pkg/config"
	"github.com/b-open-io/1sat-stack/pkg/overlay"
	overlaystorage "github.com/b-open-io/1sat-stack/pkg/overlay/storage"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/spf13/viper"
)

const (
	ModeDisabled = "disabled"
	ModeEmbedded = "embedded"
	QueueName    = "ecosystemalias"
)

// Config holds ecosystem-alias overlay configuration.
type Config struct {
	Mode     string                     `mapstructure:"mode"`
	LogLevel string                     `mapstructure:"log_level"`
	Sync     *overlay.OverlaySyncConfig `mapstructure:"sync"`
	Routes   RoutesConfig               `mapstructure:"routes"`
}

// RoutesConfig controls the module's standard overlay HTTP surface.
type RoutesConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Prefix  string `mapstructure:"prefix"`
}

// SetDefaults configures ecosystem-alias defaults.
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	if prefix != "" {
		p = prefix + "."
	}

	v.SetDefault(p+"mode", ModeDisabled)
	v.SetDefault(p+"sync.enabled", false)
	v.SetDefault(p+"sync.subscription_id", "")
	v.SetDefault(p+"sync.queue_name", QueueName)
	v.SetDefault(p+"sync.from_block", 0)
	v.SetDefault(p+"sync.concurrency", 8)
	v.SetDefault(p+"sync.batch_size", 1000)
	v.SetDefault(p+"sync.reorg_depth", 6)
	v.SetDefault(p+"sync.resolve_dependencies", false)
	v.SetDefault(p+"routes.enabled", true)
	v.SetDefault(p+"routes.prefix", "/ecosystemalias")
}

// Services holds initialized ecosystem-alias services.
type Services struct {
	Engine        *engine.Engine
	Lookup        *LookupService
	TopicManager  *TopicManager
	ClaimStore    ClaimStore
	Sync          *overlay.OverlaySync
	OverlayRoutes *overlay.Routes
}

// Initialize creates the ecosystem-alias engine and its durable, topic-scoped
// claim store. The shared overlay factory retains ownership of the database.
func (c *Config) Initialize(
	ctx context.Context,
	logger *slog.Logger,
	deps *overlay.ModuleDeps,
) (*Services, error) {
	if c.Mode == ModeDisabled {
		return nil, nil
	}
	if logger == nil {
		logger = slog.Default()
	}

	switch c.Mode {
	case ModeEmbedded:
		if c.Routes.Enabled {
			prefix, err := configpkg.NormalizeEcosystemAliasRoutePrefix(c.Routes.Prefix)
			if err != nil {
				return nil, fmt.Errorf("invalid ecosystem-alias route prefix: %w", err)
			}
			c.Routes.Prefix = prefix
		}
		if c.Sync != nil && c.Sync.Enabled {
			if err := configpkg.ValidateEcosystemAliasConcurrency(c.Sync.Concurrency); err != nil {
				return nil, err
			}
			if err := configpkg.ValidateEcosystemAliasBatchSize(c.Sync.BatchSize); err != nil {
				return nil, err
			}
		}
		if deps == nil || deps.Factory == nil {
			return nil, fmt.Errorf("overlay ModuleDeps with Factory is required for ecosystem-alias")
		}
		if deps.BeefStorage == nil {
			return nil, fmt.Errorf("overlay ModuleDeps with BEEF storage is required for ecosystem-alias lookup hydration")
		}
		topicStorage, err := deps.Factory(TopicName)
		if err != nil {
			return nil, fmt.Errorf("failed to get ecosystem-alias topic storage: %w", err)
		}
		if topicStorage == nil || topicStorage.DB() == nil {
			return nil, fmt.Errorf("ecosystem-alias topic storage has no database")
		}

		claimStore := NewSQLStore(topicStorage.DB(), topicStorage.TopicID())
		if err := claimStore.ensureSchema(); err != nil {
			return nil, fmt.Errorf("failed to initialize ecosystem-alias claim store: %w", err)
		}
		outputLoader := overlaystorage.NewEngineAdapter(deps.Factory, deps.BeefStorage, deps.TxTopicIndex)
		lookupService := NewLookupService(claimStore, outputLoader)
		topicManager := &TopicManager{}
		eng := overlay.NewModuleEngine(deps,
			map[string]engine.TopicManager{TopicName: topicManager},
			map[string]engine.LookupService{LookupName: lookupService},
		)

		svc := &Services{
			Engine:       eng,
			Lookup:       lookupService,
			TopicManager: topicManager,
			ClaimStore:   claimStore,
		}
		if c.Routes.Enabled && deps.RoutesConfig != nil && deps.RoutesConfig.Enabled {
			svc.OverlayRoutes = overlay.NewRoutes(eng, deps.RoutesConfig, logger)
		}
		return svc, nil

	default:
		return nil, fmt.Errorf("unknown ecosystem-alias mode: %s", c.Mode)
	}
}

// Close stops optional background work. Storage is owned by ModuleDeps.
func (s *Services) Close() error {
	if s == nil {
		return nil
	}
	if s.Sync != nil {
		s.Sync.Stop()
	}
	return nil
}

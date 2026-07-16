package collection

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/b-open-io/1sat-stack/pkg/overlay"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/spf13/viper"
)

// Mode constants
const (
	ModeDisabled = "disabled"
	ModeEmbedded = "embedded"
)

// Config holds collection overlay configuration (library defaults).
// Stand-alone collection-overlay owns product-level config; this is for
// embedding or composing the stack package.
type Config struct {
	Mode     string       `mapstructure:"mode"`
	LogLevel string       `mapstructure:"log_level"`
	Routes   RoutesConfig `mapstructure:"routes"`
	// CollectionIDs optionally pre-registers per-collection item topics
	// at Initialize time. Additional collections can be registered via
	// Services.RegisterCollection.
	CollectionIDs []string `mapstructure:"collection_ids"`
}

// RoutesConfig holds HTTP route configuration.
type RoutesConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Prefix  string `mapstructure:"prefix"`
}

// SetDefaults sets viper defaults for collection configuration.
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	if prefix != "" {
		p = prefix + "."
	}
	v.SetDefault(p+"mode", ModeDisabled)
	v.SetDefault(p+"routes.enabled", true)
	v.SetDefault(p+"routes.prefix", "/collection")
	v.SetDefault(p+"collection_ids", []string{})
}

// Services holds initialized collection overlay services.
type Services struct {
	Engine           *engine.Engine
	Lookup           *LookupService
	DiscoveryManager *DiscoveryTopicManager
	Routes           *Routes
	OverlayRoutes    *overlay.Routes
	logger           *slog.Logger

	items sync.Map // collectionId -> *ItemTopicManager
}

// Initialize creates collection services from configuration.
// Library-clean: no config.db; requires overlay ModuleDeps with Factory.
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
	logger = logger.With("component", "collection")

	switch c.Mode {
	case ModeEmbedded:
		if deps == nil || deps.Factory == nil {
			return nil, fmt.Errorf("overlay ModuleDeps with Factory is required for collection")
		}

		lookupSvc := NewLookupService(deps.Factory)
		discovery := NewDiscoveryTopicManager(logger)

		managers := map[string]engine.TopicManager{
			DiscoveryTopic: discovery,
		}
		for _, id := range c.CollectionIDs {
			if id == "" {
				continue
			}
			managers[ItemTopic(id)] = NewItemTopicManager(id, logger)
		}

		eng := overlay.NewModuleEngine(deps,
			managers,
			map[string]engine.LookupService{
				LookupName: lookupSvc,
			},
		)

		svc := &Services{
			Engine:           eng,
			Lookup:           lookupSvc,
			DiscoveryManager: discovery,
			logger:           logger,
		}
		for _, id := range c.CollectionIDs {
			if id == "" {
				continue
			}
			svc.items.Store(id, managers[ItemTopic(id)])
		}

		if c.Routes.Enabled {
			svc.Routes = NewRoutes(lookupSvc, logger)
		}
		if deps.RoutesConfig != nil && deps.RoutesConfig.Enabled {
			svc.OverlayRoutes = overlay.NewRoutes(eng, deps.RoutesConfig, logger)
		}

		return svc, nil

	default:
		return nil, fmt.Errorf("unknown collection mode: %s", c.Mode)
	}
}

// RegisterCollection registers a per-collection item topic on the engine.
// Safe to call multiple times for the same id (no-op if already registered).
func (s *Services) RegisterCollection(collectionID string) error {
	if s == nil || s.Engine == nil {
		return fmt.Errorf("collection services not initialized")
	}
	if collectionID == "" {
		return fmt.Errorf("collectionId is required")
	}
	if _, ok := s.items.Load(collectionID); ok {
		return nil
	}
	tm := NewItemTopicManager(collectionID, s.logger)
	s.items.Store(collectionID, tm)
	s.Engine.RegisterTopicManager(ItemTopic(collectionID), tm)
	s.logger.Info("registered collection item topic", "collectionId", collectionID, "topic", ItemTopic(collectionID))
	return nil
}

// Close releases collection services.
func (s *Services) Close() error {
	return nil
}

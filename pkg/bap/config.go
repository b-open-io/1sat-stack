package bap

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/overlay"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/spf13/viper"
)

// Mode constants
const (
	ModeDisabled = "disabled"
	ModeEmbedded = "embedded"
)

// Config holds BAP configuration.
type Config struct {
	Mode     string                     `mapstructure:"mode"`      // disabled, embedded
	LogLevel string                     `mapstructure:"log_level"` // debug, info, warn, error
	Sync     *overlay.OverlaySyncConfig `mapstructure:"sync"`
	Routes   RoutesConfig               `mapstructure:"routes"`
}

// RoutesConfig holds route configuration.
type RoutesConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Prefix  string `mapstructure:"prefix"`
}

// SetDefaults sets viper defaults for BAP configuration.
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	if prefix != "" {
		p = prefix + "."
	}

	v.SetDefault(p+"mode", ModeDisabled)
	v.SetDefault(p+"sync.enabled", false)
	v.SetDefault(p+"sync.subscription_id", "")
	v.SetDefault(p+"sync.queue_name", "bap")
	v.SetDefault(p+"sync.from_block", 575000)
	v.SetDefault(p+"sync.concurrency", 8)
	v.SetDefault(p+"sync.batch_size", 1000)
	v.SetDefault(p+"sync.reorg_depth", 6)
	v.SetDefault(p+"routes.enabled", true)
	v.SetDefault(p+"routes.prefix", "/bap")
}

// Services holds initialized BAP services.
type Services struct {
	Engine        *engine.Engine
	Lookup        *LookupService
	TopicManager  *TopicManager
	Sync          *overlay.OverlaySync
	Routes        *Routes
	OverlayRoutes *overlay.Routes
}

// Initialize creates BAP services from the configuration.
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
		if deps == nil || deps.Factory == nil {
			return nil, fmt.Errorf("overlay ModuleDeps with Factory is required for BAP")
		}
		ts, err := deps.Factory("tm_bap")
		if err != nil {
			return nil, fmt.Errorf("failed to get BAP topic storage: %w", err)
		}
		store := NewSQLStore(ts.DB(), ts.TopicID())
		lookupSvc := NewLookupService(store)
		topicManager := &TopicManager{}

		eng := overlay.NewModuleEngine(deps,
			map[string]engine.TopicManager{"tm_bap": topicManager},
			map[string]engine.LookupService{"bap": lookupSvc},
		)

		svc := &Services{
			Engine:       eng,
			Lookup:       lookupSvc,
			TopicManager: topicManager,
		}

		if c.Routes.Enabled {
			svc.Routes = NewRoutes(lookupSvc, logger)
		}

		if deps.RoutesConfig != nil && deps.RoutesConfig.Enabled {
			svc.OverlayRoutes = overlay.NewRoutes(eng, deps.RoutesConfig, logger)
		}

		return svc, nil

	default:
		return nil, fmt.Errorf("unknown bap mode: %s", c.Mode)
	}
}

// Close closes the BAP services.
func (s *Services) Close() error {
	if s.Sync != nil {
		s.Sync.Stop()
	}
	return nil
}

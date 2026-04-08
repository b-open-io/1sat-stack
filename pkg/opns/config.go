package opns

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

// Config holds OPNS configuration.
type Config struct {
	Mode     string                     `mapstructure:"mode"`      // disabled, embedded
	LogLevel string                     `mapstructure:"log_level"` // debug, info, warn, error
	Sync     *overlay.OverlaySyncConfig `mapstructure:"sync"`
	Crawl    CrawlConfig                `mapstructure:"crawl"`
	Routes   RoutesConfig               `mapstructure:"routes"`
}

// RoutesConfig holds route configuration.
type RoutesConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Prefix  string `mapstructure:"prefix"`
}

// SetDefaults sets viper defaults for OPNS configuration.
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	if prefix != "" {
		p = prefix + "."
	}

	v.SetDefault(p+"mode", ModeDisabled)
	v.SetDefault(p+"sync.resolve_dependencies", true)
	v.SetDefault(p+"crawl.enabled", false)
	v.SetDefault(p+"crawl.concurrency", 8)
	v.SetDefault(p+"routes.enabled", true)
	v.SetDefault(p+"routes.prefix", "/opns")
}

// Services holds initialized OPNS services.
type Services struct {
	Engine        *engine.Engine
	Lookup        *LookupService
	TopicManager  *TopicManager
	Crawl         *GenesisCrawl
	Sync          *overlay.OverlaySync
	Routes        *Routes
	OverlayRoutes *overlay.Routes
}

// Initialize creates OPNS services from the configuration.
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
			return nil, fmt.Errorf("overlay ModuleDeps with Factory is required for OPNS")
		}
		ts, err := deps.Factory("tm_opns")
		if err != nil {
			return nil, fmt.Errorf("failed to get OPNS topic storage: %w", err)
		}
		lookupSvc := NewLookupService(ts)
		topicManager := &TopicManager{}

		eng := overlay.NewModuleEngine(deps,
			map[string]engine.TopicManager{"tm_opns": topicManager},
			map[string]engine.LookupService{"opns": lookupSvc},
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
		return nil, fmt.Errorf("unknown opns mode: %s", c.Mode)
	}
}

// Close closes the OPNS services.
func (s *Services) Close() error {
	if s.Crawl != nil {
		s.Crawl.Stop()
	}
	return nil
}

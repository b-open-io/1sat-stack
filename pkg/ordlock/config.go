package ordlock

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/overlay"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/spf13/viper"
)

const (
	ModeDisabled = "disabled"
	ModeEmbedded = "embedded"
	TopicName    = "tm_ordlock"
	QueueName    = "ordlock"
)

type Config struct {
	Mode   string                     `mapstructure:"mode"`
	Sync   *overlay.OverlaySyncConfig `mapstructure:"sync"`
	Routes RoutesConfig               `mapstructure:"routes"`
}

type RoutesConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Prefix  string `mapstructure:"prefix"`
}

func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	if prefix != "" {
		p = prefix + "."
	}

	v.SetDefault(p+"mode", ModeDisabled)
	v.SetDefault(p+"sync.enabled", false)
	v.SetDefault(p+"sync.subscription_id", "")
	v.SetDefault(p+"sync.queue_name", QueueName)
	v.SetDefault(p+"sync.from_block", 575000)
	v.SetDefault(p+"sync.concurrency", 8)
	v.SetDefault(p+"sync.resolve_dependencies", true)
	v.SetDefault(p+"routes.enabled", true)
	v.SetDefault(p+"routes.prefix", "/market")
}

type Services struct {
	Engine        *engine.Engine
	Lookup        *LookupService
	TopicManager  *TopicManager
	OrdLock       *OrdLock
	Sync          *overlay.OverlaySync
	Routes        *Routes
	OverlayRoutes *overlay.Routes
}

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
			return nil, fmt.Errorf("overlay ModuleDeps with Factory is required for OrdLock")
		}
		ts, err := deps.Factory(TopicName)
		if err != nil {
			return nil, fmt.Errorf("failed to get OrdLock topic storage: %w", err)
		}

		ol := New(ts.DB(), ts.TopicID(), nil, logger)

		lookupSvc := NewLookupService(ol)
		topicManager := &TopicManager{}

		eng := overlay.NewModuleEngine(deps,
			map[string]engine.TopicManager{TopicName: topicManager},
			map[string]engine.LookupService{"ordlock": lookupSvc},
		)

		svc := &Services{
			Engine:       eng,
			Lookup:       lookupSvc,
			TopicManager: topicManager,
			OrdLock:      ol,
		}

		if c.Routes.Enabled {
			svc.Routes = NewRoutes(ol, logger)
		}

		if deps.RoutesConfig != nil && deps.RoutesConfig.Enabled {
			svc.OverlayRoutes = overlay.NewRoutes(eng, deps.RoutesConfig, logger)
		}

		return svc, nil

	default:
		return nil, fmt.Errorf("unknown ordlock mode: %s", c.Mode)
	}
}

func (s *Services) Close() error {
	if s.Sync != nil {
		s.Sync.Stop()
	}
	if s.OrdLock != nil {
		return s.OrdLock.Close()
	}
	return nil
}

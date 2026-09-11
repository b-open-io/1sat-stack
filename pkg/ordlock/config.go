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
)

// QueueName is the overlay work queue fed by the event bridge and the
// optional JungleBus subscriber; OverlaySync drains it into TopicNameV2.
const QueueName = "ordlock"

type Config struct {
	Mode     string                     `mapstructure:"mode"`
	LogLevel string                     `mapstructure:"log_level"` // debug, info, warn, error
	Sync     *overlay.OverlaySyncConfig `mapstructure:"sync"`
	Routes   RoutesConfig               `mapstructure:"routes"`
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
	v.SetDefault(p+"sync.from_block", 783968)
	v.SetDefault(p+"sync.concurrency", 8)
	v.SetDefault(p+"sync.batch_size", 1000)
	v.SetDefault(p+"sync.resolve_dependencies", false)
	v.SetDefault(p+"routes.enabled", true)
	v.SetDefault(p+"routes.prefix", "/market")
}

type Services struct {
	Engine         *engine.Engine
	LookupV2       *LookupServiceV2
	TopicManagerV2 *TopicManagerV2
	OrdLockV2      *OrdLock
	Sync           *overlay.OverlaySync
	Routes         *Routes
	OverlayRoutes  *overlay.Routes
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
		// OrdLock v1 is deprecated: its overlay topic is not registered, so the
		// stack does not admit, index, or serve v1 listings as a live market.
		// Only OrdLock v2 (batch, tag-output binding) is served. v1
		// cancellation/recovery is unaffected: it runs off the per-output data
		// + owner index written by pkg/parse/ordlock, independent of this topic.
		tsV2, err := deps.Factory(TopicNameV2)
		if err != nil {
			return nil, fmt.Errorf("failed to get OrdLock v2 topic storage: %w", err)
		}
		olV2 := New(tsV2.DB(), tsV2.TopicID(), nil, logger)
		lookupSvcV2 := NewLookupServiceV2(olV2)
		topicManagerV2 := &TopicManagerV2{}

		eng := overlay.NewModuleEngine(deps,
			map[string]engine.TopicManager{
				TopicNameV2: topicManagerV2,
			},
			map[string]engine.LookupService{
				"ordlock2": lookupSvcV2,
			},
		)

		svc := &Services{
			Engine:         eng,
			LookupV2:       lookupSvcV2,
			TopicManagerV2: topicManagerV2,
			OrdLockV2:      olV2,
		}

		if c.Routes.Enabled {
			svc.Routes = NewRoutes(olV2, logger)
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
	if s.OrdLockV2 != nil {
		return s.OrdLockV2.Close()
	}
	return nil
}

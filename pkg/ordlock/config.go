package ordlock

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/lookup"
	"github.com/b-open-io/1sat-stack/pkg/overlay"
	overlaystorage "github.com/b-open-io/1sat-stack/pkg/overlay/storage"
	"github.com/spf13/viper"
)

const (
	ModeDisabled = "disabled"
	ModeEmbedded = "embedded"
	TopicName    = "tm_ordlock"
)

type Config struct {
	Mode   string                    `mapstructure:"mode"`
	Sync   *overlay.OverlaySyncConfig `mapstructure:"sync"`
	Routes RoutesConfig              `mapstructure:"routes"`
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
	v.SetDefault(p+"sync.queue_name", "ordlock")
	v.SetDefault(p+"sync.from_block", 783968)
	v.SetDefault(p+"sync.concurrency", 8)
	v.SetDefault(p+"sync.batch_size", 1000)
	v.SetDefault(p+"sync.reorg_depth", 6)
	v.SetDefault(p+"sync.resolve_dependencies", true)
	v.SetDefault(p+"routes.enabled", true)
	v.SetDefault(p+"routes.prefix", "/market")
}

type Services struct {
	Lookup       *lookup.OrdLockLookup
	TopicManager *TopicManager
	Sync         *overlay.OverlaySync
	Routes       *Routes
}

func (c *Config) Initialize(
	ctx context.Context,
	logger *slog.Logger,
	topicDB overlaystorage.Factory,
) (*Services, error) {
	if c.Mode == ModeDisabled {
		return nil, nil
	}

	if logger == nil {
		logger = slog.Default()
	}

	switch c.Mode {
	case ModeEmbedded:
		lookupSvc := lookup.NewOrdLockLookup(topicDB)
		topicManager := &TopicManager{}

		svc := &Services{
			Lookup:       lookupSvc,
			TopicManager: topicManager,
		}

		if c.Routes.Enabled {
			svc.Routes = NewRoutes(lookupSvc, TopicName, logger)
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
	return nil
}

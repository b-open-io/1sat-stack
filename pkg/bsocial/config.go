package bsocial

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/overlay"
	"github.com/spf13/viper"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// Mode constants
const (
	ModeDisabled = "disabled"
	ModeEmbedded = "embedded"
)

// Config holds BSocial configuration.
type Config struct {
	Mode   string                    `mapstructure:"mode"` // disabled, embedded
	Sync   *overlay.OverlaySyncConfig `mapstructure:"sync"`
	Routes RoutesConfig              `mapstructure:"routes"`
}

// RoutesConfig holds route configuration.
type RoutesConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Prefix  string `mapstructure:"prefix"`
}

// SetDefaults sets viper defaults for BSocial configuration.
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	if prefix != "" {
		p = prefix + "."
	}

	v.SetDefault(p+"mode", ModeDisabled)
	v.SetDefault(p+"sync.enabled", false)
	v.SetDefault(p+"sync.subscription_id", "")
	v.SetDefault(p+"sync.queue_name", "bsocial")
	v.SetDefault(p+"sync.from_block", 575000)
	v.SetDefault(p+"sync.concurrency", 8)
	v.SetDefault(p+"sync.batch_size", 1000)
	v.SetDefault(p+"sync.reorg_depth", 6)
	v.SetDefault(p+"routes.enabled", true)
	v.SetDefault(p+"routes.prefix", "/bsocial")
}

// Services holds initialized BSocial services.
type Services struct {
	Lookup       *LookupService
	TopicManager *TopicManager
	Sync         *overlay.OverlaySync
	Routes       *Routes
}

// Initialize creates BSocial services from the configuration.
func (c *Config) Initialize(
	ctx context.Context,
	logger *slog.Logger,
	db *mongo.Database,
) (*Services, error) {
	if c.Mode == ModeDisabled {
		return nil, nil
	}

	if logger == nil {
		logger = slog.Default()
	}

	switch c.Mode {
	case ModeEmbedded:
		lookupSvc := NewLookupService(db)

		if err := lookupSvc.EnsureIndexes(ctx); err != nil {
			return nil, fmt.Errorf("failed to ensure bsocial indexes: %w", err)
		}

		topicManager := &TopicManager{}

		svc := &Services{
			Lookup:       lookupSvc,
			TopicManager: topicManager,
		}

		if c.Routes.Enabled {
			svc.Routes = NewRoutes(lookupSvc, logger)
		}

		return svc, nil

	default:
		return nil, fmt.Errorf("unknown bsocial mode: %s", c.Mode)
	}
}

// Close closes the BSocial services.
func (s *Services) Close() error {
	if s.Sync != nil {
		s.Sync.Stop()
	}
	return nil
}

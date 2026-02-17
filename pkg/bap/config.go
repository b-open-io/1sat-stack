package bap

import (
	"context"
	"errors"
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

// Config holds BAP configuration.
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
	v.SetDefault(p+"sync.concurrency", 1) // Sequential - BAP validates against current identity owner state
	v.SetDefault(p+"sync.batch_size", 1000)
	v.SetDefault(p+"sync.reorg_depth", 6)
	v.SetDefault(p+"routes.enabled", true)
	v.SetDefault(p+"routes.prefix", "/bap")
}

// Services holds initialized BAP services.
type Services struct {
	Lookup       *LookupService
	TopicManager *TopicManager
	Sync         *overlay.OverlaySync
	Routes       *Routes
}

// Initialize creates BAP services from the configuration.
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

		topicManager := &TopicManager{
			Lookup: lookupSvc,
		}

		svc := &Services{
			Lookup:       lookupSvc,
			TopicManager: topicManager,
		}

		// BAP must process sequentially — identity state is order-dependent
		if c.Sync != nil {
			c.Sync.Concurrency = 1
			c.Sync.ErrorClassifier = func(err error) overlay.ErrorAction {
				var stateErr *BAPStateError
				if errors.As(err, &stateErr) {
					return overlay.ErrorSkip
				}
				return overlay.ErrorRetry
			}
		}

		if c.Routes.Enabled {
			svc.Routes = NewRoutes(lookupSvc, logger)
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

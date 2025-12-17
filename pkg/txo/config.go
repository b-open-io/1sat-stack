package txo

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/pubsub"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/spf13/viper"
)

// Mode constants
const (
	ModeDisabled = "disabled"
	ModeEmbedded = "embedded"
	ModeRemote   = "remote"
)

// Config holds txo storage configuration.
type Config struct {
	Mode string `mapstructure:"mode"` // disabled, embedded, remote

	// Embedded configs for dependencies (only used in embedded mode)
	Store  store.Config  `mapstructure:"store"`
	PubSub pubsub.Config `mapstructure:"pubsub"`
	Beef   beef.Config   `mapstructure:"beef"`

	Routes RoutesConfig `mapstructure:"routes"`
}

// RoutesConfig holds route configuration
type RoutesConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Prefix  string `mapstructure:"prefix"`
}

// SetDefaults sets viper defaults for txo configuration.
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	if prefix != "" {
		p = prefix + "."
	}

	v.SetDefault(p+"mode", ModeDisabled)
	v.SetDefault(p+"routes.enabled", true)
	v.SetDefault(p+"routes.prefix", "")

	// Cascade to embedded configs
	c.Store.SetDefaults(v, p+"store")
	c.PubSub.SetDefaults(v, p+"pubsub")
	c.Beef.SetDefaults(v, p+"beef")
}

// Services holds initialized txo services.
type Services struct {
	OutputStore *OutputStore // Unified output storage
	Routes      *Routes      // HTTP routes (nil if not enabled)
	Store       *store.Services
	PubSub      *pubsub.Services
	BeefStore   *beef.Services
	sseManager  *pubsub.SSEManager
}

// Initialize creates an OutputStore from the configuration.
func (c *Config) Initialize(ctx context.Context, logger *slog.Logger) (*Services, error) {
	if c.Mode == ModeDisabled {
		return nil, nil
	}

	if logger == nil {
		logger = slog.Default()
	}

	switch c.Mode {
	case ModeEmbedded:
		// Initialize dependencies
		storeSvc, err := c.Store.Initialize(ctx, logger)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize store: %w", err)
		}

		pubsubSvc, err := c.PubSub.Initialize(ctx, logger)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize pubsub: %w", err)
		}

		beefSvc, err := c.Beef.Initialize(ctx, logger, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize beef storage: %w", err)
		}

		var beefStorage *beef.Storage
		if beefSvc != nil {
			beefStorage = beefSvc.Storage
		}

		var pubsubImpl pubsub.PubSub
		if pubsubSvc != nil {
			pubsubImpl = pubsubSvc.PubSub
		}

		outputStore := NewOutputStore(storeSvc.Store, pubsubImpl, beefStorage)

		svc := &Services{
			OutputStore: outputStore,
			Store:       storeSvc,
			PubSub:      pubsubSvc,
			BeefStore:   beefSvc,
		}

		if c.Routes.Enabled {
			svc.Routes = NewRoutes(outputStore)
		}

		return svc, nil

	case ModeRemote:
		// TODO: Implement remote txo client
		return nil, fmt.Errorf("remote mode not yet implemented for txo")

	default:
		return nil, fmt.Errorf("unknown txo mode: %s", c.Mode)
	}
}

// InitializeWithDeps creates an OutputStore using pre-initialized dependencies.
// Use this when you need to share dependencies across multiple services.
func (c *Config) InitializeWithDeps(
	ctx context.Context,
	storeSvc *store.Services,
	pubsubSvc *pubsub.Services,
	beefSvc *beef.Services,
	logger *slog.Logger,
) (*Services, error) {
	if c.Mode == ModeDisabled {
		return nil, nil
	}

	var beefStorage *beef.Storage
	if beefSvc != nil {
		beefStorage = beefSvc.Storage
	}

	var pubsubImpl pubsub.PubSub
	if pubsubSvc != nil {
		pubsubImpl = pubsubSvc.PubSub
	}

	outputStore := NewOutputStore(storeSvc.Store, pubsubImpl, beefStorage)

	svc := &Services{
		OutputStore: outputStore,
		Store:       storeSvc,
		PubSub:      pubsubSvc,
		BeefStore:   beefSvc,
	}

	if c.Routes.Enabled {
		svc.Routes = NewRoutes(outputStore)
	}

	return svc, nil
}

// Close closes all txo services.
func (s *Services) Close() error {
	if s == nil {
		return nil
	}
	// Don't close dependencies here - they may be shared
	// The caller is responsible for closing shared dependencies
	return nil
}

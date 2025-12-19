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
	Mode   string       `mapstructure:"mode"` // disabled, embedded, remote
	Routes RoutesConfig `mapstructure:"routes"`
}

// RoutesConfig holds route configuration
type RoutesConfig struct {
	Enabled bool `mapstructure:"enabled"`
}

// SetDefaults sets viper defaults for txo configuration.
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	if prefix != "" {
		p = prefix + "."
	}

	v.SetDefault(p+"mode", ModeDisabled)
	v.SetDefault(p+"routes.enabled", true)
}

// Services holds initialized txo services.
type Services struct {
	OutputStore *OutputStore // Unified output storage
	Routes      *Routes      // HTTP routes (nil if not enabled)
	sseManager  *pubsub.SSEManager
}

// Initialize creates an OutputStore from the configuration.
// Accepts shared dependencies - pass nil to skip that dependency.
func (c *Config) Initialize(
	ctx context.Context,
	logger *slog.Logger,
	storeSvc *store.Services,
	pubsubSvc *pubsub.Services,
	beefSvc *beef.Services,
) (*Services, error) {
	if c.Mode == ModeDisabled {
		return nil, nil
	}

	if logger == nil {
		logger = slog.Default()
	}

	switch c.Mode {
	case ModeEmbedded:
		if storeSvc == nil {
			return nil, fmt.Errorf("txo embedded mode requires store service")
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

// Close closes all txo services.
func (s *Services) Close() error {
	if s == nil {
		return nil
	}
	// Don't close dependencies here - they may be shared
	// The caller is responsible for closing shared dependencies
	return nil
}

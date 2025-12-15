package pubsub

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/spf13/viper"
)

// Mode constants
const (
	ModeDisabled = "disabled"
	ModeEmbedded = "embedded"
	ModeRemote   = "remote"
)

// Config holds pub/sub configuration.
type Config struct {
	Mode string `mapstructure:"mode"` // disabled, embedded, remote
	URL  string `mapstructure:"url"`  // Connection string

	Routes RoutesConfig `mapstructure:"routes"`
}

// RoutesConfig holds route configuration
type RoutesConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Prefix  string `mapstructure:"prefix"`
}

// SetDefaults sets viper defaults for pubsub configuration.
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	if prefix != "" {
		p = prefix + "."
	}

	v.SetDefault(p+"mode", ModeDisabled)
	v.SetDefault(p+"url", "channels://")
	v.SetDefault(p+"routes.enabled", true)
	v.SetDefault(p+"routes.prefix", "")
}

// Services holds initialized pubsub services.
type Services struct {
	PubSub     PubSub
	SSEManager *SSEManager
	Routes     *Routes
}

// Initialize creates a PubSub from the configuration.
func (c *Config) Initialize(ctx context.Context, logger *slog.Logger) (*Services, error) {
	if c.Mode == ModeDisabled {
		return nil, nil
	}

	if logger == nil {
		logger = slog.Default()
	}

	switch c.Mode {
	case ModeEmbedded:
		pubsub, err := CreatePubSub(c.URL)
		if err != nil {
			return nil, fmt.Errorf("failed to create pubsub: %w", err)
		}

		svc := &Services{
			PubSub:     pubsub,
			SSEManager: NewSSEManager(ctx, pubsub),
		}

		if c.Routes.Enabled {
			svc.Routes = NewRoutes(svc.SSEManager)
		}

		return svc, nil

	case ModeRemote:
		// TODO: Implement remote pubsub client
		return nil, fmt.Errorf("remote mode not yet implemented for pubsub")

	default:
		return nil, fmt.Errorf("unknown pubsub mode: %s", c.Mode)
	}
}

// Close closes the pubsub.
func (s *Services) Close() error {
	if s == nil {
		return nil
	}
	if s.SSEManager != nil {
		s.SSEManager.Stop()
	}
	if s.PubSub != nil {
		return s.PubSub.Close()
	}
	return nil
}

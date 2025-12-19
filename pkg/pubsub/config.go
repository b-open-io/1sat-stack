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

// Provider constants
const (
	ProviderChannels = "channels"
	// Future providers:
	// ProviderRedis = "redis"
)

// Config holds pub/sub configuration.
type Config struct {
	Mode     string         `mapstructure:"mode"`     // disabled, embedded, remote
	Provider string         `mapstructure:"provider"` // channels, redis
	Channels ChannelsConfig `mapstructure:"channels"` // Channels-specific config (in-memory)
	// Future providers:
	// Redis    RedisConfig     `mapstructure:"redis"`

	Routes RoutesConfig `mapstructure:"routes"`
}

// ChannelsConfig holds in-memory channels configuration
type ChannelsConfig struct {
	BufferSize int `mapstructure:"buffer_size"` // Channel buffer size (default: 100)
}

// RoutesConfig holds route configuration
type RoutesConfig struct {
	Enabled bool `mapstructure:"enabled"`
}

// SetDefaults sets viper defaults for pubsub configuration.
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	if prefix != "" {
		p = prefix + "."
	}

	v.SetDefault(p+"mode", ModeDisabled)
	v.SetDefault(p+"provider", ProviderChannels)
	v.SetDefault(p+"channels.buffer_size", 100)
	v.SetDefault(p+"routes.enabled", true)
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
		return c.initializeEmbedded(ctx, logger)

	case ModeRemote:
		// TODO: Implement remote pubsub client
		return nil, fmt.Errorf("remote mode not yet implemented for pubsub")

	default:
		return nil, fmt.Errorf("unknown pubsub mode: %s", c.Mode)
	}
}

// initializeEmbedded creates an embedded pubsub based on provider
func (c *Config) initializeEmbedded(ctx context.Context, logger *slog.Logger) (*Services, error) {
	var pubsub PubSub

	switch c.Provider {
	case ProviderChannels, "": // Default to channels if empty
		pubsub = NewChannelPubSub()

	// Future providers:
	// case ProviderRedis:
	//     return c.initializeRedis(ctx, logger)

	default:
		return nil, fmt.Errorf("unknown pubsub provider: %s", c.Provider)
	}

	svc := &Services{
		PubSub:     pubsub,
		SSEManager: NewSSEManager(ctx, pubsub),
	}

	if c.Routes.Enabled {
		svc.Routes = NewRoutes(svc.SSEManager)
	}

	return svc, nil
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

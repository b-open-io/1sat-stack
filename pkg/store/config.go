package store

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
	ProviderBadger = "badger"
	// Future providers:
	// ProviderTiKV     = "tikv"
	// ProviderRedis    = "redis"
	// ProviderAerospike = "aerospike"
)

// Config holds store configuration.
type Config struct {
	Mode     string       `mapstructure:"mode"`     // disabled, embedded, remote
	Provider string       `mapstructure:"provider"` // badger, tikv, redis, aerospike
	Badger   BadgerConfig `mapstructure:"badger"`   // Badger-specific config
	// Future providers:
	// TiKV     TiKVConfig     `mapstructure:"tikv"`
	// Redis    RedisConfig    `mapstructure:"redis"`
	// Aerospike AerospikeConfig `mapstructure:"aerospike"`
}

// BadgerConfig holds Badger-specific configuration
type BadgerConfig struct {
	Path     string `mapstructure:"path"`      // Path to database directory
	InMemory bool   `mapstructure:"in_memory"` // Use in-memory storage
}

// SetDefaults sets viper defaults for store configuration.
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	if prefix != "" {
		p = prefix + "."
	}
	v.SetDefault(p+"mode", ModeDisabled)
	v.SetDefault(p+"provider", ProviderBadger)
	v.SetDefault(p+"badger.path", "~/.1sat/store")
	v.SetDefault(p+"badger.in_memory", false)
}

// Services holds initialized store services.
type Services struct {
	Store Store
}

// Initialize creates a Store from the configuration.
func (c *Config) Initialize(ctx context.Context, logger *slog.Logger) (*Services, error) {
	if c.Mode == ModeDisabled {
		return nil, nil
	}

	if logger == nil {
		logger = slog.Default()
	}

	switch c.Mode {
	case ModeEmbedded:
		return c.initializeEmbedded(logger)

	case ModeRemote:
		// TODO: Implement remote store client
		return nil, fmt.Errorf("remote mode not yet implemented for store")

	default:
		return nil, fmt.Errorf("unknown store mode: %s", c.Mode)
	}
}

// initializeEmbedded creates an embedded store based on provider
func (c *Config) initializeEmbedded(logger *slog.Logger) (*Services, error) {
	switch c.Provider {
	case ProviderBadger, "": // Default to badger if empty
		store, err := NewBadgerStoreFromConfig(&c.Badger, logger)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize badger store: %w", err)
		}
		return &Services{Store: store}, nil

	// Future providers:
	// case ProviderTiKV:
	//     return c.initializeTiKV(logger)
	// case ProviderRedis:
	//     return c.initializeRedis(logger)

	default:
		return nil, fmt.Errorf("unknown store provider: %s", c.Provider)
	}
}

// Close closes the store.
func (s *Services) Close() error {
	if s != nil && s.Store != nil {
		return s.Store.Close()
	}
	return nil
}

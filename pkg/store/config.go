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

// Config holds store configuration.
type Config struct {
	Mode string `mapstructure:"mode"` // disabled, embedded, remote
	URL  string `mapstructure:"url"`  // Connection string (badger:///path or redis://...)
}

// SetDefaults sets viper defaults for store configuration.
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	if prefix != "" {
		p = prefix + "."
	}
	v.SetDefault(p+"mode", ModeDisabled)
	v.SetDefault(p+"url", "badger:///tmp/1sat-stack/store")
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
		store, err := NewBadgerStore(c.URL, logger)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize badger store: %w", err)
		}
		return &Services{Store: store}, nil

	case ModeRemote:
		// TODO: Implement Redis store for remote mode
		return nil, fmt.Errorf("remote mode not yet implemented for store")

	default:
		return nil, fmt.Errorf("unknown store mode: %s", c.Mode)
	}
}

// Close closes the store.
func (s *Services) Close() error {
	if s != nil && s.Store != nil {
		return s.Store.Close()
	}
	return nil
}

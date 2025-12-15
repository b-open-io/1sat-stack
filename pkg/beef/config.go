package beef

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/bsv-blockchain/go-sdk/transaction/chaintracker"
	"github.com/spf13/viper"
)

// Mode constants
const (
	ModeDisabled = "disabled"
	ModeEmbedded = "embedded"
	ModeRemote   = "remote"
)

// Config holds BEEF storage configuration.
type Config struct {
	Mode string `mapstructure:"mode"` // disabled, embedded, remote
	URL  string `mapstructure:"url"`  // For remote mode

	// Chain is an array of storage connection strings for the fallback chain.
	// Only used in embedded mode.
	Chain []string `mapstructure:"chain"`

	Routes RoutesConfig `mapstructure:"routes"`
}

// RoutesConfig holds route configuration
type RoutesConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Prefix  string `mapstructure:"prefix"`
}

// SetDefaults sets viper defaults for beef configuration.
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	if prefix != "" {
		p = prefix + "."
	}

	v.SetDefault(p+"mode", ModeDisabled)
	v.SetDefault(p+"chain", []string{})
	v.SetDefault(p+"url", DefaultStoragePath())
	v.SetDefault(p+"routes.enabled", true)
	v.SetDefault(p+"routes.prefix", "")
}

// DefaultStoragePath returns the default filesystem path for BEEF storage.
func DefaultStoragePath() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "./beef/"
	}
	return filepath.Join(homeDir, ".1sat", "beef")
}

// Services holds initialized beef services.
type Services struct {
	Storage *Storage
	Routes  *Routes // nil if routes not enabled or remote mode
}

// Initialize creates a BEEF Storage from the configuration.
func (c *Config) Initialize(ctx context.Context, logger *slog.Logger, chainTracker chaintracker.ChainTracker) (*Services, error) {
	if c.Mode == ModeDisabled {
		return nil, nil
	}

	if logger == nil {
		logger = slog.Default()
	}

	switch c.Mode {
	case ModeEmbedded:
		var connString string

		if len(c.Chain) > 0 {
			if len(c.Chain) == 1 {
				connString = c.Chain[0]
			} else {
				connString = "["
				for i, s := range c.Chain {
					if i > 0 {
						connString += ","
					}
					connString += `"` + s + `"`
				}
				connString += "]"
			}
		} else if c.URL != "" {
			connString = c.URL
		}

		storage, err := NewStorage(connString, chainTracker)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize beef storage: %w", err)
		}

		svc := &Services{Storage: storage}

		if c.Routes.Enabled {
			svc.Routes = NewRoutes(storage)
		}

		return svc, nil

	case ModeRemote:
		// TODO: Implement remote HTTP client for beef
		return nil, fmt.Errorf("remote mode not yet implemented for beef")

	default:
		return nil, fmt.Errorf("unknown beef mode: %s", c.Mode)
	}
}

// Close closes the beef storage.
func (s *Services) Close() error {
	if s != nil && s.Storage != nil {
		return s.Storage.Close()
	}
	return nil
}

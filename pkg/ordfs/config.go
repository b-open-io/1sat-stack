package ordfs

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

// Config holds ORDFS configuration
type Config struct {
	Mode string `mapstructure:"mode"` // disabled, embedded, remote

	// Routes settings
	Routes RoutesConfig `mapstructure:"routes"`
}

// RoutesConfig holds route configuration
type RoutesConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Prefix  string `mapstructure:"prefix"`
}

// SetDefaults sets viper defaults for ORDFS configuration
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	if prefix != "" {
		p = prefix + "."
	}

	v.SetDefault(p+"mode", ModeDisabled)
	v.SetDefault(p+"routes.enabled", true)
	v.SetDefault(p+"routes.prefix", "/ordfs")
}

// Services holds initialized ORDFS services
type Services struct {
	Content *ContentService
	Routes  *Routes
}

// Initialize creates ORDFS services from the configuration
func (c *Config) Initialize(
	ctx context.Context,
	logger *slog.Logger,
	loader Loader,
) (*Services, error) {
	if c.Mode == ModeDisabled {
		return nil, nil
	}

	if logger == nil {
		logger = slog.Default()
	}

	switch c.Mode {
	case ModeEmbedded:
		if loader == nil {
			return nil, fmt.Errorf("loader is required for embedded mode")
		}

		// Create content service
		content := NewContentService(loader, logger)

		svc := &Services{
			Content: content,
		}

		// Create routes if enabled
		if c.Routes.Enabled {
			svc.Routes = NewRoutes(&RoutesDeps{
				Content: content,
				Logger:  logger,
			})
		}

		return svc, nil

	case ModeRemote:
		return nil, fmt.Errorf("remote mode not yet implemented for ordfs")

	default:
		return nil, fmt.Errorf("unknown ordfs mode: %s", c.Mode)
	}
}

// Close closes the ORDFS services
func (s *Services) Close() error {
	// Nothing to close
	return nil
}

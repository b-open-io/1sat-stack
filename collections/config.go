package collections

import (
	"context"
	"log/slog"

	"github.com/spf13/viper"
)

const (
	ModeDisabled = "disabled"
	ModeEnabled  = "enabled"
)

// Config holds the collections browser configuration.
type Config struct {
	Mode   string       `mapstructure:"mode"`
	Routes RoutesConfig `mapstructure:"routes"`
}

// RoutesConfig holds HTTP route configuration.
type RoutesConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Prefix  string `mapstructure:"prefix"`
}

// Services holds initialized collections browser services.
type Services struct {
	Routes *Routes
}

// SetDefaults sets viper defaults for the collections browser.
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	v.SetDefault(prefix+".mode", ModeEnabled)
	v.SetDefault(prefix+".routes.enabled", true)
	v.SetDefault(prefix+".routes.prefix", "/collections")
}

// Initialize creates the collections browser. Disabled mode returns nil.
func (c *Config) Initialize(ctx context.Context, logger *slog.Logger) (*Services, error) {
	if c.Mode == ModeDisabled {
		return nil, nil
	}
	if logger == nil {
		logger = slog.Default()
	}

	svc := &Services{}
	if c.Routes.Enabled {
		svc.Routes = NewRoutes(&c.Routes, logger)
	}

	logger.Info("collections browser initialized", "mode", c.Mode)
	return svc, nil
}

// Close releases collections browser services.
func (svc *Services) Close() error {
	return nil
}

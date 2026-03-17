package sweep

import (
	"context"
	"log/slog"

	"github.com/spf13/viper"
)

const (
	ModeDisabled = "disabled"
	ModeEnabled  = "enabled"
)

type Config struct {
	Mode   string       `mapstructure:"mode"`
	Routes RoutesConfig `mapstructure:"routes"`
}

type RoutesConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Prefix  string `mapstructure:"prefix"`
}

type Services struct {
	Routes *Routes
}

func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	v.SetDefault(prefix+".mode", ModeEnabled)
	v.SetDefault(prefix+".routes.enabled", true)
	v.SetDefault(prefix+".routes.prefix", "/sweep")
}

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

	logger.Info("sweep service initialized", "mode", c.Mode)
	return svc, nil
}

func (svc *Services) Close() error {
	return nil
}

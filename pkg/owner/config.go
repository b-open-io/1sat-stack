package own

import (
	"context"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/overlay"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/b-open-io/go-junglebus"
	"github.com/spf13/viper"
)

// Mode constants
const (
	ModeDisabled = "disabled"
	ModeEmbedded = "embedded"
)

// Config holds owner service configuration.
type Config struct {
	Mode string `mapstructure:"mode"` // disabled, embedded

	Routes RoutesConfig `mapstructure:"routes"`
}

// RoutesConfig holds route configuration
type RoutesConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Prefix  string `mapstructure:"prefix"`
}

// SetDefaults sets viper defaults for owner configuration.
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	if prefix != "" {
		p = prefix + "."
	}

	v.SetDefault(p+"mode", ModeEmbedded)
	v.SetDefault(p+"routes.enabled", true)
	v.SetDefault(p+"routes.prefix", "")
}

// Services holds initialized owner services.
type Services struct {
	Sync   *OwnerSync
	Routes *Routes
}

// InitializeDeps holds dependencies for owner service initialization
type InitializeDeps struct {
	JungleBus   *junglebus.Client
	BeefStorage *beef.Storage
	Overlay     *overlay.Services
	OutputStore *txo.OutputStore
}

// Initialize creates an owner service from the configuration.
func (c *Config) Initialize(
	ctx context.Context,
	logger *slog.Logger,
	deps *InitializeDeps,
) (*Services, error) {
	if c.Mode == ModeDisabled {
		return nil, nil
	}

	if logger == nil {
		logger = slog.Default()
	}

	svc := &Services{
		Sync: NewOwnerSync(
			deps.JungleBus,
			deps.BeefStorage,
			deps.Overlay,
			deps.OutputStore,
			logger,
		),
	}

	if c.Routes.Enabled {
		svc.Routes = NewRoutes(ctx, svc.Sync, deps.OutputStore, logger)
	}

	return svc, nil
}

// Close closes the owner service.
func (s *Services) Close() error {
	return nil
}

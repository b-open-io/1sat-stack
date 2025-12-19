package admin

import (
	"context"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/fees"
	"github.com/b-open-io/1sat-stack/pkg/overlay"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/spf13/viper"
)

const (
	// ModeDisabled disables admin
	ModeDisabled = "disabled"
	// ModeEnabled enables admin
	ModeEnabled = "enabled"
)

// Config holds admin configuration
type Config struct {
	Mode   string       `mapstructure:"mode"`
	Routes RoutesConfig `mapstructure:"routes"`
}

// RoutesConfig holds route configuration
type RoutesConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Prefix  string `mapstructure:"prefix"`
}

// Services holds admin service instances
type Services struct {
	Routes *Routes
}

// InitializeDeps holds dependencies for admin initialization
type InitializeDeps struct {
	FeeService *fees.FeeService
	Overlay    *overlay.Services
	Store      store.Store
}

// SetDefaults sets default configuration values
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	v.SetDefault(prefix+".mode", ModeEnabled)
	v.SetDefault(prefix+".routes.enabled", true)
	v.SetDefault(prefix+".routes.prefix", "/admin")
}

// Initialize creates admin services from configuration
func (c *Config) Initialize(ctx context.Context, logger *slog.Logger, deps *InitializeDeps) (*Services, error) {
	if c.Mode == ModeDisabled {
		return nil, nil
	}

	if logger == nil {
		logger = slog.Default()
	}

	svc := &Services{}

	// Create routes if enabled
	if c.Routes.Enabled && deps.FeeService != nil {
		svc.Routes = NewRoutes(deps.FeeService, deps.Overlay, deps.Store, &c.Routes, logger)
	}

	logger.Info("admin service initialized", "mode", c.Mode)
	return svc, nil
}

// Close cleans up admin services
func (svc *Services) Close() error {
	return nil
}

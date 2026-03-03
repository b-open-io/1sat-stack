package paymail

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/opns"
	"github.com/b-open-io/1sat-stack/pkg/ordfs"
	arcadeservice "github.com/bsv-blockchain/arcade/service"
	sdk "github.com/bsv-blockchain/go-sdk/wallet"
	"github.com/spf13/viper"
)

// Mode constants
const (
	ModeDisabled = "disabled"
	ModeEnabled  = "enabled"
)

// Config holds paymail service configuration.
type Config struct {
	Mode   string       `mapstructure:"mode"`
	Routes RoutesConfig `mapstructure:"routes"`
}

// RoutesConfig holds route configuration.
type RoutesConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Prefix  string `mapstructure:"prefix"`
}

// SetDefaults sets viper defaults for paymail configuration.
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	if prefix != "" {
		p = prefix + "."
	}

	v.SetDefault(p+"mode", ModeDisabled)
	v.SetDefault(p+"routes.enabled", true)
	v.SetDefault(p+"routes.prefix", "/v1/bsvalias")
}

// InitializeDeps holds dependencies for paymail initialization.
type InitializeDeps struct {
	OpnsLookup *opns.LookupService
	Ordfs      *ordfs.Ordfs
	Arcade     arcadeservice.ArcadeService
	Wallet     sdk.Interface
}

// Services holds initialized paymail services.
type Services struct {
	Service *Service
	Routes  *Routes
}

// Initialize creates paymail services from the configuration.
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

	if deps.OpnsLookup == nil {
		return nil, fmt.Errorf("OpNS lookup service is required for paymail")
	}
	if deps.Ordfs == nil {
		return nil, fmt.Errorf("ORDFS service is required for paymail")
	}
	if deps.Arcade == nil {
		return nil, fmt.Errorf("Arcade service is required for paymail")
	}
	if deps.Wallet == nil {
		return nil, fmt.Errorf("wallet is required for paymail")
	}

	service := NewService(deps.OpnsLookup, deps.Ordfs, deps.Arcade, deps.Wallet, logger)

	svc := &Services{
		Service: service,
	}

	if c.Routes.Enabled {
		svc.Routes = NewRoutes(service, logger)
	}

	logger.Info("paymail service initialized", "mode", c.Mode)
	return svc, nil
}

package own

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/b-open-io/go-junglebus"
	"github.com/b-open-io/1sat-stack/pkg/indexer"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/spf13/viper"
)

// Mode constants
const (
	ModeDisabled = "disabled"
	ModeEmbedded = "embedded"
)

// Config holds owner service configuration.
type Config struct {
	Mode      string `mapstructure:"mode"`      // disabled, embedded
	JungleBus string `mapstructure:"junglebus"` // JungleBus URL

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
	v.SetDefault(p+"junglebus", "https://junglebus.gorillapool.io")
	v.SetDefault(p+"routes.enabled", true)
	v.SetDefault(p+"routes.prefix", "")
}

// Services holds initialized owner services.
type Services struct {
	JungleBus *junglebus.Client
	Routes    *Routes
}

// Initialize creates an owner service from the configuration.
func (c *Config) Initialize(
	ctx context.Context,
	logger *slog.Logger,
	outputStore *txo.OutputStore,
	ingestCtx *indexer.IngestCtx,
) (*Services, error) {
	if c.Mode == ModeDisabled {
		return nil, nil
	}

	if logger == nil {
		logger = slog.Default()
	}

	switch c.Mode {
	case ModeEmbedded:
		// Initialize JungleBus client
		jb, err := junglebus.New(
			junglebus.WithHTTP(c.JungleBus),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create junglebus client: %w", err)
		}

		svc := &Services{JungleBus: jb}

		if c.Routes.Enabled {
			svc.Routes = NewRoutes(ctx, outputStore, ingestCtx, jb, logger)
		}

		return svc, nil

	default:
		return nil, fmt.Errorf("unknown own mode: %s", c.Mode)
	}
}

// Close closes the owner service.
func (s *Services) Close() error {
	// Nothing to close
	return nil
}

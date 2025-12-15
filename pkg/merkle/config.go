package merkle

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/pubsub"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/bsv-blockchain/go-sdk/transaction/chaintracker"
	"github.com/spf13/viper"
)

// Mode constants
const (
	ModeDisabled = "disabled"
	ModeEmbedded = "embedded"
)

// Config holds merkle service configuration.
type Config struct {
	Mode string `mapstructure:"mode"` // disabled, embedded

	Routes RoutesConfig `mapstructure:"routes"`
}

// RoutesConfig holds route configuration
type RoutesConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Prefix  string `mapstructure:"prefix"`
}

// SetDefaults sets viper defaults for merkle configuration.
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	if prefix != "" {
		p = prefix + "."
	}

	v.SetDefault(p+"mode", ModeDisabled)
	v.SetDefault(p+"routes.enabled", true)
	v.SetDefault(p+"routes.prefix", "")
}

// Services holds initialized merkle services.
type Services struct {
	Service *Service
	Routes  *Routes // nil if routes not enabled
}

// Initialize creates a MerkleService from the configuration.
func (c *Config) Initialize(
	ctx context.Context,
	logger *slog.Logger,
	s store.Store,
	beefStore *beef.Storage,
	ps pubsub.PubSub,
	ct chaintracker.ChainTracker,
	txoStore *txo.OutputStore,
) (*Services, error) {
	if c.Mode == ModeDisabled {
		return nil, nil
	}

	if logger == nil {
		logger = slog.Default()
	}

	switch c.Mode {
	case ModeEmbedded:
		service := NewService(s, beefStore, ps, ct, txoStore, logger)

		svc := &Services{Service: service}

		if c.Routes.Enabled {
			svc.Routes = NewRoutes(service, ps)
		}

		// Start the service
		if err := service.Start(ctx); err != nil {
			return nil, fmt.Errorf("failed to start merkle service: %w", err)
		}

		return svc, nil

	default:
		return nil, fmt.Errorf("unknown merkle mode: %s", c.Mode)
	}
}

// Close closes the merkle service.
func (s *Services) Close() error {
	if s != nil && s.Service != nil {
		return s.Service.Stop()
	}
	return nil
}

package ovr

import (
	"context"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/transaction/chaintracker"
	"github.com/spf13/viper"
)

// Mode constants for overlay configuration
const (
	ModeDisabled = "disabled"
	ModeEmbedded = "embedded"
)

// Config holds overlay engine configuration
type Config struct {
	Mode   string       `mapstructure:"mode"`
	Routes RoutesConfig `mapstructure:"routes"`
}

// RoutesConfig holds route configuration
type RoutesConfig struct {
	Enabled          bool   `mapstructure:"enabled"`
	Prefix           string `mapstructure:"prefix"`
	AdminBearerToken string `mapstructure:"admin_bearer_token"`
	ARCAPIKey        string `mapstructure:"arc_api_key"`
	ARCCallbackToken string `mapstructure:"arc_callback_token"`
}

// SetDefaults configures viper defaults for overlay settings
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	v.SetDefault(prefix+".mode", ModeDisabled)
	v.SetDefault(prefix+".routes.enabled", true)
	v.SetDefault(prefix+".routes.prefix", "/overlay")
}

// InitializeDeps holds dependencies required for overlay initialization
type InitializeDeps struct {
	OutputStore  *txo.OutputStore
	ChainTracker chaintracker.ChainTracker
	Storage      Storage // Optional: for dynamic topic sync (implements GetActiveTopics)
}

// Initialize creates overlay services from configuration
func (c *Config) Initialize(ctx context.Context, logger *slog.Logger, deps *InitializeDeps) (*Services, error) {
	if c.Mode == ModeDisabled {
		return nil, nil
	}

	if logger == nil {
		logger = slog.Default()
	}

	svc := &Services{
		logger:  logger,
		Storage: deps.Storage,
	}

	// Create the overlay engine
	eng := engine.NewEngine(&engine.EngineConfig{
		Managers:       make(map[string]engine.TopicManager),
		LookupServices: make(map[string]engine.LookupService),
		Storage:        deps.OutputStore,
		ChainTracker:   deps.ChainTracker,
	})
	svc.Engine = eng

	// Create routes if enabled
	if c.Routes.Enabled {
		svc.Routes = NewRoutes(eng, &c.Routes, logger)
	}

	logger.Info("overlay engine initialized", "mode", c.Mode)

	return svc, nil
}

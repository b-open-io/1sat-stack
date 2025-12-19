package fees

import (
	"context"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/spf13/viper"
)

// Mode constants
const (
	ModeDisabled = "disabled"
	ModeEmbedded = "embedded"
)

// Config holds fee service configuration
type Config struct {
	Mode string `mapstructure:"mode"` // disabled, embedded
}

// SetDefaults sets viper defaults for fee configuration
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	if prefix != "" {
		p = prefix + "."
	}

	v.SetDefault(p+"mode", ModeEmbedded)
}

// InitializeDeps holds dependencies for fee service initialization
type InitializeDeps struct {
	Store       store.Store
	OutputStore *txo.OutputStore
}

// Initialize creates a fee service from the configuration
func (c *Config) Initialize(ctx context.Context, logger *slog.Logger, deps *InitializeDeps) (*FeeService, error) {
	if c.Mode == ModeDisabled {
		return nil, nil
	}

	if logger == nil {
		logger = slog.Default()
	}

	feeService := NewFeeService(
		deps.Store,
		deps.OutputStore,
		logger,
	)

	logger.Info("fee service initialized", "mode", c.Mode)
	return feeService, nil
}

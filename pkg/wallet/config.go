package wallet

import (
	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/defs"
	"github.com/spf13/viper"
)

// Mode constants
const (
	ModeDisabled = "disabled"
	ModeEmbedded = "embedded"
)

// Config holds wallet service configuration.
type Config struct {
	Mode             string        `mapstructure:"mode"`               // disabled, embedded
	ServerPrivateKey string        `mapstructure:"server_private_key"` // Server private key for BRC-100 auth
	Name             string        `mapstructure:"name"`               // Storage name identifier
	DB               defs.Database `mapstructure:"db"`                 // Database configuration
	Routes           RoutesConfig  `mapstructure:"routes"`
}

// RoutesConfig holds route configuration
type RoutesConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Prefix  string `mapstructure:"prefix"`
}

// SetDefaults sets viper defaults for wallet configuration.
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	if prefix != "" {
		p = prefix + "."
	}

	v.SetDefault(p+"mode", ModeDisabled)
	v.SetDefault(p+"name", "1sat-wallet")
	v.SetDefault(p+"server_private_key", "")
	v.SetDefault(p+"routes.enabled", true)
	v.SetDefault(p+"routes.prefix", "/wallet")

	// Database defaults - SQLite by default
	v.SetDefault(p+"db.engine", "sqlite")
	v.SetDefault(p+"db.sqlite.connection_string", "./data/wallet.sqlite")
	v.SetDefault(p+"db.max_idle_connections", 5)
	v.SetDefault(p+"db.max_open_connections", 5)
}

package faucet

import "github.com/spf13/viper"

// Config holds faucet service configuration.
type Config struct {
	Enabled                       bool         `mapstructure:"enabled"`
	DefaultDropSats               int64        `mapstructure:"default_drop_sats"`
	DefaultMaxConsolidationInputs int64        `mapstructure:"default_max_consolidation_inputs"`
	DBPath                        string       `mapstructure:"db_path"`
	Routes                        RoutesConfig `mapstructure:"routes"`
}

// RoutesConfig holds route configuration.
type RoutesConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Prefix  string `mapstructure:"prefix"`
}

// SetDefaults sets viper defaults for faucet configuration.
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	if prefix != "" {
		p = prefix + "."
	}
	v.SetDefault(p+"enabled", false)
	v.SetDefault(p+"default_drop_sats", 5000)
	v.SetDefault(p+"default_max_consolidation_inputs", 5)
	v.SetDefault(p+"db_path", "faucet.db")
	v.SetDefault(p+"routes.enabled", true)
	v.SetDefault(p+"routes.prefix", "/faucet")
}

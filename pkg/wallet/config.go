package wallet

import (
	"github.com/spf13/viper"
)

// Config holds wallet service configuration.
type Config struct {
	ServerPrivateKey string `mapstructure:"server_private_key"` // Server private key for BRC-100 auth
}

// SetDefaults sets viper defaults for wallet configuration.
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	if prefix != "" {
		p = prefix + "."
	}

	v.SetDefault(p+"server_private_key", "")
}

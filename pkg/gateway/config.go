package gateway

import "github.com/spf13/viper"

const (
	// ModeDisabled turns the gateway off.
	ModeDisabled = "disabled"
	// ModeEmbedded runs the gateway in-process.
	ModeEmbedded = "embedded"
)

// Config holds gateway configuration. The gateway is a reverse proxy that
// fronts a multi-process deployment, presenting the public /1sat/* surface.
type Config struct {
	Mode string `mapstructure:"mode"`
	// Backends maps a service name (index, beef, bsv21, opns, bap, bsocial,
	// ordlock, ordfs, paymail) to that service's base URL, e.g.
	// index: "http://localhost:8081".
	Backends map[string]string `mapstructure:"backends"`
}

// SetDefaults configures viper defaults for the gateway.
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	v.SetDefault(prefix+".mode", ModeDisabled)
}

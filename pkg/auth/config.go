package auth

import "github.com/spf13/viper"

// Config holds auth middleware configuration.
type Config struct {
	// AllowUnauthenticated controls whether requests without BRC-103/104
	// authentication are allowed through. When true, unauthenticated requests
	// proceed with an unknown identity. When false, they get 401.
	AllowUnauthenticated bool `mapstructure:"allow_unauthenticated"`

	// ApiKey is an optional static key for development/agent access.
	// When set, requests with a matching X-Api-Key header bypass BRC-103/104.
	ApiKey string `mapstructure:"api_key"`
}

// SetDefaults sets viper defaults for auth configuration.
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	if prefix != "" {
		p = prefix + "."
	}

	v.SetDefault(p+"allow_unauthenticated", true)
	v.SetDefault(p+"api_key", "")
}

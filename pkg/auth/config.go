package auth

import "github.com/spf13/viper"

// Config holds auth middleware configuration.
type Config struct {
	// AllowUnauthenticated controls whether requests without BRC-103/104
	// authentication are allowed through. When true, unauthenticated requests
	// proceed with an unknown identity. When false, they get 401.
	AllowUnauthenticated bool `mapstructure:"allow_unauthenticated"`

	// AdminPubkeys is the list of identity public keys (DER hex) that have
	// admin access. Used by AdminGuard to authorize admin endpoints.
	AdminPubkeys []string `mapstructure:"admin_pubkeys"`
}

// SetDefaults sets viper defaults for auth configuration.
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	if prefix != "" {
		p = prefix + "."
	}

	v.SetDefault(p+"allow_unauthenticated", true)
	v.SetDefault(p+"admin_pubkeys", []string{})
}

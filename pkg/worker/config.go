package worker

import (
	"time"

	"github.com/spf13/viper"
)

// DefaultConfig holds default worker configuration values.
type DefaultConfig struct {
	Concurrency int           `mapstructure:"concurrency"`
	PageSize    uint32        `mapstructure:"page_size"`
	PollDelay   time.Duration `mapstructure:"poll_delay"`
	StatusDelay time.Duration `mapstructure:"status_delay"`
}

// SetDefaults sets viper defaults for worker configuration.
func (c *DefaultConfig) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	if prefix != "" {
		p = prefix + "."
	}

	v.SetDefault(p+"concurrency", 1)
	v.SetDefault(p+"page_size", 100)
	v.SetDefault(p+"poll_delay", "1s")
	v.SetDefault(p+"status_delay", "15s")
}

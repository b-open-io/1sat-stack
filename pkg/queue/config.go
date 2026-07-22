package queue

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/spf13/viper"
)

// Provider constants.
const (
	ProviderInherit = "inherit" // wrap the process's main store (same q:* keys)
	ProviderStore   = "store"   // dedicated store backend
)

// Config selects the queue provider.
type Config struct {
	Provider string       `mapstructure:"provider"` // "inherit" (default) | "store"
	Store    store.Config `mapstructure:"store"`    // dedicated backend when provider = "store"
}

// SetDefaults sets viper defaults for queue configuration.
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	storePrefix := "store"
	if prefix != "" {
		p = prefix + "."
		storePrefix = prefix + ".store"
	}
	v.SetDefault(p+"provider", ProviderInherit)
	c.Store.SetDefaults(v, storePrefix)
	// Keep the dedicated queue store in its own directory so provider=store
	// works without colliding with the main store's badger path.
	v.SetDefault(storePrefix+".badger.path", "queue")
}

// Initialize builds a Queue. With provider "inherit" it wraps mainStore; with
// provider "store" it opens a dedicated store backend that the returned Queue
// owns and closes.
func (c *Config) Initialize(ctx context.Context, logger *slog.Logger, mainStore store.Store) (Queue, error) {
	if logger == nil {
		logger = slog.Default()
	}

	switch c.Provider {
	case ProviderInherit, "":
		if mainStore == nil {
			return nil, fmt.Errorf("queue provider %q requires a main store", ProviderInherit)
		}
		return NewStoreQueue(mainStore), nil

	case ProviderStore:
		svc, err := c.Store.Initialize(ctx, logger)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize dedicated queue store: %w", err)
		}
		if svc == nil || svc.Store == nil {
			return nil, fmt.Errorf("dedicated queue store is disabled")
		}
		q := NewStoreQueue(svc.Store)
		q.closer = svc.Close
		return q, nil

	default:
		return nil, fmt.Errorf("unknown queue provider: %s", c.Provider)
	}
}

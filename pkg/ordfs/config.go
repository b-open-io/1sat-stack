package ordfs

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/spends"
	"github.com/spf13/viper"
)

// Config holds ORDFS configuration
type Config struct {
	Enabled bool `mapstructure:"enabled"`

	// Origin store path (Badger data directory)
	OriginStorePath string `mapstructure:"origin_store_path"`

	// Cache configuration
	Cache CacheConfig `mapstructure:"cache"`

	// Routes settings
	Routes RoutesConfig `mapstructure:"routes"`
}

// CacheConfig holds cache settings
type CacheConfig struct {
	LRUSize int `mapstructure:"lru_size"` // Max entries in LRU cache
}

// RoutesConfig holds route configuration
type RoutesConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Prefix  string `mapstructure:"prefix"`
}

// SetDefaults sets viper defaults for ORDFS configuration
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	if prefix != "" {
		p = prefix + "."
	}

	v.SetDefault(p+"enabled", true)
	v.SetDefault(p+"origin_store_path", "")
	v.SetDefault(p+"cache.lru_size", 10000)
	v.SetDefault(p+"routes.enabled", true)
	v.SetDefault(p+"routes.prefix", "/ordfs")
}

// Services holds initialized ORDFS services
type Services struct {
	Ordfs       *Ordfs
	Routes      *Routes
	originStore OriginStore
	coordinator *MemoryCoordinator
}

// Initialize creates ORDFS services from the configuration
func (c *Config) Initialize(
	ctx context.Context,
	logger *slog.Logger,
	dataDir string,
	spendsStorage *spends.Storage,
	beefStorage *beef.Storage,
) (*Services, error) {
	if !c.Enabled {
		return nil, nil
	}

	if logger == nil {
		logger = slog.Default()
	}

	if beefStorage == nil {
		return nil, fmt.Errorf("beef storage is required for ordfs")
	}

	if spendsStorage == nil {
		return nil, fmt.Errorf("spends storage is required for ordfs")
	}

	// Origin store
	storePath := c.OriginStorePath
	if storePath == "" {
		storePath = filepath.Join(dataDir, "ordfs")
	}
	originStore, err := NewBadgerOriginStore(storePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open origin store: %w", err)
	}
	logger.Info("ordfs origin store opened", "path", storePath)

	// Cache
	lruSize := c.Cache.LRUSize
	if lruSize <= 0 {
		lruSize = 10000
	}
	cache := NewLRUCache(lruSize)

	// Coordinator
	coordinator := NewMemoryCoordinator()

	ordfs := New(spendsStorage, beefStorage, originStore, cache, coordinator, logger)

	svc := &Services{
		Ordfs:       ordfs,
		originStore: originStore,
		coordinator: coordinator,
	}

	if c.Routes.Enabled {
		svc.Routes = NewRoutes(&RoutesDeps{
			Ordfs:  ordfs,
			Logger: logger,
		})
	}

	return svc, nil
}

// Close closes the ORDFS services
func (s *Services) Close() error {
	if s.coordinator != nil {
		s.coordinator.Close()
	}
	if s.originStore != nil {
		return s.originStore.Close()
	}
	return nil
}

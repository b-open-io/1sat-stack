package ordfs

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/spends"
	"github.com/spf13/viper"
)

// Origin store provider constants
const (
	OriginStoreProviderBadger = "badger"
	OriginStoreProviderRedis  = "redis"
)

// Config holds ORDFS configuration
type Config struct {
	Enabled bool `mapstructure:"enabled"`

	// Origin store backend: badger or redis
	OriginStoreProvider string `mapstructure:"origin_store_provider"`

	// Origin store path (Badger data directory)
	OriginStorePath string `mapstructure:"origin_store_path"`

	// Origin store Redis URL (redis provider only)
	OriginStoreRedisURL string `mapstructure:"origin_store_redis_url"`

	// Cache configuration
	Cache CacheConfig `mapstructure:"cache"`

	// Routes settings
	Routes RoutesConfig `mapstructure:"routes"`
}

// CacheConfig holds cache settings
type CacheConfig struct {
	LRUSize  int    `mapstructure:"lru_size"`  // Max entries in LRU cache
	RedisURL string `mapstructure:"redis_url"` // Optional Redis cache URL
	RedisTTL string `mapstructure:"redis_ttl"` // Redis TTL (e.g., "24h"), empty = no expiration
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
	v.SetDefault(p+"origin_store_provider", OriginStoreProviderBadger)
	v.SetDefault(p+"origin_store_path", "")
	v.SetDefault(p+"origin_store_redis_url", "")
	v.SetDefault(p+"cache.lru_size", 10000)
	v.SetDefault(p+"routes.enabled", true)
	v.SetDefault(p+"routes.prefix", "/ordfs")
}

// redactRedisURL strips credentials from a Redis URL for logging.
func redactRedisURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "invalid"
	}
	return u.Redacted()
}

// newOriginStore creates the origin store selected by origin_store_provider.
func (c *Config) newOriginStore(ctx context.Context, logger *slog.Logger, dataDir string) (OriginStore, error) {
	switch c.OriginStoreProvider {
	case OriginStoreProviderBadger:
		storePath := c.OriginStorePath
		if storePath == "" {
			storePath = filepath.Join(dataDir, "ordfs")
		}
		store, err := NewBadgerOriginStore(storePath)
		if err != nil {
			return nil, fmt.Errorf("failed to open badger origin store at %s: %w", storePath, err)
		}
		logger.Info("ordfs origin store opened", "provider", OriginStoreProviderBadger, "path", storePath)
		return store, nil

	case OriginStoreProviderRedis:
		if c.OriginStoreRedisURL == "" {
			return nil, fmt.Errorf("ordfs origin_store_redis_url is required when origin_store_provider is %s", OriginStoreProviderRedis)
		}
		store, err := NewRedisOriginStore(ctx, c.OriginStoreRedisURL)
		if err != nil {
			return nil, fmt.Errorf("failed to open redis origin store: %w", err)
		}
		logger.Info("ordfs origin store opened", "provider", OriginStoreProviderRedis, "url", redactRedisURL(c.OriginStoreRedisURL))
		return store, nil

	default:
		return nil, fmt.Errorf("unknown ordfs origin_store_provider %q: must be %s or %s", c.OriginStoreProvider, OriginStoreProviderBadger, OriginStoreProviderRedis)
	}
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

	originStore, err := c.newOriginStore(ctx, logger, dataDir)
	if err != nil {
		return nil, err
	}

	// Cache chain: LRU → optional Redis
	lruSize := c.Cache.LRUSize
	if lruSize <= 0 {
		lruSize = 10000
	}
	var cache Cache
	if c.Cache.RedisURL != "" {
		redisCache, err := NewRedisCache(ctx, c.Cache.RedisURL, c.Cache.RedisTTL)
		if err != nil {
			return nil, fmt.Errorf("failed to create redis cache: %w", err)
		}
		cache = NewCacheChain(NewLRUCache(lruSize), redisCache)
		logger.Info("ordfs cache: LRU + Redis", "lru_size", lruSize, "redis", redactRedisURL(c.Cache.RedisURL))
	} else {
		cache = NewLRUCache(lruSize)
		logger.Info("ordfs cache: LRU only", "lru_size", lruSize)
	}

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

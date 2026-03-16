package ordfs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/spends"
	"github.com/redis/go-redis/v9"
	"github.com/spf13/viper"
)

// Config holds ORDFS configuration
type Config struct {
	Enabled bool `mapstructure:"enabled"`

	// Redis configuration for ordinal chain caching
	Redis RedisConfig `mapstructure:"redis"`

	// Routes settings
	Routes RoutesConfig `mapstructure:"routes"`
}

// RedisConfig holds Redis connection settings
type RedisConfig struct {
	URL string `mapstructure:"url"` // e.g., "redis://user:pass@localhost:6379/0"
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

	v.SetDefault(p+"enabled", false)
	v.SetDefault(p+"redis.url", "redis://localhost:6379/0")
	v.SetDefault(p+"routes.enabled", true)
	v.SetDefault(p+"routes.prefix", "/ordfs")
}

// Services holds initialized ORDFS services
type Services struct {
	Ordfs  *Ordfs
	Routes *Routes
	redis  *redis.Client
}

// Initialize creates ORDFS services from the configuration
func (c *Config) Initialize(
	ctx context.Context,
	logger *slog.Logger,
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

	// Create Redis client
	opts, err := redis.ParseURL(c.Redis.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse redis url: %w", err)
	}
	redisClient := redis.NewClient(opts)

	// Test connection
	if err := redisClient.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to redis: %w", err)
	}

	logger.Info("ordfs connected to redis", "url", c.Redis.URL)

	// Create ordfs service
	ordfs := New(spendsStorage, beefStorage, redisClient, logger)

	svc := &Services{
		Ordfs: ordfs,
		redis: redisClient,
	}

	// Create routes if enabled
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
	if s.redis != nil {
		return s.redis.Close()
	}
	return nil
}

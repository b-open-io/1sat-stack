package ordfs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/b-open-io/go-junglebus"
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
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
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
	v.SetDefault(p+"redis.addr", "localhost:6379")
	v.SetDefault(p+"redis.password", "")
	v.SetDefault(p+"redis.db", 0)
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
	jb *junglebus.Client,
) (*Services, error) {
	if !c.Enabled {
		return nil, nil
	}

	if logger == nil {
		logger = slog.Default()
	}

	if jb == nil {
		return nil, fmt.Errorf("junglebus client is required for ordfs")
	}

	// Create Redis client
	redisClient := redis.NewClient(&redis.Options{
		Addr:     c.Redis.Addr,
		Password: c.Redis.Password,
		DB:       c.Redis.DB,
	})

	// Test connection
	if err := redisClient.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to redis: %w", err)
	}

	logger.Info("ordfs connected to redis", "addr", c.Redis.Addr, "db", c.Redis.DB)

	// Create ordfs service
	ordfs := New(jb, redisClient, logger)

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

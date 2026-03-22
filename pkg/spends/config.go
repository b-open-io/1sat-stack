package spends

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/go-junglebus"
	"github.com/redis/go-redis/v9"
	"github.com/spf13/viper"
)

const (
	ModeDisabled = "disabled"
	ModeEmbedded = "embedded"
)

const (
	ProviderLRU       = "lru"
	ProviderStore     = "store"
	ProviderJungleBus = "junglebus"
)

type Config struct {
	Mode  string        `mapstructure:"mode"`
	Chain []ChainConfig `mapstructure:"chain"`
}

type ChainConfig struct {
	Provider string    `mapstructure:"provider"`
	LRU      LRUConfig `mapstructure:"lru"`
}

type LRUConfig struct {
	Size string `mapstructure:"size"`
}

type Services struct {
	Storage *Storage
}

func (s *Services) Close() error {
	if s != nil && s.Storage != nil {
		return s.Storage.Close()
	}
	return nil
}

func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	if prefix != "" {
		p = prefix + "."
	}
	v.SetDefault(p+"mode", ModeEmbedded)
}

func (c *Config) Initialize(
	ctx context.Context,
	logger *slog.Logger,
	client *redis.Client,
	jbClient *junglebus.Client,
) (*Services, error) {
	if c.Mode == ModeDisabled {
		return nil, nil
	}

	if logger == nil {
		logger = slog.Default()
	}

	switch c.Mode {
	case ModeEmbedded:
		return c.initializeEmbedded(logger, client, jbClient)
	default:
		return nil, fmt.Errorf("unknown spends mode: %s", c.Mode)
	}
}

func (c *Config) initializeEmbedded(
	logger *slog.Logger,
	client *redis.Client,
	jbClient *junglebus.Client,
) (*Services, error) {
	var storages []BaseSpendStorage

	if len(c.Chain) == 0 {
		if client != nil {
			storages = append(storages, NewStoreSpendStorage(client))
		}
	} else {
		for i, chainItem := range c.Chain {
			storage, err := createStorageFromConfig(chainItem, client, jbClient)
			if err != nil {
				return nil, fmt.Errorf("failed to create storage at chain index %d: %w", i, err)
			}
			if storage == nil {
				logger.Warn("skipping nil storage provider", "index", i, "provider", chainItem.Provider)
				continue
			}
			storages = append(storages, storage)
		}
	}

	if len(storages) == 0 {
		return nil, nil
	}

	return &Services{Storage: NewStorageFromProviders(storages)}, nil
}

func createStorageFromConfig(
	cfg ChainConfig,
	client *redis.Client,
	jbClient *junglebus.Client,
) (BaseSpendStorage, error) {
	switch cfg.Provider {
	case ProviderLRU:
		if cfg.LRU.Size == "" {
			return nil, fmt.Errorf("LRU size is required")
		}
		size, err := beef.ParseSize(cfg.LRU.Size)
		if err != nil {
			return nil, fmt.Errorf("invalid LRU size %q: %w", cfg.LRU.Size, err)
		}
		return NewLRUSpendStorage(size), nil

	case ProviderStore:
		if client == nil {
			return nil, nil
		}
		return NewStoreSpendStorage(client), nil

	case ProviderJungleBus:
		if jbClient == nil {
			return nil, nil
		}
		return NewJunglebusSpendStorage(jbClient), nil

	default:
		return nil, fmt.Errorf("unknown spends provider: %s", cfg.Provider)
	}
}

package beef

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/b-open-io/go-junglebus"
	"github.com/bsv-blockchain/go-sdk/transaction/chaintracker"
	"github.com/spf13/viper"
)

// Mode constants
const (
	ModeDisabled = "disabled"
	ModeEmbedded = "embedded"
	ModeRemote   = "remote"
)

// Provider constants
const (
	ProviderLRU        = "lru"
	ProviderFilesystem = "filesystem"
	ProviderJungleBus  = "junglebus"
	ProviderBadger     = "badger"
)

// Config holds BEEF storage configuration.
type Config struct {
	Mode  string        `mapstructure:"mode"` // disabled, embedded, remote
	Chain []ChainConfig `mapstructure:"chain"`

	Routes RoutesConfig `mapstructure:"routes"`
}

// ChainConfig represents a single storage provider in the chain
type ChainConfig struct {
	Provider   string           `mapstructure:"provider"` // lru, filesystem, junglebus, badger
	LRU        LRUConfig        `mapstructure:"lru"`
	Filesystem FilesystemConfig `mapstructure:"filesystem"`
	Badger     BadgerConfig     `mapstructure:"badger"`
	// JungleBus uses the system client, no config needed here
}

// BadgerConfig holds badger storage configuration
type BadgerConfig struct {
	Path string `mapstructure:"path"`
}

// LRUConfig holds LRU cache configuration
type LRUConfig struct {
	Size string `mapstructure:"size"` // e.g., "100mb", "1gb"
}

// FilesystemConfig holds filesystem storage configuration
type FilesystemConfig struct {
	Path string `mapstructure:"path"`
}

// RoutesConfig holds route configuration
type RoutesConfig struct {
	Enabled bool `mapstructure:"enabled"`
}

// SetDefaults sets viper defaults for beef configuration.
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	if prefix != "" {
		p = prefix + "."
	}

	v.SetDefault(p+"mode", ModeDisabled)
	v.SetDefault(p+"routes.enabled", true)
	// Note: chain defaults are not set here - users must configure explicitly
}

// DefaultStoragePath returns the default filesystem path for BEEF storage.
func DefaultStoragePath() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "./beef/"
	}
	return filepath.Join(homeDir, ".1sat", "beef")
}

// Services holds initialized beef services.
type Services struct {
	Storage *Storage
	Routes  *Routes // nil if routes not enabled or remote mode
}

// Initialize creates a BEEF Storage from the configuration.
func (c *Config) Initialize(
	ctx context.Context,
	logger *slog.Logger,
	chainTracker chaintracker.ChainTracker,
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
		return c.initializeEmbedded(ctx, logger, chainTracker, jbClient)

	case ModeRemote:
		// TODO: Implement remote HTTP client for beef
		return nil, fmt.Errorf("remote mode not yet implemented for beef")

	default:
		return nil, fmt.Errorf("unknown beef mode: %s", c.Mode)
	}
}

// initializeEmbedded creates embedded storage from the chain config
func (c *Config) initializeEmbedded(
	ctx context.Context,
	logger *slog.Logger,
	chainTracker chaintracker.ChainTracker,
	jbClient *junglebus.Client,
) (*Services, error) {
	var storages []BaseBeefStorage

	// If no chain configured, use default filesystem storage
	if len(c.Chain) == 0 {
		fs, err := NewFilesystemBeefStorage(DefaultStoragePath())
		if err != nil {
			return nil, fmt.Errorf("failed to create default filesystem storage: %w", err)
		}
		storages = append(storages, fs)
	} else {
		for i, chainItem := range c.Chain {
			storage, err := c.createStorageFromConfig(chainItem, jbClient, logger)
			if err != nil {
				return nil, fmt.Errorf("failed to create storage at chain index %d: %w", i, err)
			}
			if storage == nil {
				return nil, fmt.Errorf("storage at chain index %d is nil", i)
			}
			storages = append(storages, storage)
		}
	}

	storage := NewStorageFromProviders(storages, chainTracker)

	svc := &Services{Storage: storage}

	if c.Routes.Enabled {
		svc.Routes = NewRoutes(storage)
	}

	return svc, nil
}

// createStorageFromConfig creates a storage provider from config
func (c *Config) createStorageFromConfig(
	cfg ChainConfig,
	jbClient *junglebus.Client,
	logger *slog.Logger,
) (BaseBeefStorage, error) {
	switch cfg.Provider {
	case ProviderLRU:
		if cfg.LRU.Size == "" {
			return nil, fmt.Errorf("LRU size is required")
		}
		size, err := ParseSize(cfg.LRU.Size)
		if err != nil {
			return nil, fmt.Errorf("invalid LRU size %q: %w", cfg.LRU.Size, err)
		}
		return NewLRUBeefStorage(size), nil

	case ProviderFilesystem:
		path := cfg.Filesystem.Path
		if path == "" {
			path = DefaultStoragePath()
		}
		return NewFilesystemBeefStorage(expandPath(path))

	case ProviderJungleBus:
		if jbClient == nil {
			return nil, fmt.Errorf("junglebus provider requires a junglebus client to be passed to Initialize()")
		}
		return NewJunglebusBeefStorageWithClient(jbClient), nil

	case ProviderBadger:
		path := cfg.Badger.Path
		if path == "" {
			path = DefaultBadgerPath()
		}
		return NewBadgerBeefStorageFromPath(expandPath(path), logger)

	default:
		return nil, fmt.Errorf("unknown beef provider: %s", cfg.Provider)
	}
}

// expandPath expands ~ to home directory
func expandPath(path string) string {
	if len(path) > 0 && path[0] == '~' {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[1:])
		}
	}
	return path
}

// Close closes the beef storage.
func (s *Services) Close() error {
	if s != nil && s.Storage != nil {
		return s.Storage.Close()
	}
	return nil
}

package indexer

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/pubsub"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/spf13/viper"
)

// Mode constants
const (
	ModeDisabled = "disabled"
	ModeEmbedded = "embedded"
	ModeRemote   = "remote"
)

// Config holds indexer configuration.
type Config struct {
	Mode string `mapstructure:"mode"` // disabled, embedded, remote
	URL  string `mapstructure:"url"`  // For remote mode

	// Indexer settings
	Tag         string   `mapstructure:"tag"`
	Indexers    []string `mapstructure:"indexers"` // List of indexer tags to enable
	Concurrency int      `mapstructure:"concurrency"`
	Verbose     bool     `mapstructure:"verbose"`
	Network     string   `mapstructure:"network"` // mainnet, testnet

	Routes RoutesConfig `mapstructure:"routes"`
}

// RoutesConfig holds route configuration
type RoutesConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Prefix  string `mapstructure:"prefix"`
}

// SetDefaults sets viper defaults for indexer configuration.
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	if prefix != "" {
		p = prefix + "."
	}

	v.SetDefault(p+"mode", ModeDisabled)
	v.SetDefault(p+"tag", IngestTag)
	v.SetDefault(p+"indexers", []string{})
	v.SetDefault(p+"concurrency", 1)
	v.SetDefault(p+"verbose", false)
	v.SetDefault(p+"network", "mainnet")
	v.SetDefault(p+"routes.enabled", true)
	v.SetDefault(p+"routes.prefix", "")
}

// Services holds initialized indexer services.
type Services struct {
	IngestCtx *IngestCtx
	Routes    *Routes // nil if routes not enabled
}

// Initialize creates an IndexerService from the configuration.
func (c *Config) Initialize(
	ctx context.Context,
	logger *slog.Logger,
	txoStore *txo.OutputStore,
	beefStore *beef.Storage,
	ps pubsub.PubSub,
	indexers []Indexer,
) (*Services, error) {
	if c.Mode == ModeDisabled {
		return nil, nil
	}

	if logger == nil {
		logger = slog.Default()
	}

	switch c.Mode {
	case ModeEmbedded:
		network := types.Mainnet
		if c.Network == "testnet" {
			network = types.Testnet
		}

		ingestCtx := &IngestCtx{
			Tag:         c.Tag,
			Key:         QueueKey(c.Tag),
			Indexers:    indexers,
			Concurrency: uint(c.Concurrency),
			Verbose:     c.Verbose,
			PageSize:    100,
			Network:     network,
			Store:       txoStore,
			BeefStorage: beefStore,
			Logger:      logger,
		}

		svc := &Services{IngestCtx: ingestCtx}

		if c.Routes.Enabled {
			svc.Routes = NewRoutes(ingestCtx, ps)
		}

		return svc, nil

	case ModeRemote:
		// TODO: Implement remote indexer client
		return nil, fmt.Errorf("remote mode not yet implemented for indexer")

	default:
		return nil, fmt.Errorf("unknown indexer mode: %s", c.Mode)
	}
}

// Close closes the indexer service.
func (s *Services) Close() error {
	// Nothing to close for embedded mode
	return nil
}

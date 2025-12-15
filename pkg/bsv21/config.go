package bsv21

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/indexer"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/bsv-blockchain/go-chaintracks/chaintracks"
	"github.com/spf13/viper"
)

// Mode constants
const (
	ModeDisabled = "disabled"
	ModeEmbedded = "embedded"
	ModeRemote   = "remote"
)

// Config holds BSV21 configuration
type Config struct {
	Mode string `mapstructure:"mode"` // disabled, embedded, remote

	// Indexer settings
	WhitelistTokens []string `mapstructure:"whitelist_tokens"` // Token IDs to index (empty = all)
	BlacklistTokens []string `mapstructure:"blacklist_tokens"` // Token IDs to exclude
	Network         string   `mapstructure:"network"`          // mainnet, testnet

	// Routes settings
	Routes RoutesConfig `mapstructure:"routes"`
}

// RoutesConfig holds route configuration
type RoutesConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Prefix  string `mapstructure:"prefix"`
}

// SetDefaults sets viper defaults for BSV21 configuration
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	if prefix != "" {
		p = prefix + "."
	}

	v.SetDefault(p+"mode", ModeDisabled)
	v.SetDefault(p+"whitelist_tokens", []string{})
	v.SetDefault(p+"blacklist_tokens", []string{})
	v.SetDefault(p+"network", "mainnet")
	v.SetDefault(p+"routes.enabled", true)
	v.SetDefault(p+"routes.prefix", "/bsv21")
}

// Services holds initialized BSV21 services
type Services struct {
	Indexer *Indexer
	Lookup  *Lookup
	Routes  *Routes
}

// Initialize creates BSV21 services from the configuration
func (c *Config) Initialize(
	ctx context.Context,
	logger *slog.Logger,
	txoStorage *txo.OutputStore,
	chaintracker chaintracks.Chaintracks,
	activeTopics func(topic string) bool,
) (*Services, error) {
	if c.Mode == ModeDisabled {
		return nil, nil
	}

	if logger == nil {
		logger = slog.Default()
	}

	switch c.Mode {
	case ModeEmbedded:
		// Determine network
		network := indexer.Mainnet
		if c.Network == "testnet" {
			network = indexer.Testnet
		}

		// Create indexer
		idx := NewIndexer(network)
		idx.Logger = logger

		// Set whitelist/blacklist functions if configured
		if len(c.WhitelistTokens) > 0 {
			whitelist := make(map[string]struct{}, len(c.WhitelistTokens))
			for _, id := range c.WhitelistTokens {
				whitelist[id] = struct{}{}
			}
			idx.WhitelistFn = func(tokenId string) bool {
				_, ok := whitelist[tokenId]
				return ok
			}
		}
		if len(c.BlacklistTokens) > 0 {
			blacklist := make(map[string]struct{}, len(c.BlacklistTokens))
			for _, id := range c.BlacklistTokens {
				blacklist[id] = struct{}{}
			}
			idx.BlacklistFn = func(tokenId string) bool {
				_, ok := blacklist[tokenId]
				return ok
			}
		}

		// Create lookup service
		lookup := NewLookup(txoStorage)

		svc := &Services{
			Indexer: idx,
			Lookup:  lookup,
		}

		// Create routes if enabled
		if c.Routes.Enabled && txoStorage != nil {
			svc.Routes = NewRoutes(&RoutesDeps{
				Storage:      txoStorage,
				Lookup:       lookup,
				ChainTracker: chaintracker,
				ActiveTopics: activeTopics,
				Logger:       logger,
			})
		}

		return svc, nil

	case ModeRemote:
		return nil, fmt.Errorf("remote mode not yet implemented for bsv21")

	default:
		return nil, fmt.Errorf("unknown bsv21 mode: %s", c.Mode)
	}
}

// Close closes the BSV21 services
func (s *Services) Close() error {
	// Nothing to close
	return nil
}

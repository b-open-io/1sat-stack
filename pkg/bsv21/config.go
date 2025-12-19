package bsv21

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	lookuppkg "github.com/b-open-io/1sat-stack/pkg/lookup"
	"github.com/b-open-io/1sat-stack/pkg/overlay"
	topicpkg "github.com/b-open-io/1sat-stack/pkg/topic"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/b-open-io/go-junglebus"
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

	// Sync settings
	Sync *SyncConfig `mapstructure:"sync"` // JungleBus sync configuration

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
	v.SetDefault(p+"sync.enabled", false)
	v.SetDefault(p+"sync.categorizer_workers", 8)
	v.SetDefault(p+"routes.enabled", true)
	v.SetDefault(p+"routes.prefix", "/bsv21")
}

// Services holds initialized BSV21 services
type Services struct {
	Indexer      *Indexer
	Lookup       *lookuppkg.BSV21Lookup
	TopicManager *topicpkg.Bsv21ValidatedTopicManager
	Sync         *SyncServices
	Routes       *Routes
}

// Initialize creates BSV21 services from the configuration
func (c *Config) Initialize(
	ctx context.Context,
	logger *slog.Logger,
	txoStorage *txo.OutputStore,
	chaintracker chaintracks.Chaintracks,
	beefStorage *beef.Storage,
	overlaySvc *overlay.Services,
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
		// Determine network
		network := types.Mainnet
		if c.Network == "testnet" {
			network = types.Testnet
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
		bsv21Lookup := lookuppkg.NewBSV21Lookup(txoStorage)

		// Create topic manager for overlay engine integration
		topicManager := topicpkg.NewBsv21ValidatedTopicManager("bsv21", txoStorage, c.WhitelistTokens)

		svc := &Services{
			Indexer:      idx,
			Lookup:       bsv21Lookup,
			TopicManager: topicManager,
		}

		// Create sync services if enabled
		if c.Sync != nil && c.Sync.Enabled {
			syncSvc, err := NewSyncServices(
				c.Sync,
				txoStorage.Store,
				beefStorage,
				overlaySvc,
				nil, // feeService - not needed for categorization only
				chaintracker,
				jbClient,
				logger,
			)
			if err != nil {
				return nil, fmt.Errorf("failed to create BSV21 sync services: %w", err)
			}
			svc.Sync = syncSvc
		}

		// Create routes if enabled
		if c.Routes.Enabled && txoStorage != nil {
			svc.Routes = NewRoutes(&RoutesDeps{
				Storage:      txoStorage,
				Lookup:       bsv21Lookup,
				ChainTracker: chaintracker,
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

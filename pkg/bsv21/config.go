package bsv21

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	lookuppkg "github.com/b-open-io/1sat-stack/pkg/lookup"
	"github.com/b-open-io/1sat-stack/pkg/overlay"
	"github.com/b-open-io/1sat-stack/pkg/txo"
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
	v.SetDefault(p+"sync.subscription_id", "")
	v.SetDefault(p+"sync.from_block", 783968)
	v.SetDefault(p+"sync.batch_size", 1000)
	v.SetDefault(p+"sync.reorg_depth", 6)
	v.SetDefault(p+"sync.categorizer_workers", 8)
	v.SetDefault(p+"sync.lifecycle_interval", "5m")
	v.SetDefault(p+"routes.enabled", true)
	v.SetDefault(p+"routes.prefix", "/bsv21")
}

// Services holds initialized BSV21 services
type Services struct {
	Lookup       *lookuppkg.BSV21Lookup
	TopicManager *Bsv21ValidatedTopicManager
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
		// Create lookup service
		bsv21Lookup := lookuppkg.NewBSV21Lookup(txoStorage)

		// Create topic manager for overlay engine integration
		topicManager := NewBsv21ValidatedTopicManager("bsv21", txoStorage, c.WhitelistTokens, nil)

		svc := &Services{
			Lookup:       bsv21Lookup,
			TopicManager: topicManager,
		}

		// Create sync services if enabled
		if c.Sync != nil && c.Sync.Enabled {
			syncSvc, err := NewSyncServices(
				c.Sync,
				txoStorage.Store,
				beefStorage,
				txoStorage,
				overlaySvc,
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
			deps := &RoutesDeps{
				Storage:      txoStorage,
				Lookup:       bsv21Lookup,
				ChainTracker: chaintracker,
				Logger:       logger,
			}
			// Include token manager if sync is enabled
			if svc.Sync != nil && svc.Sync.manager != nil {
				deps.Manager = svc.Sync.manager
			}
			svc.Routes = NewRoutes(deps)
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

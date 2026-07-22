package bsv21

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/config"
	lookuppkg "github.com/b-open-io/1sat-stack/pkg/lookup"
	"github.com/b-open-io/1sat-stack/pkg/overlay"
	"github.com/b-open-io/1sat-stack/pkg/queue"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/b-open-io/go-junglebus"
	"github.com/bsv-blockchain/go-chaintracks/chaintracks"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/spf13/viper"
)

// Mode constants
const (
	ModeDisabled = "disabled"
	ModeEmbedded = "embedded"
	ModeRemote   = "remote"
)

// Owner mode constants select where fee-address sync and balance lookups
// resolve: embedded requires an in-process index; remote uses HTTP.
const (
	OwnerModeEmbedded = "embedded"
	OwnerModeRemote   = "remote"
)

// OwnerConfig selects how bsv21 syncs fee addresses and reads their balance.
type OwnerConfig struct {
	Mode string `mapstructure:"mode"` // embedded (default) | remote
	URL  string `mapstructure:"url"`  // index owner mount when mode=remote, e.g. https://api.1sat.app/1sat/owner
}

// Config holds BSV21 configuration
type Config struct {
	Mode     string `mapstructure:"mode"`      // disabled, embedded, remote
	LogLevel string `mapstructure:"log_level"` // debug, info, warn, error

	// Indexer settings
	WhitelistTokens []string `mapstructure:"whitelist_tokens"` // Token IDs to index (empty = all)
	BlacklistTokens []string `mapstructure:"blacklist_tokens"` // Token IDs to exclude
	Network         string   `mapstructure:"network"`          // mainnet, testnet

	// Sync settings
	Sync *SyncConfig `mapstructure:"sync"` // JungleBus sync configuration

	// Owner sync/balance resolution (embedded in-process index or remote HTTP)
	Owner OwnerConfig `mapstructure:"owner"`

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
	v.SetDefault(p+"owner.mode", OwnerModeEmbedded)
	v.SetDefault(p+"owner.url", "")
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
	Engine        *engine.Engine
	Lookup        *lookuppkg.BSV21Lookup
	TopicManager  *Bsv21ValidatedTopicManager
	Sync          *SyncServices
	Routes        *Routes
	OverlayRoutes *overlay.Routes
}

// Initialize creates BSV21 services from the configuration
func (c *Config) Initialize(
	ctx context.Context,
	logger *slog.Logger,
	txoStorage *txo.OutputStore,
	deps *overlay.ModuleDeps,
	configStore config.Store,
	chaintracker chaintracks.Chaintracks,
	beefStorage *beef.Storage,
	jbClient *junglebus.Client,
	q queue.Queue,
	ownerSync OwnerSyncer,
	balance BalanceLookup,
) (*Services, error) {
	if c.Mode == ModeDisabled {
		return nil, nil
	}

	if logger == nil {
		logger = slog.Default()
	}

	switch c.Mode {
	case ModeEmbedded:
		bsv21Lookup := lookuppkg.NewBSV21Lookup(deps.Factory)
		topicManager := NewBsv21ValidatedTopicManager("bsv21", c.WhitelistTokens, nil)
		discoveryManager := NewBsv21DiscoveryTopicManager("tm_bsv21", logger)

		eng := overlay.NewModuleEngine(deps,
			map[string]engine.TopicManager{
				"bsv21":    topicManager,
				"tm_bsv21": discoveryManager,
			},
			map[string]engine.LookupService{
				"bsv21": bsv21Lookup,
			},
		)

		svc := &Services{
			Engine:       eng,
			Lookup:       bsv21Lookup,
			TopicManager: topicManager,
		}

		// Create sync services if enabled
		if c.Sync != nil && c.Sync.Enabled {
			syncSvc, err := NewSyncServices(
				c.Sync,
				q,
				configStore,
				beefStorage,
				txoStorage,
				eng,
				bsv21Lookup,
				ownerSync,
				balance,
				chaintracker,
				jbClient,
				logger,
			)
			if err != nil {
				return nil, fmt.Errorf("failed to create BSV21 sync services: %w", err)
			}
			svc.Sync = syncSvc
		}

		// Create routes if enabled. txoStorage is optional: only GetTransaction's
		// BEEF passthrough uses it, and it nil-guards. This lets bsv21 serve its
		// API in a standalone process with no in-process index (remote owner).
		if c.Routes.Enabled {
			routesDeps := &RoutesDeps{
				Storage: txoStorage,
				Lookup:  bsv21Lookup,
				Logger:  logger,
			}
			if svc.Sync != nil && svc.Sync.manager != nil {
				routesDeps.Manager = svc.Sync.manager
			}
			svc.Routes = NewRoutes(routesDeps)
		}

		if deps.RoutesConfig != nil && deps.RoutesConfig.Enabled {
			svc.OverlayRoutes = overlay.NewRoutes(eng, deps.RoutesConfig, logger)
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
	return nil
}

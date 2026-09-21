// Package gib is the overlay module that indexes gib commit heads: the
// PushDrop coins that name a repository's branch tips. It admits a head into
// tm_gib only with the push it publishes, keeps the full push history (spend
// chain) per branch in its own table, and serves REST and BRC-24 lookups by
// repository, branch, and publisher identity.
//
// Heads enter one way: a client submits them. gib is a far more explicit
// push than anything else in this stack — the client holds the repository
// and sends the head together with the content transactions that prove it —
// so there is no queue, no chain-feed sync and no discovery here. Repository
// exchange between overlays is peer to peer.
package gib

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/overlay"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/spf13/viper"
)

const (
	ModeDisabled = "disabled"
	ModeEmbedded = "embedded"

	// TopicName is the overlay topic for commit heads.
	TopicName = "tm_gib"
	// LookupName is the BRC-24 lookup service name.
	LookupName = "ls_gib"
	// ProtocolVersion is reported in topic/lookup metadata.
	ProtocolVersion = "1"
)

// Config holds gib overlay configuration. There is no sync section: this
// module has no queue and ingests nothing from a chain feed.
type Config struct {
	Mode     string       `mapstructure:"mode"`
	LogLevel string       `mapstructure:"log_level"`
	Routes   RoutesConfig `mapstructure:"routes"`
}

// RoutesConfig controls the module's REST surface.
type RoutesConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Prefix  string `mapstructure:"prefix"`
}

// SetDefaults configures gib defaults.
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	if prefix != "" {
		p = prefix + "."
	}
	v.SetDefault(p+"mode", ModeDisabled)
	v.SetDefault(p+"routes.enabled", true)
	v.SetDefault(p+"routes.prefix", "/gib")
}

// Services holds initialized gib services.
type Services struct {
	Engine        *engine.Engine
	Lookup        *LookupService
	TopicManager  *TopicManager
	Store         *Store
	Routes        *Routes
	OverlayRoutes *overlay.Routes
}

// Initialize creates the gib engine. Topic storage is owned by ModuleDeps.
func (c *Config) Initialize(ctx context.Context, logger *slog.Logger, deps *overlay.ModuleDeps) (*Services, error) {
	if c.Mode == "" || c.Mode == ModeDisabled {
		return nil, nil
	}
	if logger == nil {
		logger = slog.Default()
	}

	switch c.Mode {
	case ModeEmbedded:
		if deps == nil || deps.Factory == nil {
			return nil, fmt.Errorf("overlay ModuleDeps with Factory is required for gib")
		}
		topicStorage, err := deps.Factory(TopicName)
		if err != nil {
			return nil, fmt.Errorf("failed to get gib topic storage: %w", err)
		}
		if topicStorage == nil {
			return nil, fmt.Errorf("gib topic storage is required")
		}

		store := NewStore(topicStorage.DB(), topicStorage.TopicID(), logger)
		lookup := NewLookupService(store, logger)
		// The sync lookups hand out BEEF, so they read the shared BEEF store
		// directly. Without it they refuse rather than answer partially.
		if deps.BeefStorage != nil {
			lookup.SetBeefLoader(deps.BeefStorage)
		}
		topicManager := &TopicManager{Logger: logger}
		eng := overlay.NewModuleEngine(deps,
			map[string]engine.TopicManager{TopicName: topicManager},
			map[string]engine.LookupService{LookupName: lookup},
		)

		svc := &Services{
			Engine:       eng,
			Lookup:       lookup,
			TopicManager: topicManager,
			Store:        store,
		}
		if c.Routes.Enabled {
			svc.Routes = NewRoutes(store, logger)
		}
		if deps.RoutesConfig != nil && deps.RoutesConfig.Enabled {
			svc.OverlayRoutes = overlay.NewRoutes(eng, deps.RoutesConfig, logger)
		}
		return svc, nil

	default:
		return nil, fmt.Errorf("unknown gib mode: %s", c.Mode)
	}
}

// Close releases the module's own resources. There are none: the topic
// database is owned by ModuleDeps, and the module runs no background work
// of its own — heads arrive by submission, on the caller's goroutine.
func (s *Services) Close() error {
	return nil
}

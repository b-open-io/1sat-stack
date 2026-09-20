// Package gib is the overlay module that indexes gib commit heads: the
// PushDrop coins that name a repository's branch tips. It admits every valid
// head into tm_gib, keeps the full push history (spend chain) per branch in
// its own table, and serves REST and BRC-24 lookups by repository, branch,
// and publisher identity.
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
	// QueueName is the overlay work queue (q:gib) fed by the event bridge
	// and the optional JungleBus subscriber.
	QueueName = "gib"
	// ProtocolVersion is reported in topic/lookup metadata.
	ProtocolVersion = "1"
)

// Config holds gib overlay configuration.
type Config struct {
	Mode     string                     `mapstructure:"mode"`
	LogLevel string                     `mapstructure:"log_level"`
	Sync     *overlay.OverlaySyncConfig `mapstructure:"sync"`
	Routes   RoutesConfig               `mapstructure:"routes"`
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
	v.SetDefault(p+"sync.enabled", false)
	v.SetDefault(p+"sync.subscription_id", "")
	v.SetDefault(p+"sync.queue_name", QueueName)
	v.SetDefault(p+"sync.from_block", 0)
	// One worker: q:gib members are ordered by arrival, so a head is applied
	// before the push that spends it (same reasoning as ordlock).
	v.SetDefault(p+"sync.concurrency", 1)
	v.SetDefault(p+"sync.batch_size", 1000)
	v.SetDefault(p+"sync.reorg_depth", 6)
	v.SetDefault(p+"sync.resolve_dependencies", false)
	v.SetDefault(p+"routes.enabled", true)
	v.SetDefault(p+"routes.prefix", "/gib")
}

// Services holds initialized gib services.
type Services struct {
	Engine        *engine.Engine
	Lookup        *LookupService
	TopicManager  *TopicManager
	Store         *Store
	Sync          *overlay.OverlaySync
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
		topicManager := &TopicManager{}
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

// Close stops background work. The topic database is owned by ModuleDeps.
func (s *Services) Close() error {
	if s == nil {
		return nil
	}
	if s.Sync != nil {
		s.Sync.Stop()
	}
	return nil
}

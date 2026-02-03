package indexer

import (
	"context"
	"log/slog"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/logging"
	"github.com/b-open-io/1sat-stack/pkg/pubsub"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/bsv-blockchain/arcade/events"
	"github.com/bsv-blockchain/go-sdk/transaction/chaintracker"
	"github.com/spf13/viper"
)

// Mode constants
const (
	ModeDisabled = "disabled"
	ModeEmbedded = "embedded"
)

// Config holds indexer service configuration.
type Config struct {
	Mode     string   `mapstructure:"mode"`      // disabled, embedded
	Verbose  bool     `mapstructure:"verbose"`   // Enable verbose logging
	Tags     []string `mapstructure:"tags"`      // Parse tags to run (nil = use parse.DefaultTags)
	LogLevel string   `mapstructure:"log_level"` // Log level (debug, info, warn, error)

	Sync   SyncConfig   `mapstructure:"sync"`
	Arcade ArcadeConfig `mapstructure:"arcade"`
	Routes RoutesConfig `mapstructure:"routes"`
}

// ArcadeConfig holds configuration for arcade event listener.
type ArcadeConfig struct {
	Enabled bool `mapstructure:"enabled"` // Enable arcade event listener
	Ingest  bool `mapstructure:"ingest"`  // Ingest transactions on ACCEPTED status
}

// SyncConfig holds configuration for the ingest sync worker.
type SyncConfig struct {
	Enabled     bool          `mapstructure:"enabled"`      // Enable the ingest sync worker
	QueueName   string        `mapstructure:"queue_name"`   // Queue to consume from (default: "ingest")
	Concurrency int           `mapstructure:"concurrency"`  // Worker concurrency (default: 8)
	PageSize    uint32        `mapstructure:"page_size"`    // Batch fetch size (default: 100)
	PollDelay   time.Duration `mapstructure:"poll_delay"`   // Sleep when queue empty (default: 1s)
	StatusDelay time.Duration `mapstructure:"status_delay"` // Status log interval (default: 15s)
}

// RoutesConfig holds route configuration.
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

	v.SetDefault(p+"mode", ModeEmbedded)
	v.SetDefault(p+"verbose", false)
	// Don't set default for tags - nil means use parse.DefaultTags
	v.SetDefault(p+"log_level", "info")

	// Sync defaults
	v.SetDefault(p+"sync.enabled", false)
	v.SetDefault(p+"sync.queue_name", "ingest")
	v.SetDefault(p+"sync.concurrency", 8)
	v.SetDefault(p+"sync.page_size", 100)
	v.SetDefault(p+"sync.poll_delay", "1s")
	v.SetDefault(p+"sync.status_delay", "15s")

	// Arcade listener defaults
	v.SetDefault(p+"arcade.enabled", false)
	v.SetDefault(p+"arcade.ingest", true)

	// Routes defaults
	v.SetDefault(p+"routes.enabled", true)
	v.SetDefault(p+"routes.prefix", "")
}

// Services holds initialized indexer services.
type Services struct {
	Indexer        *IngestCtx
	Sync           *IngestSync
	ArcadeListener *ArcadeListener
	StatusHandler  *StatusHandler
	Routes         *Routes

	config   *Config
	logger   *slog.Logger
	initDeps *InitializeDeps // stored for SetupStatusHandler
}

// InitializeDeps holds dependencies for indexer service initialization.
type InitializeDeps struct {
	Store       store.Store
	BeefStorage *beef.Storage
	OutputStore *txo.OutputStore
}

// ArcadeListenerDeps holds dependencies for arcade listener initialization.
// These are set separately since arcade is initialized after indexer.
type ArcadeListenerDeps struct {
	EventPublisher events.Publisher
	PubSub         pubsub.PubSub
}

// StatusHandlerDeps holds dependencies for status handler initialization.
type StatusHandlerDeps struct {
	PubSub       pubsub.PubSub
	ChainTracker chaintracker.ChainTracker
}

// Initialize creates indexer services from the configuration.
func (c *Config) Initialize(
	ctx context.Context,
	logger *slog.Logger,
	deps *InitializeDeps,
) (*Services, error) {
	if c.Mode == ModeDisabled {
		return nil, nil
	}

	if logger == nil {
		logger = slog.Default()
	}

	// Create logger with optional level override
	indexerLogger := logging.NewComponentLogger(logger, "indexer", c.LogLevel)

	svc := &Services{
		config:   c,
		logger:   indexerLogger,
		initDeps: deps,
	}

	// Create the IngestCtx with configured tags
	svc.Indexer = NewIngestCtx(deps.OutputStore, deps.BeefStorage, indexerLogger).
		WithTags(c.Tags). // nil tags = use parse.DefaultTags
		WithVerbose(c.Verbose)

	// Create sync worker if enabled
	if c.Sync.Enabled {
		svc.Sync = NewIngestSync(
			&c.Sync,
			deps.Store,
			svc.Indexer,
			indexerLogger,
		)
	}

	return svc, nil
}

// SetupArcadeListener initializes the arcade listener with its dependencies.
// This must be called after arcade is initialized since arcade depends on indexer.
func (s *Services) SetupArcadeListener(deps *ArcadeListenerDeps) {
	s.logger.Info("SetupArcadeListener called",
		"arcade.enabled", s.config.Arcade.Enabled,
		"eventPublisher", deps.EventPublisher != nil,
		"pubsub", deps.PubSub != nil)

	if !s.config.Arcade.Enabled || deps.EventPublisher == nil || deps.PubSub == nil {
		s.logger.Warn("ArcadeListener not initialized",
			"arcade.enabled", s.config.Arcade.Enabled,
			"hasEventPublisher", deps.EventPublisher != nil,
			"hasPubSub", deps.PubSub != nil)
		return
	}

	s.ArcadeListener = NewArcadeListener(
		deps.EventPublisher,
		deps.PubSub,
		s.logger,
	)
}

// SetupStatusHandler initializes the status handler with its dependencies.
// This subscribes to the "arc" pubsub topic and handles all transaction status updates.
func (s *Services) SetupStatusHandler(deps *StatusHandlerDeps) {
	if deps.PubSub == nil {
		return
	}

	s.StatusHandler = NewStatusHandler(
		deps.PubSub,
		s.initDeps.Store,
		s.initDeps.BeefStorage,
		s.initDeps.OutputStore,
		deps.ChainTracker,
		s.Indexer,
		&StatusHandlerConfig{IngestEnabled: s.config.Arcade.Ingest},
		s.logger,
	)
}

// SetupRoutes initializes the routes with pubsub for webhook callbacks.
func (s *Services) SetupRoutes(ps pubsub.PubSub) {
	if !s.config.Routes.Enabled || ps == nil {
		return
	}
	s.Routes = NewRoutes(ps)
}

// Start starts the JungleBus sync worker (if configured).
func (s *Services) Start(ctx context.Context) error {
	if s.Sync != nil {
		if err := s.Sync.Start(ctx); err != nil {
			return err
		}
	}
	return nil
}

// StartEventHandlers starts the arcade listener and status handler.
// These handle transaction events from arcade broadcasts.
func (s *Services) StartEventHandlers(ctx context.Context) error {
	if s.ArcadeListener != nil {
		if err := s.ArcadeListener.Start(ctx); err != nil {
			return err
		}
	}
	if s.StatusHandler != nil {
		if err := s.StatusHandler.Start(ctx); err != nil {
			return err
		}
	}
	return nil
}

// Close closes the indexer services.
func (s *Services) Close() error {
	if s.StatusHandler != nil {
		s.StatusHandler.Stop()
	}
	if s.ArcadeListener != nil {
		s.ArcadeListener.Stop()
	}
	if s.Sync != nil {
		s.Sync.Stop()
	}
	return nil
}

package indexer

import (
	"context"
	"log/slog"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/logging"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/txo"
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
	Routes RoutesConfig `mapstructure:"routes"`
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

	// Routes defaults
	v.SetDefault(p+"routes.enabled", true)
	v.SetDefault(p+"routes.prefix", "")
}

// Services holds initialized indexer services.
type Services struct {
	Indexer *IngestCtx
	Sync    *IngestSync
	// Routes  *Routes // TODO: Add if indexer routes are needed

	config *Config
	logger *slog.Logger
}

// InitializeDeps holds dependencies for indexer service initialization.
type InitializeDeps struct {
	Store       store.Store
	BeefStorage *beef.Storage
	OutputStore *txo.OutputStore
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
		config: c,
		logger: indexerLogger,
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

// Start starts background services (sync worker).
func (s *Services) Start(ctx context.Context) error {
	if s.Sync != nil {
		return s.Sync.Start(ctx)
	}
	return nil
}

// Close closes the indexer services.
func (s *Services) Close() error {
	if s.Sync != nil {
		s.Sync.Stop()
	}
	return nil
}

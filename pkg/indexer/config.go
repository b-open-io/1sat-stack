package indexer

import (
	"context"
	"log/slog"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/arcadeclient"
	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/logging"
	"github.com/b-open-io/1sat-stack/pkg/ordfs"
	"github.com/b-open-io/1sat-stack/pkg/pubsub"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/bsv-blockchain/go-chaintracks/chaintracks"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
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

// SyncConfig holds configuration for the ingest sync worker and JungleBus subscriptions.
type SyncConfig struct {
	Enabled         bool          `mapstructure:"enabled"`          // Enable the ingest sync worker
	SubscriptionIDs []string      `mapstructure:"subscription_ids"` // JungleBus subscription IDs (set via env var)
	QueueName       string        `mapstructure:"queue_name"`       // Queue to consume from (default: "ingest")
	FromBlock       uint64        `mapstructure:"from_block"`       // Starting block for JungleBus subscriptions
	Concurrency     int           `mapstructure:"concurrency"`      // Worker concurrency (default: 8)
	PageSize        uint32        `mapstructure:"page_size"`        // Batch fetch size (default: 100)
	PollDelay       time.Duration `mapstructure:"poll_delay"`       // Sleep when queue empty (default: 1s)
	StatusDelay     time.Duration `mapstructure:"status_delay"`     // Status log interval (default: 15s)
	BatchSize       int           `mapstructure:"batch_size"`       // JungleBus batch size (default: 500)
	ReorgDepth      uint32        `mapstructure:"reorg_depth"`      // JungleBus reorg depth (default: 6)
	EnableMempool   bool          `mapstructure:"enable_mempool"`   // Enable mempool subscription
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
	v.SetDefault(p+"sync.subscription_ids", []string{})
	v.SetDefault(p+"sync.queue_name", "ingest")
	v.SetDefault(p+"sync.from_block", 783968)
	v.SetDefault(p+"sync.concurrency", 8)
	v.SetDefault(p+"sync.page_size", 100)
	v.SetDefault(p+"sync.poll_delay", "1s")
	v.SetDefault(p+"sync.status_delay", "15s")
	v.SetDefault(p+"sync.batch_size", 500)
	v.SetDefault(p+"sync.reorg_depth", 6)
	v.SetDefault(p+"sync.enable_mempool", false)

	// Arcade listener defaults
	v.SetDefault(p+"arcade.enabled", true)
	v.SetDefault(p+"arcade.ingest", true)

	// Routes defaults
	v.SetDefault(p+"routes.enabled", true)
	v.SetDefault(p+"routes.prefix", "")
}

// Services holds initialized indexer services.
type Services struct {
	Indexer        *IngestCtx
	Sync           *IngestSync
	StatusHandler  *StatusHandler
	PendingAuditor *PendingAuditor

	config   *Config
	logger   *slog.Logger
	initDeps *InitializeDeps // stored for SetupStatusHandler
}

// InitializeDeps holds dependencies for indexer service initialization.
type InitializeDeps struct {
	Store       store.Store
	BeefStorage *beef.Storage
	OutputStore *txo.OutputStore
	Ordfs       *ordfs.Ordfs
}

// TopicIndexer looks up which overlay topics contain outputs for a given txid.
type TopicIndexer interface {
	Topics(ctx context.Context, txid *chainhash.Hash) ([]string, error)
}

// StatusHandlerDeps holds dependencies for status handler initialization.
type StatusHandlerDeps struct {
	PubSub         pubsub.PubSub
	ChainTracker   chaintracker.ChainTracker
	OverlayStorage engine.Storage                  // For BEEF update + rollback
	TopicIndex     TopicIndexer                    // txid → topic names
	LookupServices map[string]engine.LookupService // topic name → lookup service
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
		WithOrdfs(deps.Ordfs).
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
		deps.OverlayStorage,
		deps.TopicIndex,
		deps.LookupServices,
		deps.ChainTracker,
		s.Indexer,
		&StatusHandlerConfig{IngestEnabled: s.config.Arcade.Ingest},
		s.logger,
	)
}

// PendingAuditorDeps holds dependencies for pending auditor initialization.
type PendingAuditorDeps struct {
	Chaintracks  chaintracks.Chaintracks
	ArcadeClient *arcadeclient.Client // may be nil
}

// SetupPendingAuditor initializes the pending transaction auditor.
// This subscribes to chaintracks for new block events and audits tx:pending.
func (s *Services) SetupPendingAuditor(deps *PendingAuditorDeps) {
	if deps.Chaintracks == nil {
		s.logger.Warn("no chaintracks, pending auditor disabled")
		return
	}

	s.PendingAuditor = NewPendingAuditor(
		s.initDeps.OutputStore,
		s.initDeps.BeefStorage,
		s.Indexer,
		deps.Chaintracks,
		deps.ArcadeClient,
		s.logger,
	)
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

// StartEventHandlers starts the status handler and pending auditor.
func (s *Services) StartEventHandlers(ctx context.Context) error {
	if s.StatusHandler != nil {
		if err := s.StatusHandler.Start(ctx); err != nil {
			return err
		}
	}
	if s.PendingAuditor != nil {
		s.PendingAuditor.Start(ctx)
	}
	return nil
}

// Close closes the indexer services.
func (s *Services) Close() error {
	if s.PendingAuditor != nil {
		s.PendingAuditor.Stop()
	}
	if s.StatusHandler != nil {
		s.StatusHandler.Stop()
	}
	if s.Sync != nil {
		s.Sync.Stop()
	}
	return nil
}

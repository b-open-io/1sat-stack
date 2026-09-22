package collection

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/config"
	"github.com/b-open-io/1sat-stack/pkg/indexer"
	"github.com/b-open-io/1sat-stack/pkg/jbsync"
	"github.com/b-open-io/1sat-stack/pkg/logging"
	"github.com/b-open-io/1sat-stack/pkg/owner"
	"github.com/b-open-io/1sat-stack/pkg/parse"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/b-open-io/1sat-stack/pkg/worker"
	"github.com/b-open-io/go-junglebus"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	sdkoverlay "github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"golang.org/x/sync/errgroup"
)

// IngestQueue is the JungleBus feed queue. Members are 32-byte txids.
const IngestQueue = "collection"

// SyncConfig holds the collection ingest pipeline configuration.
type SyncConfig struct {
	Enabled           bool          `mapstructure:"enabled"`
	SubscriptionID    string        `mapstructure:"subscription_id"`
	FromBlock         uint64        `mapstructure:"from_block"`
	BatchSize         int           `mapstructure:"batch_size"`
	ReorgDepth        uint32        `mapstructure:"reorg_depth"`
	EnableMempool     bool          `mapstructure:"enable_mempool"`
	DispatchWorkers   int           `mapstructure:"dispatch_workers"`
	ItemWorkers       int           `mapstructure:"item_workers"`
	FeePerOutput      int64         `mapstructure:"fee_per_output"`
	IndexAll          bool          `mapstructure:"index_all"`
	LifecycleInterval time.Duration `mapstructure:"lifecycle_interval"`
	LogLevel          string        `mapstructure:"log_level"`
}

// SubscriberConfig creates a JungleBus subscriber config for the ingest queue.
func (c *SyncConfig) SubscriberConfig() *jbsync.SubscriberConfig {
	return &jbsync.SubscriberConfig{
		AutoStart:      true,
		SubscriptionID: c.SubscriptionID,
		QueueName:      IngestQueue,
		FromBlock:      c.FromBlock,
		BatchSize:      c.BatchSize,
		ReorgDepth:     c.ReorgDepth,
		EnableMempool:  c.EnableMempool,
	}
}

// SyncDeps are the process dependencies the ingest pipeline needs.
type SyncDeps struct {
	Store       store.Store
	ConfigStore config.Store
	Beef        *beef.Storage
	Outputs     *txo.OutputStore
	JungleBus   *junglebus.Client
}

// SyncServices runs the collection ingest pipeline.
type SyncServices struct {
	config      *SyncConfig
	store       store.Store
	beefStorage *beef.Storage
	overlay     *engine.Engine
	logger      *slog.Logger

	dispatcher *worker.Worker
	manager    *Manager
}

// NewSyncServices creates the dispatcher and item-worker manager.
// The discovery topic must already be registered on overlaySvc.
func NewSyncServices(
	cfg *SyncConfig,
	deps *SyncDeps,
	overlaySvc *engine.Engine,
	lookup *LookupService,
	logger *slog.Logger,
) (*SyncServices, error) {
	if overlaySvc == nil {
		return nil, fmt.Errorf("overlay engine is required for collection sync")
	}
	if deps == nil || deps.Store == nil || deps.Beef == nil {
		return nil, fmt.Errorf("collection sync requires a store and beef storage")
	}
	if cfg.DispatchWorkers == 0 {
		cfg.DispatchWorkers = 8
	}
	if cfg.ItemWorkers == 0 {
		cfg.ItemWorkers = 8
	}
	if cfg.FeePerOutput == 0 {
		cfg.FeePerOutput = FeePerOutput
	}
	if cfg.LifecycleInterval == 0 {
		cfg.LifecycleInterval = 5 * time.Minute
	}
	if logger == nil {
		logger = slog.Default()
	}
	syncLogger := logging.NewComponentLogger(logger, "collection-sync", cfg.LogLevel)

	var ownerSync OwnerSyncer
	if deps.JungleBus != nil && deps.Outputs != nil {
		idx := indexer.NewIngestCtx(deps.Outputs, deps.Beef, syncLogger)
		ownerSync = owner.NewOwnerSync(deps.JungleBus, deps.Beef, idx, deps.Outputs, deps.ConfigStore, syncLogger)
	}

	manager := NewManager(
		deps.Store,
		deps.ConfigStore,
		deps.Beef,
		deps.Outputs,
		overlaySvc,
		lookup,
		ownerSync,
		cfg.ItemWorkers,
		cfg.FeePerOutput,
		cfg.LifecycleInterval,
		cfg.IndexAll,
		syncLogger,
	)

	return &SyncServices{
		config:      cfg,
		store:       deps.Store,
		beefStorage: deps.Beef,
		overlay:     overlaySvc,
		logger:      syncLogger,
		manager:     manager,
	}, nil
}

// GetManager returns the item-worker manager for status queries.
func (s *SyncServices) GetManager() *Manager {
	if s == nil {
		return nil
	}
	return s.manager
}

// Start runs the dispatcher and the item-worker lifecycle until ctx is cancelled.
func (s *SyncServices) Start(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)

	s.dispatcher = worker.New(&worker.Config{
		Store:   s.store,
		Key:     jbsync.QueueKey(IngestQueue),
		Limiter: make(chan struct{}, s.config.DispatchWorkers),
		Handler: s.dispatch,
		OnError: func(ctx context.Context, id string, score float64, err error) {
			s.logger.Error("dispatcher error", "txid", id, "score", score, "error", err)
		},
		PageSize:  1000,
		PollDelay: time.Second,
		Logger:    s.logger,
	})

	g.Go(func() error {
		return s.dispatcher.Start(ctx)
	})
	g.Go(func() error {
		return s.manager.Start(ctx)
	})
	return g.Wait()
}

// dispatch reads one ingested transaction. Roots are submitted to the discovery
// topic immediately. Items are queued onto q:tm_col_{collectionId} and are not
// submitted here.
func (s *SyncServices) dispatch(ctx context.Context, member string, score float64) error {
	if len(member) != 32 {
		return fmt.Errorf("invalid txid length: expected 32, got %d", len(member))
	}
	var txid chainhash.Hash
	copy(txid[:], member)

	tx, err := s.beefStorage.LoadTx(ctx, &txid)
	if err != nil {
		return fmt.Errorf("failed to load transaction %s: %w", txid.String(), err)
	}

	var beefBytes []byte
	for vout, output := range tx.Outputs {
		if !IsCollectionMintOutput(output) {
			continue
		}
		fields := DecodeMapFields(output.LockingScript)
		outpoint := &transaction.Outpoint{Txid: txid, Index: uint32(vout)}
		discovery, collectionID := routeCollection(fields, outpoint)
		if discovery {
			if beefBytes == nil {
				beefBytes, err = s.beefStorage.BuildFullBeef(ctx, &txid)
				if err != nil {
					s.logger.Error("failed to build beef for collection root", "error", err, "txid", txid.String())
					continue
				}
			}
			if _, err := s.overlay.Submit(ctx, sdkoverlay.TaggedBEEF{
				Beef:   beefBytes,
				Topics: []string{DiscoveryTopic},
			}, engine.SubmitModeHistorical, nil); err != nil {
				s.logger.Error("failed to submit collection root", "error", err, "txid", txid.String())
			}
			continue
		}
		if collectionID == "" {
			continue
		}
		if err := s.store.ZAdd(ctx, txo.KeyQueue(ItemTopic(collectionID)), store.ScoredMember{
			Member: outpoint.Bytes(),
			Score:  score,
		}); err != nil {
			return fmt.Errorf("failed to enqueue collection item: %w", err)
		}
	}
	return nil
}

// routeCollection reports whether an output is a collection root, or the
// normalized collection id of an item. Other outputs return false, "".
func routeCollection(fields *MapFields, outpoint *transaction.Outpoint) (discovery bool, collectionID string) {
	if fields == nil {
		return false, ""
	}
	switch fields.SubType {
	case SubTypeCollection:
		return true, ""
	case SubTypeCollectionItem:
		return false, parse.NormalizeRelativeOutpoint(fields.CollectionID, outpoint)
	default:
		return false, ""
	}
}

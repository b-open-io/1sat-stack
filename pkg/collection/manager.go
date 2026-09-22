package collection

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/config"
	"github.com/b-open-io/1sat-stack/pkg/overlay"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/txo"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"golang.org/x/sync/errgroup"
)

const (
	whitelistPrefix   = "collection.whitelist:"
	blacklistPrefix   = "collection.blacklist:"
	outputCountPrefix = "collection.outputs:"
)

// OwnerSyncer pulls UTXOs for a fee address into the indexer.
type OwnerSyncer interface {
	Sync(ctx context.Context, owner string) error
}

// Manager starts and stops per-collection item workers.
// A worker runs only while the collection is whitelisted, funded, or index-all is on.
type Manager struct {
	store        store.Store
	configStore  config.Store
	beefStorage  *beef.Storage
	outputStore  *txo.OutputStore
	overlay      *engine.Engine
	lookup       *LookupService
	ownerSync    OwnerSyncer
	concurrency  int
	feePerOutput int64
	indexAll     bool
	interval     time.Duration
	logger       *slog.Logger

	workers  sync.Map // collectionId -> *itemWorker
	statuses sync.Map // collectionId -> *CollectionStatus
	limiter  chan struct{}
	g        *errgroup.Group
	ctx      context.Context
}

type itemWorker struct {
	cancel    context.CancelFunc
	status    *CollectionStatus
	startedAt time.Time
}

// WorkerStatus is one running item worker, for the admin list.
type WorkerStatus struct {
	CollectionID string            `json:"collection_id"`
	FeeAddress   string            `json:"fee_address"`
	QueueDepth   int64             `json:"queue_depth"`
	StartedAt    time.Time         `json:"started_at"`
	Status       *CollectionStatus `json:"status,omitempty"`
}

// NewManager creates an item-worker manager. It does not start workers.
func NewManager(
	s store.Store,
	cs config.Store,
	beefStorage *beef.Storage,
	outputStore *txo.OutputStore,
	overlaySvc *engine.Engine,
	lookup *LookupService,
	ownerSync OwnerSyncer,
	concurrency int,
	feePerOutput int64,
	interval time.Duration,
	indexAll bool,
	logger *slog.Logger,
) *Manager {
	if concurrency == 0 {
		concurrency = 8
	}
	if feePerOutput == 0 {
		feePerOutput = FeePerOutput
	}
	if interval == 0 {
		interval = 5 * time.Minute
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{
		store:        s,
		configStore:  cs,
		beefStorage:  beefStorage,
		outputStore:  outputStore,
		overlay:      overlaySvc,
		lookup:       lookup,
		ownerSync:    ownerSync,
		concurrency:  concurrency,
		feePerOutput: feePerOutput,
		indexAll:     indexAll,
		interval:     interval,
		logger:       logger.With("component", "collection-manager"),
		limiter:      make(chan struct{}, concurrency),
	}
}

// Start manages item workers until ctx is cancelled.
func (m *Manager) Start(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)
	m.g = g
	m.ctx = ctx

	m.manage(ctx)
	g.Go(func() error {
		ticker := time.NewTicker(m.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				m.manage(ctx)
			}
		}
	})
	g.Go(func() error {
		ticker := time.NewTicker(15 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				m.refreshInactive(ctx)
			}
		}
	})
	return g.Wait()
}

func (m *Manager) manage(ctx context.Context) {
	active := map[string]struct{}{}

	if m.configStore != nil {
		entries, err := m.configStore.List(ctx, whitelistPrefix)
		if err != nil {
			m.logger.Error("failed to load collection whitelist", "error", err)
		} else {
			for key := range entries {
				id := canonicalID(strings.TrimPrefix(key, whitelistPrefix))
				if id == "" {
					continue
				}
				active[id] = struct{}{}
				if _, exists := m.workers.Load(id); exists {
					continue
				}
				status, err := m.GetCollectionStatus(ctx, id)
				if err != nil {
					m.logger.Debug("whitelist status failed", "collectionId", id, "error", err)
					continue
				}
				if err := m.startWorker(ctx, status); err != nil {
					m.logger.Error("failed to start whitelisted collection", "collectionId", id, "error", err)
				}
			}
		}
	}

	if m.lookup != nil {
		cols, err := m.lookup.ListCollections(ctx, 0, false)
		if err != nil {
			m.logger.Error("failed to list collections", "error", err)
		} else {
			for _, col := range cols {
				id := canonicalID(col.CollectionID)
				if id == "" {
					continue
				}
				if _, exists := m.workers.Load(id); exists {
					active[id] = struct{}{}
					continue
				}
				status, err := m.GetCollectionStatus(ctx, id)
				if err != nil || !status.IsActive() {
					continue
				}
				if status.Name == "" {
					status.Name = col.Name
				}
				active[id] = struct{}{}
				if err := m.startWorker(ctx, status); err != nil {
					m.logger.Error("failed to start collection worker", "collectionId", id, "error", err)
				}
			}
		}
	}

	m.workers.Range(func(key, value any) bool {
		id := key.(string)
		if _, ok := active[id]; ok {
			return true
		}
		status, err := m.GetCollectionStatus(ctx, id)
		if err == nil && status.IsActive() {
			return true
		}
		value.(*itemWorker).cancel()
		if m.overlay != nil {
			m.overlay.UnregisterTopicManager(ItemTopic(id))
		}
		m.logger.Info("collection worker stopped", "collectionId", id)
		return true
	})
}

func (m *Manager) startWorker(ctx context.Context, status *CollectionStatus) error {
	if m.g == nil || m.ctx == nil {
		return errors.New("collection manager not started")
	}
	if _, exists := m.workers.Load(status.CollectionID); exists {
		return nil
	}
	if m.lookup != nil {
		count, err := m.lookup.Count(ctx, ItemTopic(status.CollectionID))
		if err != nil {
			return fmt.Errorf("count items: %w", err)
		}
		status.SetOutputCount(count)
		status.UpdateBalance(int64(status.Credits) - status.Debits())
		m.persistCount(ctx, status.CollectionID, count)
	}
	if !status.IsActive() {
		return nil
	}

	workerCtx, cancel := context.WithCancel(m.ctx)
	topic := ItemTopic(status.CollectionID)
	if m.overlay != nil {
		m.overlay.RegisterTopicManager(topic, NewItemTopicManager(status.CollectionID, m.logger))
	}
	syncWorker := overlay.NewOverlaySync(
		&overlay.OverlaySyncConfig{
			QueueName:           topic,
			Limiter:             m.limiter,
			ResolveDependencies: false,
			OnProcessed: func(string) error {
				m.onProcessed(status.CollectionID)
				return nil
			},
		},
		topic,
		m.store,
		m.beefStorage,
		m.overlay,
		m.logger.With("collectionId", status.CollectionID),
	)
	m.statuses.Store(status.CollectionID, status)
	m.workers.Store(status.CollectionID, &itemWorker{cancel: cancel, status: status, startedAt: time.Now()})
	m.g.Go(func() error {
		defer m.workers.Delete(status.CollectionID)
		defer m.statuses.Delete(status.CollectionID)
		err := syncWorker.Start(workerCtx)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil
		}
		return err
	})
	m.logger.Info("collection worker started", "collectionId", status.CollectionID, "topic", topic)
	return nil
}

func (m *Manager) onProcessed(id string) {
	value, ok := m.workers.Load(id)
	if !ok {
		return
	}
	status := value.(*itemWorker).status
	if status.IsWhitelisted || status.forced {
		return
	}
	if status.RecordOutput() > 0 {
		return
	}
	if !status.TryStartSync() {
		return
	}
	go func() {
		defer status.EndSync()
		ctx := context.Background()
		if m.ownerSync != nil && status.FeeAddress != "" {
			if err := m.ownerSync.Sync(ctx, status.FeeAddress); err != nil {
				m.logger.Debug("failed to sync fee address", "collectionId", id, "error", err)
			}
		}
		fresh, err := m.GetCollectionStatus(ctx, id)
		if err != nil {
			m.logger.Error("failed to refresh collection status", "collectionId", id, "error", err)
			return
		}
		status.Credits = fresh.Credits
		status.IsWhitelisted = fresh.IsWhitelisted
		status.IsBlacklisted = fresh.IsBlacklisted
		status.FeePerOutput = fresh.FeePerOutput
		status.SetOutputCount(fresh.OutputCount())
		status.UpdateBalance(fresh.Balance())
		if !status.IsActive() {
			if tw, ok := m.workers.Load(id); ok {
				tw.(*itemWorker).cancel()
				m.logger.Info("collection worker cancelled, insufficient funding", "collectionId", id)
			}
		}
	}()
}

// GetCollectionStatus computes the funding status for one collection.
// A running worker's count is used when one exists; otherwise the persisted
// count, then the item topic database.
func (m *Manager) GetCollectionStatus(ctx context.Context, id string) (*CollectionStatus, error) {
	op, err := parseOutpoint(id)
	if err != nil {
		return nil, fmt.Errorf("invalid collection id: %w", err)
	}
	id = op.OrdinalString()
	feeAddress, err := FeeAddress(op)
	if err != nil {
		return nil, err
	}
	status := &CollectionStatus{
		CollectionID: id,
		FeeAddress:   feeAddress,
		FeePerOutput: m.feePerOutput,
		forced:       m.indexAll,
	}
	if m.configStore != nil {
		if _, err := m.configStore.Get(ctx, whitelistPrefix+id); err == nil {
			status.IsWhitelisted = true
		}
		if _, err := m.configStore.Get(ctx, blacklistPrefix+id); err == nil {
			status.IsBlacklisted = true
		}
	}
	count, err := m.resolveOutputCount(ctx, id)
	if err != nil {
		return nil, err
	}
	// Listed collections do not pay per output. Report the count, not a balance.
	if status.IsWhitelisted || status.IsBlacklisted {
		status.FeePerOutput = 0
		status.SetOutputCount(count)
		status.UpdateBalance(0)
		return status, nil
	}
	if m.outputStore == nil {
		status.SetOutputCount(count)
		status.UpdateBalance(0)
		return status, nil
	}
	credits, _, err := m.outputStore.SearchBalance(ctx, &txo.OutputSearchCfg{
		SearchCfg:   store.SearchCfg{Keys: [][]byte{[]byte("own:" + feeAddress)}},
		FilterSpent: true,
	})
	if err != nil {
		return nil, fmt.Errorf("fee balance: %w", err)
	}
	status.Credits = credits
	status.SetOutputCount(count)
	status.UpdateBalance(int64(credits) - status.Debits())
	return status, nil
}

// resolveOutputCount prefers the live worker count, then the persisted value,
// and only then the item topic database.
func (m *Manager) resolveOutputCount(ctx context.Context, id string) (int64, error) {
	if v, ok := m.statuses.Load(id); ok {
		count := v.(*CollectionStatus).OutputCount()
		m.persistCount(ctx, id, count)
		return count, nil
	}
	if m.configStore != nil {
		if v, err := m.configStore.Get(ctx, outputCountPrefix+id); err == nil {
			if count, err := strconv.ParseInt(v, 10, 64); err == nil {
				return count, nil
			}
		}
	}
	if m.lookup == nil {
		return 0, nil
	}
	count, err := m.lookup.Count(ctx, ItemTopic(id))
	if err != nil {
		return 0, fmt.Errorf("count items: %w", err)
	}
	m.persistCount(ctx, id, count)
	return count, nil
}

// ListCollectionStatuses returns funding status. By default only collections
// with a running worker are included. includeAll recomputes status for every
// discovered collection.
func (m *Manager) ListCollectionStatuses(ctx context.Context, includeAll bool) []*CollectionStatus {
	if !includeAll {
		var statuses []*CollectionStatus
		m.statuses.Range(func(_, value any) bool {
			statuses = append(statuses, value.(*CollectionStatus))
			return true
		})
		return statuses
	}
	if m.lookup == nil {
		return nil
	}
	cols, err := m.lookup.ListCollections(ctx, 0, false)
	if err != nil {
		m.logger.Error("failed to list collections", "error", err)
		return nil
	}
	statuses := make([]*CollectionStatus, 0, len(cols))
	for _, col := range cols {
		id := canonicalID(col.CollectionID)
		if id == "" {
			continue
		}
		if val, ok := m.statuses.Load(id); ok {
			status := val.(*CollectionStatus)
			if status.Name == "" {
				status.Name = col.Name
			}
			statuses = append(statuses, status)
			continue
		}
		status, err := m.GetCollectionStatus(ctx, id)
		if err != nil {
			m.logger.Debug("failed to get collection status", "collectionId", id, "error", err)
			continue
		}
		status.Name = col.Name
		statuses = append(statuses, status)
	}
	return statuses
}

// ListWorkers returns the running item workers and their queue depth.
func (m *Manager) ListWorkers(ctx context.Context) []WorkerStatus {
	var workers []WorkerStatus
	m.workers.Range(func(key, value any) bool {
		id := key.(string)
		w, ok := value.(*itemWorker)
		if !ok {
			return true
		}
		depth, err := m.store.ZCard(ctx, txo.KeyQueue(ItemTopic(id)))
		if err != nil {
			m.logger.Warn("failed to get queue depth", "collectionId", id, "error", err)
			depth = 0
		}
		ws := WorkerStatus{
			CollectionID: id,
			FeeAddress:   w.status.FeeAddress,
			QueueDepth:   depth,
			StartedAt:    w.startedAt,
		}
		if val, ok := m.statuses.Load(id); ok {
			ws.Status = val.(*CollectionStatus)
		}
		workers = append(workers, ws)
		return true
	})
	return workers
}

func (m *Manager) persistCount(ctx context.Context, id string, count int64) {
	if m.configStore == nil {
		return
	}
	if err := m.configStore.Set(ctx, outputCountPrefix+id, strconv.FormatInt(count, 10)); err != nil {
		m.logger.Warn("failed to persist output count", "collectionId", id, "error", err)
	}
}

func (m *Manager) refreshInactive(ctx context.Context) {
	if m.lookup == nil || m.ownerSync == nil {
		return
	}
	cols, err := m.lookup.ListCollections(ctx, 0, false)
	if err != nil {
		m.logger.Error("failed to list collections for fee refresh", "error", err)
		return
	}
	for _, col := range cols {
		id := canonicalID(col.CollectionID)
		if id == "" {
			continue
		}
		if _, exists := m.workers.Load(id); exists {
			continue
		}
		op, err := parseOutpoint(id)
		if err != nil {
			continue
		}
		addr, err := FeeAddress(op)
		if err != nil {
			continue
		}
		if err := m.ownerSync.Sync(ctx, addr); err != nil {
			m.logger.Debug("failed to refresh fee address", "collectionId", id, "error", err)
		}
	}
}

func canonicalID(id string) string {
	op, err := parseOutpoint(id)
	if err != nil {
		return ""
	}
	return op.OrdinalString()
}

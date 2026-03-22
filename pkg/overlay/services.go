package overlay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/gasp"
	overlaystorage "github.com/b-open-io/1sat-stack/pkg/overlay/storage"
	"github.com/redis/go-redis/v9"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	gasplib "github.com/bsv-blockchain/go-overlay-services/pkg/core/gasp"
	"golang.org/x/sync/errgroup"
)

// RemoteConfig defines a peer for topic sync.
// Stored in DB as JSON array for per-topic configuration.
// Array order determines priority for GASP resolution (first = highest priority).
type RemoteConfig struct {
	// Identity
	Type   string `json:"type"`              // "beef", "http", "libp2p"
	URL    string `json:"url,omitempty"`     // for http peers
	PeerID string `json:"peer_id,omitempty"` // for libp2p peers

	// Inbound (receiving data from peer)
	SSESubscribe     bool   `json:"sse_subscribe,omitempty"`      // listen via SSE stream
	GASPSync         bool   `json:"gasp_sync,omitempty"`          // periodic full GASP sync
	GASPSyncInterval string `json:"gasp_sync_interval,omitempty"` // "15m", "1h", etc.

	// Outbound (sending data to peer)
	Broadcast bool `json:"broadcast,omitempty"` // push new admissions to peer
}

// Services holds shared overlay infrastructure. Each module creates its own engine via NewModuleEngine.
type Services struct {
	Redis  *redis.Client // For remote config and queue operations
	P2P    *P2PBus
	logger *slog.Logger

	// Shared infrastructure for modules
	ModuleDeps   *ModuleDeps
	factory      overlaystorage.Factory
	txTopicIndex overlaystorage.TxTopicIndexer

	// For remote creation
	beefStorage *beef.Storage

	// Topic registry - tracks active topics for dynamic topic management (BSV21)
	topics sync.Map // topicName -> *Topic
}

// TxTopicIndex returns the shared cross-topic txid→topics index.
func (s *Services) TxTopicIndex() overlaystorage.TxTopicIndexer {
	return s.txTopicIndex
}

// TopicDB returns the TopicStorage for a given topic name.
func (s *Services) TopicDB(topic string) (overlaystorage.TopicStorage, error) {
	if s.factory == nil {
		return nil, errors.New("overlay storage not initialized")
	}
	return s.factory(topic)
}

// TopicDBFactory returns the underlying per-topic storage factory.
func (s *Services) TopicDBFactory() overlaystorage.Factory {
	return s.factory
}

// NewStorageAdapter creates an EngineAdapter backed by the shared storage factory.
// Used by the status handler for BEEF updates and output lookups.
func (s *Services) NewStorageAdapter() *overlaystorage.EngineAdapter {
	if s.ModuleDeps == nil {
		return nil
	}
	return overlaystorage.NewEngineAdapter(s.factory, s.beefStorage, s.txTopicIndex)
}

// Close cleans up overlay services.
func (s *Services) Close() error {
	s.topics.Range(func(key, value any) bool {
		topicName := key.(string)
		s.DeactivateTopic(topicName)
		return true
	})
	if s.P2P != nil {
		if err := s.P2P.Close(); err != nil {
			s.logger.Error("failed to close overlay P2P bus", "error", err)
		}
	}
	return nil
}

// ActivateTopic activates a topic on a given engine, performing all necessary setup:
// 1. Registers TopicManager with engine
// 2. If Remotes configured: checks DB for override, creates and starts OverlaySync worker
// 3. Starts listeners
// 4. Tracks in topics registry
//
// Topics without Remotes are "registration-only" (e.g., discovery topics).
func (s *Services) ActivateTopic(ctx context.Context, eng *engine.Engine, topic *Topic) error {
	if topic == nil {
		return errors.New("topic is nil")
	}
	if topic.Name == "" {
		return errors.New("topic name is required")
	}
	if topic.Manager == nil {
		return errors.New("topic manager is required")
	}
	if eng == nil {
		return errors.New("engine is required")
	}

	// Check if already active
	if _, exists := s.topics.Load(topic.Name); exists {
		return fmt.Errorf("topic %s is already active", topic.Name)
	}

	// Register with engine
	eng.RegisterTopicManager(topic.Name, topic.Manager)

	// Create cancellable context for this topic
	topicCtx, cancel := context.WithCancel(ctx)
	topic.cancel = cancel

	// Only create worker if remotes are configured
	if len(topic.Remotes) > 0 {
		// Check for DB remote config override
		if configs, err := s.GetRemoteConfig(ctx, topic.Name); err == nil && len(configs) > 0 {
			dbRemotes := s.createRemotesFromConfig(topic.Name, configs)
			if len(dbRemotes) > 0 {
				topic.Remotes = dbRemotes
				s.logger.Debug("using DB remote config override", "topic", topic.Name, "remotes", len(dbRemotes))
			}

			sseListeners := s.createListenersFromConfig(topic.Name, configs)
			topic.Listeners = append(topic.Listeners, sseListeners...)
		}

		// Create and start OverlaySync worker
		topic.worker = NewOverlaySync(
			&OverlaySyncConfig{
				QueueName:           topic.Name,
				Concurrency:         8,
				ResolveDependencies: true,
				OnProcessed:         topic.OnProcessed,
			},
			topic.Name,
			s.Redis,
			s.beefStorage,
			eng,
			s.logger,
		)

		// Start worker in background
		g, gCtx := errgroup.WithContext(topicCtx)
		g.Go(func() error {
			return topic.worker.Start(gCtx)
		})

		// Start listeners
		for _, listener := range topic.Listeners {
			l := listener
			g.Go(func() error {
				return l.Start(gCtx)
			})
		}
	}

	// Subscribe to P2P topic if bus available
	if s.P2P != nil {
		topic.p2pUnsub = s.P2P.Subscribe(topicCtx, topic.Name)
	}

	// Track in registry
	topic.active.Store(true)
	s.topics.Store(topic.Name, topic)

	s.logger.Info("topic activated",
		"topic", topic.Name,
		"remotes", len(topic.Remotes),
		"listeners", len(topic.Listeners),
		"p2p", s.P2P != nil,
	)

	return nil
}

// DeactivateTopic deactivates a topic, stopping all components.
func (s *Services) DeactivateTopic(name string) error {
	value, exists := s.topics.Load(name)
	if !exists {
		return fmt.Errorf("topic %s is not active", name)
	}

	topic := value.(*Topic)

	if topic.p2pUnsub != nil {
		topic.p2pUnsub()
	}

	if topic.cancel != nil {
		topic.cancel()
	}

	if topic.worker != nil {
		topic.worker.Stop()
	}

	for _, listener := range topic.Listeners {
		listener.Stop()
	}

	topic.active.Store(false)
	s.topics.Delete(name)

	s.logger.Info("topic deactivated", "topic", name)

	return nil
}

// GetTopic returns an active topic by name, or nil if not found.
func (s *Services) GetTopic(name string) *Topic {
	if value, exists := s.topics.Load(name); exists {
		return value.(*Topic)
	}
	return nil
}

// ListActiveTopics returns all active topics.
func (s *Services) ListActiveTopics() []*Topic {
	var topics []*Topic
	s.topics.Range(func(key, value any) bool {
		topics = append(topics, value.(*Topic))
		return true
	})
	return topics
}

// Remote config DB key prefix
const remoteConfigKeyPrefix = "topic:remotes:"

// createRemotesFromConfig converts RemoteConfig slice to actual gasp.Remote instances.
func (s *Services) createRemotesFromConfig(topicName string, configs []RemoteConfig) []gasplib.Remote {
	remotes := make([]gasplib.Remote, 0, len(configs))

	for _, cfg := range configs {
		remote := s.createRemoteFromConfig(topicName, cfg)
		if remote != nil {
			remotes = append(remotes, remote)
		}
	}

	return remotes
}

func (s *Services) createRemoteFromConfig(topicName string, cfg RemoteConfig) gasplib.Remote {
	switch cfg.Type {
	case "beef":
		if s.beefStorage == nil {
			s.logger.Warn("beef remote configured but beefStorage not available", "topic", topicName)
			return nil
		}
		s.logger.Debug("creating beef remote", "topic", topicName)
		return gasp.NewBeefRemote(s.beefStorage, s.Redis, "")

	case "http":
		if cfg.URL == "" {
			s.logger.Warn("http remote configured without URL", "topic", topicName)
			return nil
		}
		s.logger.Debug("creating http remote", "topic", topicName, "url", cfg.URL)
		return engine.NewOverlayGASPRemote(cfg.URL, topicName, http.DefaultClient, 8)

	case "libp2p":
		s.logger.Warn("libp2p remote type not yet implemented", "topic", topicName, "peerId", cfg.PeerID)
		return nil

	default:
		s.logger.Warn("unknown remote type", "type", cfg.Type, "topic", topicName)
		return nil
	}
}

func (s *Services) createListenersFromConfig(topicName string, configs []RemoteConfig) []Listener {
	var listeners []Listener

	for _, cfg := range configs {
		if cfg.SSESubscribe && cfg.URL != "" {
			listener := gasp.NewSSEListener(&gasp.SSEListenerConfig{
				PeerURL:   cfg.URL,
				TopicName: topicName,
				QueueKey:  []byte("q:" + topicName),
				Redis:     s.Redis,
				Logger:    s.logger,
			})
			listeners = append(listeners, listener)
			s.logger.Debug("created SSE listener", "topic", topicName, "peer", cfg.URL)
		}
	}

	return listeners
}

// SaveRemoteConfig saves remote configuration for a topic to the database.
func (s *Services) SaveRemoteConfig(ctx context.Context, topicName string, configs []RemoteConfig) error {
	if s.Redis == nil {
		return errors.New("redis not configured")
	}

	data, err := json.Marshal(configs)
	if err != nil {
		return fmt.Errorf("failed to marshal remote config: %w", err)
	}

	return s.Redis.Set(ctx, remoteConfigKeyPrefix+topicName, data, 0).Err()
}

// DeleteRemoteConfig removes remote configuration for a topic from the database.
func (s *Services) DeleteRemoteConfig(ctx context.Context, topicName string) error {
	if s.Redis == nil {
		return errors.New("redis not configured")
	}

	return s.Redis.Del(ctx, remoteConfigKeyPrefix+topicName).Err()
}

// GetRemoteConfig retrieves the remote configuration for a topic.
func (s *Services) GetRemoteConfig(ctx context.Context, topicName string) ([]RemoteConfig, error) {
	if s.Redis == nil {
		return nil, errors.New("redis not configured")
	}

	data, err := s.Redis.Get(ctx, remoteConfigKeyPrefix+topicName).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}

	var configs []RemoteConfig
	if err := json.Unmarshal(data, &configs); err != nil {
		return nil, fmt.Errorf("failed to parse remote config: %w", err)
	}

	return configs, nil
}

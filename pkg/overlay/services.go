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
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	gasplib "github.com/bsv-blockchain/go-overlay-services/pkg/core/gasp"
	"github.com/bsv-blockchain/go-sdk/overlay"
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

// TopicManagerFactory creates a TopicManager instance for a given topic name
// Deprecated: Use ActivateTopic with a Topic struct instead.
type TopicManagerFactory func(topicName string) (engine.TopicManager, error)

// Services holds initialized overlay services and acts as the central Topic registry.
type Services struct {
	Engine *engine.Engine
	Routes *Routes
	Store  store.Store // For DB remote config lookup
	logger *slog.Logger

	// For remote creation
	beefStorage *beef.Storage

	// Topic registry - tracks ALL active topics
	topics sync.Map // topicName -> *Topic

	// Legacy fields (to be removed after migration)
	mu             sync.RWMutex
	topicFactories map[string]TopicManagerFactory // topic name -> factory
	topicWhitelist map[string]struct{}            // config-based whitelist
	topicBlacklist map[string]struct{}            // config-based blacklist
}

// RegisterTopic registers a topic factory for static topics (tm_1sat, tm_bsv21).
// The factory will be called when ActivateConfiguredTopics is called.
func (s *Services) RegisterTopic(topicName string, factory TopicManagerFactory) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.topicFactories == nil {
		s.topicFactories = make(map[string]TopicManagerFactory)
	}
	s.topicFactories[topicName] = factory
	s.logger.Debug("topic factory registered", "name", topicName)
}

// SetTopicWhitelist sets the config-based topic whitelist
func (s *Services) SetTopicWhitelist(topics []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.topicWhitelist = make(map[string]struct{}, len(topics))
	for _, t := range topics {
		s.topicWhitelist[t] = struct{}{}
	}
}

// SetTopicBlacklist sets the config-based topic blacklist
func (s *Services) SetTopicBlacklist(topics []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.topicBlacklist = make(map[string]struct{}, len(topics))
	for _, t := range topics {
		s.topicBlacklist[t] = struct{}{}
	}
}

// IsTopicBlacklisted checks if a topic is blacklisted
func (s *Services) IsTopicBlacklisted(topicName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, blacklisted := s.topicBlacklist[topicName]
	return blacklisted
}

// ActivateConfiguredTopics activates all topics that are in the whitelist and have registered factories
func (s *Services) ActivateConfiguredTopics() {
	s.mu.RLock()
	factories := make(map[string]TopicManagerFactory, len(s.topicFactories))
	for k, v := range s.topicFactories {
		factories[k] = v
	}
	whitelist := make(map[string]struct{}, len(s.topicWhitelist))
	for k := range s.topicWhitelist {
		whitelist[k] = struct{}{}
	}
	blacklist := make(map[string]struct{}, len(s.topicBlacklist))
	for k := range s.topicBlacklist {
		blacklist[k] = struct{}{}
	}
	s.mu.RUnlock()

	s.logger.Debug("ActivateConfiguredTopics called",
		"factories", len(factories),
		"whitelist", len(whitelist))

	for topicName := range whitelist {
		// Skip blacklisted
		if _, blocked := blacklist[topicName]; blocked {
			s.logger.Debug("topic blacklisted, skipping", "topic", topicName)
			continue
		}

		factory, hasFactory := factories[topicName]
		if !hasFactory {
			s.logger.Warn("whitelisted topic has no registered factory", "topic", topicName)
			continue
		}
		manager, err := factory(topicName)
		if err != nil {
			s.logger.Error("failed to create topic manager", "topic", topicName, "error", err)
			continue
		}
		s.Engine.RegisterTopicManager(topicName, manager)
		s.logger.Info("topic activated from config", "topic", topicName)
	}
}

// RegisterLookupService registers a lookup service with the overlay engine
func (s *Services) RegisterLookupService(name string, service engine.LookupService) {
	if s.Engine != nil {
		s.Engine.RegisterLookupService(name, service)
		s.logger.Info("registered lookup service", "name", name)
	}
}

// UnregisterLookupService removes a lookup service from the overlay engine
func (s *Services) UnregisterLookupService(name string) {
	if s.Engine != nil {
		s.Engine.UnregisterLookupService(name)
		s.logger.Info("unregistered lookup service", "name", name)
	}
}

// GetEngine returns the overlay engine for direct access
func (s *Services) GetEngine() *engine.Engine {
	return s.Engine
}

// Submit submits a tagged BEEF to the overlay engine
func (s *Services) Submit(ctx context.Context, beef overlay.TaggedBEEF, mode engine.SumbitMode) (overlay.Steak, error) {
	if s.Engine == nil {
		return nil, errors.New("overlay engine not initialized")
	}
	return s.Engine.Submit(ctx, beef, mode, nil)
}

// GetTopics returns list of active topic names from the engine
func (s *Services) GetTopics() []string {
	if s.Engine == nil {
		return nil
	}
	managers := s.Engine.ListTopicManagers()
	topics := make([]string, 0, len(managers))
	for name := range managers {
		topics = append(topics, name)
	}
	return topics
}

// GetLookupServices returns list of active lookup service names from the engine
func (s *Services) GetLookupServices() []string {
	if s.Engine == nil {
		return nil
	}
	providers := s.Engine.ListLookupServiceProviders()
	services := make([]string, 0, len(providers))
	for name := range providers {
		services = append(services, name)
	}
	return services
}

// Close cleans up overlay services
func (s *Services) Close() error {
	// Deactivate all topics
	s.topics.Range(func(key, value any) bool {
		topicName := key.(string)
		s.DeactivateTopic(topicName)
		return true
	})
	return nil
}

// ActivateTopic activates a topic, performing all necessary setup:
// 1. Registers TopicManager with engine
// 2. If Remotes configured: checks DB for override, creates and starts TopicWorker
// 3. Starts listeners
// 4. Tracks in topics registry
//
// Topics without Remotes are "registration-only" (e.g., discovery topics).
func (s *Services) ActivateTopic(ctx context.Context, topic *Topic) error {
	if topic == nil {
		return errors.New("topic is nil")
	}
	if topic.Name == "" {
		return errors.New("topic name is required")
	}
	if topic.Manager == nil {
		return errors.New("topic manager is required")
	}

	// Check if already active
	if _, exists := s.topics.Load(topic.Name); exists {
		return fmt.Errorf("topic %s is already active", topic.Name)
	}

	// Register with engine
	s.Engine.RegisterTopicManager(topic.Name, topic.Manager)

	// Create cancellable context for this topic
	topicCtx, cancel := context.WithCancel(ctx)
	topic.cancel = cancel

	// Only create worker if remotes are configured
	if len(topic.Remotes) > 0 {
		// Check for DB remote config override
		if configs, err := s.GetRemoteConfig(ctx, topic.Name); err == nil && len(configs) > 0 {
			// Override remotes from DB config
			dbRemotes := s.createRemotesFromConfig(topic.Name, configs)
			if len(dbRemotes) > 0 {
				topic.Remotes = dbRemotes
				s.logger.Debug("using DB remote config override", "topic", topic.Name, "remotes", len(dbRemotes))
			}

			// Create SSE listeners from DB config
			sseListeners := s.createListenersFromConfig(topic.Name, configs)
			topic.Listeners = append(topic.Listeners, sseListeners...)
		}

		// Create and start TopicWorker
		topic.worker = gasp.NewTopicWorker(&gasp.TopicWorkerConfig{
			TopicName:   topic.Name,
			Store:       s.Store,
			Engine:      s.Engine,
			Remotes:     topic.Remotes,
			Concurrency: 8, // TODO: make configurable
			OnProcessed: topic.OnProcessed,
			Logger:      s.logger,
		})

		// Start worker in background
		g, gCtx := errgroup.WithContext(topicCtx)
		g.Go(func() error {
			return topic.worker.Start(gCtx)
		})

		// Start listeners
		for _, listener := range topic.Listeners {
			l := listener // capture for goroutine
			g.Go(func() error {
				return l.Start(gCtx)
			})
		}
	}

	// Track in registry
	topic.active.Store(true)
	s.topics.Store(topic.Name, topic)

	s.logger.Info("topic activated",
		"topic", topic.Name,
		"remotes", len(topic.Remotes),
		"listeners", len(topic.Listeners),
	)

	return nil
}

// DeactivateTopic deactivates a topic, stopping all components:
// 1. Stops listeners
// 2. Stops worker
// 3. Unregisters from engine
// 4. Removes from topics registry
func (s *Services) DeactivateTopic(name string) error {
	value, exists := s.topics.Load(name)
	if !exists {
		return fmt.Errorf("topic %s is not active", name)
	}

	topic := value.(*Topic)

	// Cancel context (stops worker and listeners)
	if topic.cancel != nil {
		topic.cancel()
	}

	// Stop worker explicitly
	if topic.worker != nil {
		topic.worker.Stop()
	}

	// Stop listeners explicitly
	for _, listener := range topic.Listeners {
		listener.Stop()
	}

	// Unregister from engine
	s.Engine.UnregisterTopicManager(name)

	// Remove from registry
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
// Array order is preserved (determines priority).
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

// createRemoteFromConfig creates a single gasp.Remote from RemoteConfig.
func (s *Services) createRemoteFromConfig(topicName string, cfg RemoteConfig) gasplib.Remote {
	switch cfg.Type {
	case "beef":
		if s.beefStorage == nil {
			s.logger.Warn("beef remote configured but beefStorage not available", "topic", topicName)
			return nil
		}
		s.logger.Debug("creating beef remote", "topic", topicName)
		return gasp.NewBeefRemote(s.beefStorage, s.Store, "")

	case "http":
		if cfg.URL == "" {
			s.logger.Warn("http remote configured without URL", "topic", topicName)
			return nil
		}
		s.logger.Debug("creating http remote", "topic", topicName, "url", cfg.URL)
		return engine.NewOverlayGASPRemote(cfg.URL, topicName, http.DefaultClient, 8)

	case "libp2p":
		// LibP2P remotes not yet implemented
		s.logger.Warn("libp2p remote type not yet implemented", "topic", topicName, "peerId", cfg.PeerID)
		return nil

	default:
		s.logger.Warn("unknown remote type", "type", cfg.Type, "topic", topicName)
		return nil
	}
}

// createListenersFromConfig creates Listener instances from RemoteConfigs that have SSESubscribe enabled.
func (s *Services) createListenersFromConfig(topicName string, configs []RemoteConfig) []Listener {
	var listeners []Listener

	for _, cfg := range configs {
		if cfg.SSESubscribe && cfg.URL != "" {
			listener := gasp.NewSSEListener(&gasp.SSEListenerConfig{
				PeerURL:   cfg.URL,
				TopicName: topicName,
				QueueKey:  []byte("q:" + topicName),
				Store:     s.Store,
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
	if s.Store == nil {
		return errors.New("store not configured")
	}

	data, err := json.Marshal(configs)
	if err != nil {
		return fmt.Errorf("failed to marshal remote config: %w", err)
	}

	key := []byte(remoteConfigKeyPrefix + topicName)
	return s.Store.Set(ctx, key, data)
}

// DeleteRemoteConfig removes remote configuration for a topic from the database.
func (s *Services) DeleteRemoteConfig(ctx context.Context, topicName string) error {
	if s.Store == nil {
		return errors.New("store not configured")
	}

	key := []byte(remoteConfigKeyPrefix + topicName)
	return s.Store.Del(ctx, key)
}

// GetRemoteConfig retrieves the remote configuration for a topic.
func (s *Services) GetRemoteConfig(ctx context.Context, topicName string) ([]RemoteConfig, error) {
	if s.Store == nil {
		return nil, errors.New("store not configured")
	}

	key := []byte(remoteConfigKeyPrefix + topicName)
	data, err := s.Store.Get(ctx, key)
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

package overlay

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/overlay"
)

// TopicManagerFactory creates a TopicManager instance for a given topic name
type TopicManagerFactory func(topicName string) (engine.TopicManager, error)

// Storage interface for reading active topics from database
type Storage interface {
	// GetActiveTopics returns the set of currently active topic names.
	// This should apply whitelist/blacklist/active balance filtering.
	GetActiveTopics(ctx context.Context) map[string]struct{}
}

// Services holds initialized overlay services
type Services struct {
	Engine  *engine.Engine
	Routes  *Routes
	Storage Storage // For reading active topics from database
	logger  *slog.Logger

	mu             sync.RWMutex
	topicFactories map[string]TopicManagerFactory // topic name -> factory
	topicWhitelist map[string]struct{}            // config-based whitelist
	topicBlacklist map[string]struct{}            // config-based blacklist
	syncStarted    bool
	cancelSync     context.CancelFunc
}

// RegisterTopic registers a topic with its factory function.
// The factory will be called when the topic becomes active (per Storage.GetActiveTopics).
func (s *Services) RegisterTopic(topicName string, factory TopicManagerFactory) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.topicFactories == nil {
		s.topicFactories = make(map[string]TopicManagerFactory)
	}
	s.topicFactories[topicName] = factory
	s.logger.Debug("topic registered", "name", topicName)
}

// UnregisterTopic removes a topic registration.
// If the topic is currently active in the engine, it will be removed on next sync.
func (s *Services) UnregisterTopic(topicName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.topicFactories, topicName)
	s.logger.Debug("topic unregistered", "name", topicName)
}

// GetRegisteredTopics returns a copy of all registered topic names
func (s *Services) GetRegisteredTopics() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	topics := make([]string, 0, len(s.topicFactories))
	for name := range s.topicFactories {
		topics = append(topics, name)
	}
	return topics
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

// IsTopicActive checks if a topic should be active based on whitelist/blacklist
func (s *Services) IsTopicActive(topicName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// If blacklisted, always inactive
	if _, blacklisted := s.topicBlacklist[topicName]; blacklisted {
		return false
	}

	// If whitelist is empty, all non-blacklisted topics are active
	if len(s.topicWhitelist) == 0 {
		return true
	}

	// Otherwise, must be in whitelist
	_, whitelisted := s.topicWhitelist[topicName]
	return whitelisted
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
	s.mu.RUnlock()

	s.logger.Debug("ActivateConfiguredTopics called",
		"factories", len(factories),
		"whitelist", len(whitelist))

	for topicName := range whitelist {
		s.logger.Debug("checking whitelisted topic", "topic", topicName)
		if !s.IsTopicActive(topicName) {
			s.logger.Debug("topic not active", "topic", topicName)
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

// StartSync begins periodic synchronization with database state.
// Active topics (from Storage) that have registered factories will have
// TopicManagers created and added to the engine.
func (s *Services) StartSync(ctx context.Context) {
	s.mu.Lock()
	if s.syncStarted {
		s.mu.Unlock()
		return
	}
	s.syncStarted = true

	// Create cancellable context for the sync goroutine
	syncCtx, cancel := context.WithCancel(ctx)
	s.cancelSync = cancel
	s.mu.Unlock()

	// Initial sync
	s.syncTopics(syncCtx)

	// Periodic sync goroutine
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-syncCtx.Done():
				return
			case <-ticker.C:
				s.syncTopics(syncCtx)
			}
		}
	}()

	s.logger.Info("topic sync started", "interval", "30s")
}

// syncTopics reads active topics from database and registers/unregisters with engine.
// A topic is activated if:
//  1. It has a registered factory (via RegisterTopic)
//  2. It is in the active set from Storage.GetActiveTopics()
func (s *Services) syncTopics(ctx context.Context) {
	if s.Engine == nil || s.Storage == nil {
		return
	}

	// Get active topic names from database (whitelist + active - blacklist)
	activeSet := s.Storage.GetActiveTopics(ctx)
	if activeSet == nil {
		return
	}

	// Get current topics from engine
	currentTopics := s.Engine.ListTopicManagers()

	// Get registered factories
	s.mu.RLock()
	factories := make(map[string]TopicManagerFactory, len(s.topicFactories))
	for k, v := range s.topicFactories {
		factories[k] = v
	}
	s.mu.RUnlock()

	registered := 0
	for topic := range activeSet {
		// Only activate if we have a factory for this topic
		factory, hasFactory := factories[topic]
		if !hasFactory {
			continue
		}

		// Skip if already in engine
		if _, exists := currentTopics[topic]; exists {
			continue
		}

		// Create and register the TopicManager
		manager, err := factory(topic)
		if err != nil {
			s.logger.Error("failed to create topic manager", "name", topic, "error", err)
			continue
		}
		s.Engine.RegisterTopicManager(topic, manager)
		s.logger.Info("topic activated", "name", topic)
		registered++
	}

	// Unregister topics no longer active
	unregistered := 0
	for topic := range currentTopics {
		if _, active := activeSet[topic]; !active {
			s.Engine.UnregisterTopicManager(topic)
			s.logger.Info("topic deactivated", "name", topic)
			unregistered++
		}
	}

	if registered > 0 || unregistered > 0 {
		s.logger.Debug("topic sync completed",
			"registered", registered,
			"unregistered", unregistered,
			"active", len(activeSet))
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
	s.mu.Lock()
	if s.cancelSync != nil {
		s.cancelSync()
	}
	s.mu.Unlock()
	return nil
}

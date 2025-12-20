package overlay

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/overlay"
)

// TopicManagerFactory creates a TopicManager instance for a given topic name
type TopicManagerFactory func(topicName string) (engine.TopicManager, error)

// Services holds initialized overlay services
type Services struct {
	Engine *engine.Engine
	Routes *Routes
	logger *slog.Logger

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
	return nil
}

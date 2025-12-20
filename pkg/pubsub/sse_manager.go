package pubsub

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// SSEClient represents an individual SSE connection
type SSEClient struct {
	writer interface{}
	topics []string
}

// SSEManager manages SSE clients and their subscriptions
type SSEManager struct {
	pubsub             PubSub
	clients            sync.Map
	topicClients       map[string][]string
	topicMutex         sync.RWMutex
	events             atomic.Value
	subscriptionCtx    context.Context
	subscriptionCancel context.CancelFunc
	ctx                context.Context
	cancel             context.CancelFunc
	logger             *slog.Logger
}

// NewSSEManager creates a new SSE manager
func NewSSEManager(ctx context.Context, pubsub PubSub, logger *slog.Logger) *SSEManager {
	managerCtx, cancel := context.WithCancel(ctx)

	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("component", "sse-manager")

	manager := &SSEManager{
		pubsub:       pubsub,
		topicClients: make(map[string][]string),
		ctx:          managerCtx,
		cancel:       cancel,
		logger:       logger,
	}

	go manager.broadcastLoop()

	return manager
}

// RegisterClient registers an SSE client for multiple topics
func (s *SSEManager) RegisterClient(topics []string, writer interface{}) string {
	clientID := fmt.Sprintf("sse_%d_%p", time.Now().UnixNano(), writer)

	client := &SSEClient{
		writer: writer,
		topics: topics,
	}

	s.clients.Store(clientID, client)

	for _, topic := range topics {
		s.addClientToTopic(topic, clientID)
	}

	s.logger.Debug("registered client", "clientID", clientID, "topics", topics)

	s.updateSubscriptions()

	return clientID
}

// DeregisterClient removes an SSE client
func (s *SSEManager) DeregisterClient(clientID string) error {
	clientVal, exists := s.clients.Load(clientID)
	if !exists {
		return nil
	}

	client := clientVal.(*SSEClient)

	for _, topic := range client.topics {
		s.removeClientFromTopic(topic, clientID)
	}

	s.clients.Delete(clientID)

	s.logger.Debug("deregistered client", "clientID", clientID)

	s.updateSubscriptions()

	return nil
}

func (s *SSEManager) addClientToTopic(topic, clientID string) {
	s.topicMutex.Lock()
	defer s.topicMutex.Unlock()

	if s.topicClients[topic] == nil {
		s.topicClients[topic] = []string{}
	}
	s.topicClients[topic] = append(s.topicClients[topic], clientID)
}

func (s *SSEManager) removeClientFromTopic(topic, clientID string) {
	s.topicMutex.Lock()
	defer s.topicMutex.Unlock()

	clients := s.topicClients[topic]
	for i, id := range clients {
		if id == clientID {
			s.topicClients[topic] = append(clients[:i], clients[i+1:]...)
			break
		}
	}

	if len(s.topicClients[topic]) == 0 {
		delete(s.topicClients, topic)
	}
}

func (s *SSEManager) updateSubscriptions() {
	if s.ctx == nil {
		return
	}

	s.topicMutex.RLock()
	var topics []string
	for topic, clientIDs := range s.topicClients {
		if len(clientIDs) > 0 {
			topics = append(topics, topic)
		}
	}
	s.topicMutex.RUnlock()

	s.logger.Debug("updating subscriptions", "topics", topics)

	if len(topics) > 0 {
		if s.subscriptionCancel != nil {
			s.logger.Debug("cancelling old subscription")
			s.subscriptionCancel()
		}

		s.subscriptionCtx, s.subscriptionCancel = context.WithCancel(s.ctx)

		if events, err := s.pubsub.Subscribe(s.subscriptionCtx, topics); err != nil {
			s.logger.Error("failed to update subscriptions", "error", err)
		} else {
			s.logger.Debug("subscribed to topics", "count", len(topics))
			s.events.Store(events)
		}
	}
}

func (s *SSEManager) broadcastLoop() {
	for {
		eventsVal := s.events.Load()
		if eventsVal == nil {
			select {
			case <-s.ctx.Done():
				return
			case <-time.After(100 * time.Millisecond):
				continue
			}
		}

		events := eventsVal.(<-chan Event)
		select {
		case event, ok := <-events:
			if !ok {
				continue
			}
			s.broadcastToClients(event)
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *SSEManager) broadcastToClients(event Event) {
	s.topicMutex.RLock()
	clientIDs, exists := s.topicClients[event.Topic]
	if !exists {
		s.topicMutex.RUnlock()
		return
	}

	clientIDsCopy := make([]string, len(clientIDs))
	copy(clientIDsCopy, clientIDs)
	s.topicMutex.RUnlock()

	var disconnectedClients []string

	for _, clientID := range clientIDsCopy {
		if clientVal, exists := s.clients.Load(clientID); exists {
			client := clientVal.(*SSEClient)
			if writer, ok := client.writer.(interface {
				Write([]byte) (int, error)
				Flush() error
			}); ok {
				var data string
				if event.Score > 0 {
					data = fmt.Sprintf("event: %s\ndata: %s\nid: %.0f\n\n", event.Topic, event.Member, event.Score)
				} else {
					data = fmt.Sprintf("event: %s\ndata: %s\n\n", event.Topic, event.Member)
				}
				if _, err := writer.Write([]byte(data)); err == nil {
					if flushErr := writer.Flush(); flushErr != nil {
						s.logger.Debug("failed to flush to client", "clientID", clientID, "error", flushErr)
						disconnectedClients = append(disconnectedClients, clientID)
					}
				} else {
					s.logger.Debug("failed to write to client", "clientID", clientID, "error", err)
					disconnectedClients = append(disconnectedClients, clientID)
				}
			}
		}
	}

	for _, clientID := range disconnectedClients {
		s.DeregisterClient(clientID)
	}
}

func (s *SSEManager) Stop() error {
	if s.cancel != nil {
		s.cancel()
	}
	return nil
}

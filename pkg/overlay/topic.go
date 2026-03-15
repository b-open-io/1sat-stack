package overlay

import (
	"context"
	"sync/atomic"

	"github.com/b-open-io/1sat-stack/pkg/gasp"
	"github.com/b-open-io/1sat-stack/pkg/store"
	gasplib "github.com/bsv-blockchain/go-overlay-services/pkg/core/gasp"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
)

// Listener is the interface for external data sources that feed the topic queue.
// Implementations include SSE listeners, LibP2P listeners, etc.
type Listener interface {
	Start(ctx context.Context) error
	Stop()
}

// Topic represents a first-class topic in the overlay system.
// A topic encapsulates:
// - Registration with the overlay engine (TopicManager)
// - Queue processing (TopicWorker)
// - External data sources (Listeners)
//
// Activation is atomic: when a topic is activated, all components start together.
// Deactivation stops all components and unregisters from the engine.
type Topic struct {
	Name        string               // Topic name (e.g., "tm_abc123...")
	Manager     engine.TopicManager  // Handles admission logic for the overlay engine
	Remotes     []gasplib.Remote     // Remote chain for GASP dependency resolution
	OnProcessed func(string) error   // Called after each successful item (optional)
	Listeners   []Listener           // External data sources (optional)

	// Runtime state (internal)
	worker   *gasp.TopicWorker
	cancel   context.CancelFunc
	p2pUnsub context.CancelFunc
	active   atomic.Bool
}

// IsActive returns true if the topic is currently activated.
func (t *Topic) IsActive() bool {
	return t.active.Load()
}

// QueueKey returns the queue key for this topic.
func (t *Topic) QueueKey() string {
	return "q:" + t.Name
}

// QueueDepth returns the number of items in the topic's queue.
func (t *Topic) QueueDepth(ctx context.Context, s store.Store) (int64, error) {
	return s.ZCard(ctx, []byte(t.QueueKey()))
}

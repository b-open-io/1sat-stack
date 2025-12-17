# Plan: Dynamic Topic Registration in Overlay Package

## Overview

Add dynamic topic registration to `pkg/ovr` so that modules can register their own topics at runtime. The overlay engine's `Managers` map becomes the source of truth for which topics are active.

## Key Concepts

### What is a Topic?

A **topic** is a named TopicManager instance. Each topic:
- Has a unique name (e.g., `"tm_abc123_0"`)
- Has a TopicManager that implements admission logic
- May have an associated LookupService (registered separately)

### How Topics Become Active

**Activation is driven by database state, not manual calls.**

1. A factory function is registered with `RegisterTopicFactory()`
2. `StartSync()` begins periodic synchronization (every 30 seconds)
3. `syncTopics()` calls `Storage.GetActiveTopics()` to get active topic names
4. For each active topic not in `engine.Managers`, the factory is called
5. Topics no longer in the active set are removed from `engine.Managers`

Storage determines which topics are active based on:
- Topics with data (e.g., balance > 0)
- Whitelisted topics (always active)
- Blacklisted topics (never active)

`engine.Managers` is the single source of truth. `IsTopicActive()` checks it directly.

### TopicManager Interface

From go-overlay-services, a TopicManager must implement:

```go
type TopicManager interface {
    IdentifyAdmissibleOutputs(ctx context.Context, beefBytes []byte, previousCoins []uint32) (overlay.AdmittanceInstructions, error)
    IdentifyNeededInputs(ctx context.Context, beefBytes []byte) ([]*transaction.Outpoint, error)
    GetDocumentation() string
    GetMetaData() *overlay.MetaData
}
```

Each TopicManager handles **one specific topic**. The topic name identifies what the manager is responsible for admitting.

## Current State

**`pkg/ovr`** has:
- `config.go` - Config and Initialize
- `services.go` - Services struct with Engine, basic registration methods
- `routes.go` - Route registration wrapping go-overlay-services

## Target State

The overlay Services owns topic lifecycle:
1. Module calls `ovr.RegisterTopicFactory(factory)` to set the factory function
2. `ovr.StartSync(ctx)` begins periodic sync (every 30s)
3. `syncTopics()` reads active topics from storage, updates `engine.Managers`
4. `ovr.IsTopicActive(name)` checks `engine.Managers` directly
5. Lookup services use `RegisterLookupService`/`UnregisterLookupService` with direct instances
6. `engine.Managers` is the single source of truth for active topics

## Implementation Plan

### Step 1: Add Topic Registration API to `pkg/ovr/services.go`

Extend `pkg/ovr/services.go` with factory registry and database-driven sync.

**Note:** This assumes go-overlay-services engine has been updated with thread-safe methods (see `go-overlay-services/docs/THREAD_SAFE_MANAGERS.md`).

```go
// TopicManagerFactory creates a TopicManager instance for a given topic name
type TopicManagerFactory func(topicName string) (engine.TopicManager, error)

// Services holds initialized overlay services
type Services struct {
    Engine  *engine.Engine
    Routes  *Routes
    Storage Storage // For reading active topics from database
    logger  *slog.Logger

    mu           sync.Mutex
    topicFactory TopicManagerFactory // Single factory for creating topic managers
    syncStarted  bool
}

// RegisterTopicFactory sets the factory function for creating topic managers
func (s *Services) RegisterTopicFactory(factory TopicManagerFactory) {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.topicFactory = factory
    s.logger.Info("topic manager factory registered")
}

// StartSync begins periodic synchronization with database state
func (s *Services) StartSync(ctx context.Context) {
    s.mu.Lock()
    if s.syncStarted {
        s.mu.Unlock()
        return
    }
    s.syncStarted = true
    s.mu.Unlock()

    // Initial sync
    s.syncTopics(ctx)

    // Periodic sync goroutine
    go func() {
        ticker := time.NewTicker(30 * time.Second)
        defer ticker.Stop()
        for {
            select {
            case <-ctx.Done():
                return
            case <-ticker.C:
                s.syncTopics(ctx)
            }
        }
    }()
}

// syncTopics reads active topics from database and registers/unregisters with engine
func (s *Services) syncTopics(ctx context.Context) {
    if s.Engine == nil || s.Storage == nil {
        return
    }

    // Get active topic names from database
    activeSet := s.Storage.GetActiveTopics(ctx) // Returns map[string]struct{}

    // Get current topics from engine (thread-safe)
    currentTopics := s.Engine.ListTopicManagers()

    // Register newly active topics
    s.mu.Lock()
    factory := s.topicFactory
    s.mu.Unlock()

    for topic := range activeSet {
        if _, exists := currentTopics[topic]; !exists {
            if factory != nil {
                if manager, err := factory(topic); err == nil {
                    s.Engine.RegisterTopicManager(topic, manager) // Thread-safe
                    s.logger.Info("topic registered", "name", topic)
                } else {
                    s.logger.Error("failed to create topic manager", "name", topic, "error", err)
                }
            }
        }
    }

    // Unregister topics no longer active
    for topic := range currentTopics {
        if _, active := activeSet[topic]; !active {
            s.Engine.UnregisterTopicManager(topic) // Thread-safe
            s.logger.Info("topic unregistered", "name", topic)
        }
    }
}

// IsTopicActive checks if a topic is registered with the engine
func (s *Services) IsTopicActive(name string) bool {
    if s.Engine == nil {
        return false
    }
    return s.Engine.HasTopicManager(name) // Thread-safe
}

// GetActiveTopicsFunc returns a function for checking topic availability
func (s *Services) GetActiveTopicsFunc() func(string) bool {
    return s.IsTopicActive
}

// --- Lookup Service Methods ---

// RegisterLookupService adds a lookup service to the engine
func (s *Services) RegisterLookupService(name string, lookup engine.LookupService) {
    if s.Engine == nil {
        return
    }
    s.Engine.RegisterLookupService(name, lookup) // Thread-safe
    s.logger.Info("lookup service registered", "name", name)
}

// UnregisterLookupService removes a lookup service from the engine
func (s *Services) UnregisterLookupService(name string) {
    if s.Engine == nil {
        return
    }
    s.Engine.UnregisterLookupService(name) // Thread-safe
    s.logger.Info("lookup service unregistered", "name", name)
}
```

### Step 2: Module Integration Pattern

Modules provide a factory function; Services handles the rest:

1. **Register a factory** - single function that creates managers for any topic
2. **Start sync** - periodic sync reads database, updates engine.Managers
3. **Routes check `IsTopicActive`** - checks engine.Managers directly

Example setup:
```go
// 1. Register factory that creates managers for any topic
overlay.RegisterTopicFactory(func(topicName string) (engine.TopicManager, error) {
    // Extract tokenId from topic name (e.g., "tm_abc123_0" -> "abc123_0")
    tokenId := strings.TrimPrefix(topicName, "tm_")
    return bsv21.NewTopicManager(topicName, storage, tokenId), nil
})

// 2. Start periodic sync (reads from database every 30s)
overlay.StartSync(ctx)

// 3. Routes check engine.Managers via IsTopicActive
if !overlay.IsTopicActive(topicName) {
    return c.Status(fiber.StatusServiceUnavailable).JSON(...)
}
```

**How topics become active:**
- Data is indexed and written to storage (e.g., token mint)
- Storage tracks active topics (e.g., `bsv21:active` sorted set)
- `syncTopics()` reads from storage, calls factory for new topics
- `engine.Managers` is the source of truth for what's active

Lookup services are simpler - register the instance directly:
```go
lookup := bsv21.NewLookup(storage)
overlay.RegisterLookupService("bsv21", lookup)
```

### Step 3: Wire Into Server Config

In `cmd/server/config.go`:
- Pass overlay services to modules during initialization
- Modules use overlay for topic registration/checking

```go
// Initialize module with overlay reference
moduleSvc, err := module.Initialize(ctx, logger, storage, overlay)
```

## Files to Modify

1. **`pkg/ovr/services.go`** - Add:
   - `TopicManagerFactory` function type
   - `Storage` field and interface for database access
   - `topicFactory` field, `syncStarted` flag
   - `RegisterTopicFactory`, `StartSync`, `syncTopics`
   - `IsTopicActive`, `GetActiveTopicsFunc`

2. **`pkg/ovr/storage.go`** (new or existing) - Storage interface with `GetActiveTopics(ctx) map[string]struct{}`

3. **`cmd/server/config.go`** - Wire storage into overlay, call `StartSync`

## Design Notes

1. **Depends on go-overlay-services thread-safety** - This plan assumes go-overlay-services has been updated with thread-safe `RegisterTopicManager()`, `UnregisterTopicManager()`, `HasTopicManager()` methods. See `go-overlay-services/docs/THREAD_SAFE_MANAGERS.md`.

2. **Database-Driven Activation** - Topics become active based on database state, not manual calls. `syncTopics()` periodically reads from storage and calls engine's register/unregister methods.

3. **Single Factory** - One factory function handles all topic creation. The factory receives the topic name and extracts any needed identifiers (e.g., tokenId from `"tm_<tokenId>"`).

4. **Engine is Source of Truth** - `IsTopicActive()` calls `engine.HasTopicManager()` directly. No separate cache in Services.

5. **Periodic Sync** - `syncTopics()` runs every 30 seconds. It registers managers for newly active topics and unregisters managers for deactivated topics.

6. **Topic Naming** - Topics use `"tm_<id>"` naming convention (e.g., `"tm_abc123_0"` for a token).

7. **Lookup Services Are Simpler** - Lookup services are registered directly via `engine.RegisterLookupService()`. They typically don't need dynamic activation.

8. **Storage Interface** - Storage must implement `GetActiveTopics(ctx) map[string]struct{}` which handles whitelist/blacklist/active balance logic internally.

9. **Minimal Locking in Services** - Services only needs a mutex for its own `topicFactory` field. All engine map access goes through engine's thread-safe methods.

## Reference: Original Implementation

The `ActiveTopics` pattern originates from:
- **`github.com/b-open-io/overlay/topics/active.go`** - ActiveTopics cache implementation
- **`github.com/b-open-io/bsv21-overlay/`** - BSV21 overlay using this pattern

Key database keys used:
- `bsv21:active` - Sorted set of tokenIds with balance > 0
- `bsv21:whitelist` - Set of explicitly whitelisted tokenIds
- `bsv21:blacklist` - Set of explicitly blacklisted tokenIds

The BSV21 module (`pkg/bsv21/topic.go`) demonstrates the TopicManager pattern:
- Creates TopicManager instances for specific tokens
- Uses `"tm_<tokenId>"` naming convention
- Routes check `IsTopicActive` before handling requests

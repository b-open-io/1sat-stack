# Config Cleanup Plan

This document tracks decisions and tasks for cleaning up the configuration system across all modules.

## Goals

1. **Consistent patterns** - All modules should follow the same initialization pattern
2. **Provider-based configs** - Replace URL parsing with explicit provider configurations
3. **Proper dependency injection** - Shared services passed in, not recreated
4. **Clear documentation** - Users understand what each config option does

---

## Implementation Status

### Completed

| Task | Status |
|------|--------|
| Network as simple string value | ✅ Done |
| JungleBus top-level config section | ✅ Done |
| Store provider-based config | ✅ Done |
| PubSub provider-based config | ✅ Done |
| BEEF provider-based config + JungleBus injection | ✅ Done |
| Update config.yaml with new structure | ✅ Done |

### Remaining

| Task | Priority |
|------|----------|
| TXO: Remove nested configs, single Initialize() with deps params | High |
| Remove routes.prefix from all modules | Medium |
| Overlay: Remove ARC fields, convert InitializeDeps to params | Medium |
| Owner: Convert InitializeDeps to params | Medium |
| Fees: Convert InitializeDeps to params | Medium |
| BSV21: Remove whitelist/blacklist (will be in DB), accept network via Initialize | Medium |
| Remove worker config (internal detail) | Low |

---

## Decision Log

### 1. Provider-Based Configuration (Store, PubSub, BEEF) ✅ DONE

**Problem:** Current URL-based configuration (`badger://`, `channels://`, `lru://?size=100mb`) is:
- Non-standard and hard to document
- Limited in expressiveness (complex options crammed into query params)
- Difficult to validate until parse time

**Decision:** Replace with explicit provider configs:

```yaml
# Store
store:
  mode: embedded
  provider: badger
  badger:
    path: ~/.1sat/store
    # in_memory: false

# PubSub
pubsub:
  mode: embedded
  provider: channels
  channels:
    buffer_size: 100

# BEEF
beef:
  mode: embedded
  chain:
    - provider: lru
      lru:
        size: 100mb
    - provider: filesystem
      filesystem:
        path: ~/.1sat/beef
    - provider: junglebus
```

### 2. JungleBus Config Section ✅ DONE

**Problem:** JungleBus was configured as just a URL under `network.junglebus`.

**Decision:** Create a proper `junglebus` config section at the top level with all client options.

```yaml
junglebus:
  url: https://junglebus.gorillapool.io
  token: ""        # Optional API token
  ssl: true        # Use SSL (default: true)
  version: v1      # API version (default: v1)
  debug: false     # Enable debug logging
```

The JungleBus client is initialized once at the system level and passed to modules that need it (like BEEF).

### 3. JungleBus Client Injection ✅ DONE

**Decision:** Pass the system `*junglebus.Client` into `beef.Config.Initialize()` as a parameter. If a `junglebus` provider is in the chain:
- Use the injected client
- Error if no client provided (don't silently create a new one)

### 4. Network as Simple Top-Level Value ✅ DONE

**Problem:** `network` was a struct with `type` and `junglebus` fields.

**Decision:** 
- `network` is now a simple string value (`main` or `test`)
- JungleBus has its own top-level config section
- External libraries (Arcade, P2P) have their network overridden from top-level before Initialize()
- Our own modules accept network via Initialize() params

```yaml
# Before
network:
  type: main
  junglebus: https://junglebus.gorillapool.io

# After
network: main

junglebus:
  url: https://junglebus.gorillapool.io
```

---

## Remaining Decisions (Not Yet Implemented)

### 5. TXO Dual Initialization Pattern

**Problem:** TXO has both `Initialize()` and `InitializeWithDeps()` methods, but:
- `Initialize()` is never called in the codebase
- It embeds full `store.Config`, `pubsub.Config`, `beef.Config` that are unused
- `SetDefaults()` pollutes viper with `txo.store.*`, `txo.pubsub.*`, `txo.beef.*` defaults

**Decision:** Keep `Initialize()` but fix it to match other modules' patterns:
- Remove nested config structs (`Store`, `PubSub`, `Beef` fields)
- Remove `InitializeWithDeps()` - just use `Initialize()` with deps as params

```go
// Before: Two methods, nested configs
type Config struct {
    Mode   string
    Store  store.Config   // Remove
    PubSub pubsub.Config  // Remove
    Beef   beef.Config    // Remove
    Routes RoutesConfig
}
func (c *Config) Initialize(ctx, logger) (*Services, error)
func (c *Config) InitializeWithDeps(ctx, store, pubsub, beef, logger) (*Services, error)

// After: Single method with deps
type Config struct {
    Mode   string
    Routes RoutesConfig
}
func (c *Config) Initialize(ctx, logger, store, pubsub, beef) (*Services, error)
```

### 6. Overlay ARC Config - Pass System Arcade Instance

**Problem:** Overlay routes config has ARC-related fields that don't belong there.

**Decision:** 
- Remove `arc_api_key` and `arc_callback_token` from `RoutesConfig`
- Pass the shared Arcade instance via `Initialize()` params

```go
// After
type RoutesConfig struct {
    Enabled          bool
    AdminBearerToken string
}

func (c *Config) Initialize(
    ctx context.Context,
    logger *slog.Logger,
    outputStore *txo.OutputStore,
    chainTracker chaintracker.ChainTracker,
    storage Storage,
    arcade *arcadeconfig.Services,
) (*Services, error)
```

### 7. Remove Routes Prefix from All Configs

**Problem:** Every module has a `routes.prefix` config field that's almost always empty. Real defaults are hardcoded in `RegisterRoutes()`.

**Decision:** Remove `Prefix` from all `RoutesConfig` structs. Keep only `Enabled`.

**Affected modules:**
- `pkg/txo`
- `pkg/pubsub`
- `pkg/beef`
- `pkg/bsv21`
- `pkg/overlay`
- `pkg/ordfs`
- `pkg/indexer`
- `pkg/merkle`
- `pkg/owner`

### 8. Remove Worker Config

**Problem:** `pkg/worker/config.go` defines worker pool settings that are internal implementation details.

**Decision:** Remove `pkg/worker/config.go`. Services that use workers should use sensible defaults internally and expose service-specific tuning if needed.

### 9. BSV21 Whitelist/Blacklist in Database

**Problem:** Token whitelist/blacklist are in static config, requiring restart to change.

**Decision:** Move whitelist/blacklist to database for runtime management. Remove these fields from config entirely.

```yaml
# After
bsv21:
  mode: embedded
  routes:
    enabled: true
  # network: passed via Initialize()
  # whitelist/blacklist: managed via API/database
```

### 10. Standardize Initialize() Signature - Deps as Params

**Problem:** Modules use inconsistent patterns for dependency injection.

**Decision:** Standardize on **deps as params**. Remove all `*InitializeDeps` structs.

**Affected modules:**
- `pkg/overlay` - has `InitializeDeps`
- `pkg/fees` - has `InitializeDeps`
- `pkg/owner` - has `InitializeDeps`

---

## Modules Status

| Module | Current State | Changes Needed | Status |
|--------|---------------|----------------|--------|
| `junglebus` | Was under `network.junglebus` | New top-level config section | ✅ Done |
| `store` | Was URL-based | Provider-based config | ✅ Done |
| `pubsub` | Was URL-based | Provider-based config | ✅ Done |
| `beef` | Was URL-based chain | Provider-based chain, JungleBus injection | ✅ Done |
| `txo` | Dual init, nested configs | Single init with deps params | ⏳ Pending |
| `indexer` | Has `network` field | Deprecated for parsers | ⏳ Skip |
| `bsv21` | Has network, whitelist/blacklist | Remove whitelist/blacklist, network via Initialize | ⏳ Pending |
| `overlay` | Has ARC config, InitializeDeps | Remove ARC fields, convert to params | ⏳ Pending |
| `ordfs` | Clean | None | ✅ Done |
| `owner` | Uses InitializeDeps | Convert to params | ⏳ Pending |
| `merkle` | Many params | Already uses params | ✅ Done |
| `fees` | Uses InitializeDeps | Convert to params | ⏳ Pending |
| `worker` | Has config | Remove entirely | ⏳ Pending |

---

## Open Questions

- Do we need `remote` mode for all modules, or just some?

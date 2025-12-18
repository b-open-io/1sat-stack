# Config Cleanup Plan

This document tracks decisions and tasks for cleaning up the configuration system across all modules.

## Goals

1. **Consistent patterns** - All modules should follow the same initialization pattern
2. **Provider-based configs** - Replace URL parsing with explicit provider configurations
3. **Proper dependency injection** - Shared services passed in, not recreated
4. **Clear documentation** - Users understand what each config option does

---

## Decision Log

### 1. Provider-Based Configuration (Store, PubSub, BEEF)

**Problem:** Current URL-based configuration (`badger://`, `channels://`, `lru://?size=100mb`) is:
- Non-standard and hard to document
- Limited in expressiveness (complex options crammed into query params)
- Difficult to validate until parse time

**Decision:** Replace with explicit provider configs:

```yaml
# Before (URL-based)
store:
  mode: embedded
  url: badger://~/.1sat/store

# After (provider-based)
store:
  provider: badger  # badger, tikv, redis, aerospike
  badger:
    path: ~/.1sat/store
    sync_writes: false
    gc_interval: 5m
  # Future providers:
  # tikv:
  #   endpoints: ["tikv1:2379", "tikv2:2379"]
  # redis:
  #   addr: localhost:6379
  #   db: 0
```

**Affected modules:**
- `pkg/store` - badger, (future: tikv, redis, aerospike)
- `pkg/pubsub` - channels, (future: redis)
- `pkg/beef` - lru, filesystem, junglebus

### 2. BEEF Chain Configuration

**Problem:** The BEEF `chain` array uses URL strings that get parsed into a JSON-like format internally.

**Decision:** Use explicit provider configs in the chain:

```yaml
# Before
beef:
  chain:
    - lru://?size=100mb
    - ~/.1sat/beef
    - junglebus://

# After
beef:
  chain:
    - provider: lru
      size: 100mb
    - provider: filesystem
      path: ~/.1sat/beef
    - provider: junglebus
      # Uses system junglebus client (no config needed here)
```

### 3. JungleBus Config Section

**Problem:** JungleBus client is configured as just a URL under `network.junglebus`, but the client supports more options:
- `url` - Server URL
- `token` - API token for authenticated requests
- `ssl` - Whether to use SSL
- `version` - API version
- `debug` - Enable debugging

**Decision:** Create a proper `junglebus` config section at the top level:

```yaml
# Before
network:
  type: main
  junglebus: https://junglebus.gorillapool.io

# After
network:
  type: main

junglebus:
  url: https://junglebus.gorillapool.io
  token: ""        # Optional API token
  ssl: true        # Use SSL (default: true)
  version: v1      # API version (default: v1)
  debug: false     # Enable debug logging
```

Create `pkg/junglebus/config.go` (or add to an appropriate location):

```go
type Config struct {
    URL     string `mapstructure:"url"`
    Token   string `mapstructure:"token"`
    SSL     bool   `mapstructure:"ssl"`
    Version string `mapstructure:"version"`
    Debug   bool   `mapstructure:"debug"`
}

func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
    v.SetDefault(prefix+".url", "https://junglebus.gorillapool.io")
    v.SetDefault(prefix+".token", "")
    v.SetDefault(prefix+".ssl", true)
    v.SetDefault(prefix+".version", "v1")
    v.SetDefault(prefix+".debug", false)
}

func (c *Config) Initialize() (*junglebus.Client, error) {
    opts := []junglebus.ClientOps{
        junglebus.WithHTTP(c.URL),
        junglebus.WithSSL(c.SSL),
        junglebus.WithVersion(c.Version),
        junglebus.WithDebugging(c.Debug),
    }
    if c.Token != "" {
        opts = append(opts, junglebus.WithToken(c.Token))
    }
    return junglebus.New(opts...)
}
```

### 3b. JungleBus Client Injection

**Problem:** BEEF currently creates its own JungleBus connection when it sees `junglebus://` in the chain, but a JungleBus client is already initialized at the system level.

**Decision:** Pass the system `*junglebus.Client` into `beef.Config.Initialize()` as a parameter. If a `junglebus` provider is in the chain:
- Use the injected client if provided
- Error if no client provided (don't silently create a new one)

### 4. Network as Simple Top-Level Value

**Problem:** 
- `network` is currently a struct with `type` and `junglebus` fields
- Multiple modules also define their own `network` field
- JungleBus has nothing to do with network selection

**Decision:** 
1. `network` should be a simple string value (`main` or `test`), not a struct
2. JungleBus gets its own top-level config section (see decision #3)
3. External libraries keep their internal `network` field - we override before `Initialize()`
4. Our own modules pass network via `Initialize()` params

```yaml
# Before
network:
  type: main
  junglebus: https://junglebus.gorillapool.io  # Doesn't belong here

# After
network: main  # Simple value

junglebus:     # Separate top-level section
  url: https://junglebus.gorillapool.io
  token: ""
```

```go
// Config struct change
type Config struct {
    Network string `mapstructure:"network"`  // Was NetworkConfig struct
    // ...
}

// External libraries: override before Initialize
c.Arcade.Network = c.Network
c.P2P.Network = c.Network

// Our modules: pass network to Initialize
func (c *Config) Initialize(ctx, logger, network types.Network, ...) (*Services, error)
```

**Clarification on module network config:**
- Modules CAN have their own `network` config for standalone use
- Modules MUST also accept network via `Initialize()` params
- When passed via `Initialize()`, it takes precedence over internal config
- Our server implementation always passes network, so internal config is ignored

**Affected modules:**
- `cmd/server/config.go` - change `NetworkConfig` struct to simple string
- `pkg/bsv21` - keep `network` in config, but also accept via Initialize(). Note: the `Indexer.Network` field appears unused - evaluate if it can be removed entirely
- `pkg/indexer` - keep `network` in config (if kept after parser migration)
- External libs (Arcade, P2P, Chaintracks) - keep struct as-is, override before Initialize()
- `pkg/txo` - does NOT use network (no changes needed)

### 6. Overlay ARC Config - Pass System Arcade Instance

**Problem:** Overlay routes config has ARC-related fields:
```go
type RoutesConfig struct {
    Enabled          bool
    Prefix           string
    AdminBearerToken string
    ARCAPIKey        string        // Shouldn't be here
    ARCCallbackToken string        // Shouldn't be here
}
```

Arcade is already initialized at the system level.

**Decision:** 
- Overlay can have internal arcade config for standalone use
- But we always pass in the system Arcade instance via `Initialize()`, which takes precedence
- Remove `arc_api_key` and `arc_callback_token` from `RoutesConfig` - those are Arcade config concerns, not overlay route concerns

```go
// Before
type RoutesConfig struct {
    Enabled          bool
    Prefix           string
    AdminBearerToken string
    ARCAPIKey        string         // Remove - belongs in arcade config
    ARCCallbackToken string         // Remove - belongs in arcade config
}

// After
type RoutesConfig struct {
    Enabled          bool
    AdminBearerToken string
}

// Initialize receives system arcade instance
func (c *Config) Initialize(
    ctx context.Context,
    logger *slog.Logger,
    outputStore *txo.OutputStore,
    chainTracker chaintracker.ChainTracker,
    storage Storage,
    arcade *arcadeconfig.Services,  // System arcade passed in
) (*Services, error)
```

### 8. Remove Routes Prefix from All Configs

**Problem:** Every module has a `routes.prefix` config field:
```go
type RoutesConfig struct {
    Enabled bool
    Prefix  string  // Almost always empty
}
```

But:
- Defaults are empty in `SetDefaults()`
- Real defaults are hardcoded in `cmd/server/config.go`'s `RegisterRoutes()`
- Swagger docs reflect the hardcoded paths
- Library consumers call `Routes.Register(group)` with their own path anyway

This is redundant complexity that serves no purpose.

**Decision:** Remove `Prefix` from all `RoutesConfig` structs. Keep only `Enabled`.

```go
// Before
type RoutesConfig struct {
    Enabled bool
    Prefix  string
}

// After
type RoutesConfig struct {
    Enabled bool
}
```

The server hardcodes paths in `RegisterRoutes()`. Library consumers pass their own path to `Register()`.

**Affected modules:**
- `pkg/txo`
- `pkg/pubsub`
- `pkg/beef`
- `pkg/bsv21`
- `pkg/overlay`
- `pkg/ordfs`
- `pkg/indexer`
- `pkg/merkle`
- `pkg/own`

### 9. Remove Worker Config

**Problem:** `pkg/worker/config.go` defines a `DefaultConfig` struct with worker pool settings (concurrency, page_size, poll_delay, etc.).

This is an internal implementation detail. Services that use workers (like BSV21 sync) should:
- Use sensible defaults internally
- Expose service-specific tuning options if needed (e.g., `bsv21.sync_concurrency`)

**Decision:** Remove `pkg/worker/config.go`. Worker configuration is not a user concern.

### 10. BSV21 Whitelist/Blacklist in Database

**Problem:** Token whitelist/blacklist are currently in static config:
```yaml
bsv21:
  whitelist_tokens: []
  blacklist_tokens: []
```

This requires a restart to change which tokens are indexed.

**Decision:** Move whitelist/blacklist to database for runtime management. Remove these fields from config entirely.

```yaml
# Before
bsv21:
  mode: embedded
  network: mainnet
  whitelist_tokens: ["token1", "token2"]
  blacklist_tokens: ["spamtoken"]
  routes:
    enabled: true
    prefix: /bsv21

# After
bsv21:
  mode: embedded
  routes:
    enabled: true
  # network: passed via Initialize()
  # whitelist/blacklist: managed via API/database
  # routes.prefix: removed (hardcoded in server)
```

### 5. TXO Dual Initialization Pattern

**Problem:** TXO has both `Initialize()` and `InitializeWithDeps()` methods, but:
- `Initialize()` is never called in the codebase
- It embeds full `store.Config`, `pubsub.Config`, `beef.Config` that are unused
- `SetDefaults()` pollutes viper with `txo.store.*`, `txo.pubsub.*`, `txo.beef.*` defaults

**Decision:** Keep `Initialize()` but fix it to match other modules' patterns:
- Remove nested config structs (`Store`, `PubSub`, `Beef` fields)
- Remove `InitializeWithDeps()` - just use `Initialize()` with deps as params
- Follow the same signature pattern as other modules (e.g., `indexer`, `bsv21`)

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

---

## Modules to Update

| Module | Current State | Changes Needed |
|--------|---------------|----------------|
| `junglebus` | Just URL under `network.junglebus` | New top-level config section with full options |
| `store` | URL-based (`badger://`) | Provider-based config |
| `pubsub` | URL-based (`channels://`) | Provider-based config |
| `beef` | URL-based chain | Provider-based chain, inject JungleBus client |
| `txo` | Dual init, nested configs | Single init with deps params |
| `indexer` | Has `network` field | Remove network, pass via Initialize (deprecated for parsers) |
| `bsv21` | Has `network` field, whitelist/blacklist in config | Keep network in config but also accept via Initialize(), remove whitelist/blacklist (will be in database). Note: Indexer.Network field may be unused - evaluate |
| `overlay` | Has ARC config in routes, uses `*InitializeDeps` | Remove ARC fields, convert to params |
| `ordfs` | Clean | None |
| `own` | Uses `*InitializeDeps` struct | Convert to params |
| `merkle` | Many params | Already uses params (good) |
| `fees` | Uses `*InitializeDeps` struct | Convert to params |

---

## Implementation Order

1. **Store** - Foundation, other modules depend on it
2. **PubSub** - Similar pattern to Store
3. **BEEF** - More complex (chain + JungleBus injection)
4. **TXO** - Remove nested configs, unify initialization

---

### 11. Standardize Initialize() Signature - Deps as Params

**Problem:** Modules use inconsistent patterns for dependency injection:
- Some use `Initialize(ctx, logger, dep1, dep2, dep3)` (params)
- Some use `Initialize(ctx, logger, *InitializeDeps)` (struct)

**Decision:** Standardize on **deps as params**. Remove all `*InitializeDeps` structs.

Reasons:
- Explicit - compiler checks arity
- Clear function signature shows exactly what's needed
- No hidden optional fields

```go
// Before (struct)
type InitializeDeps struct {
    OutputStore  *txo.OutputStore
    ChainTracker chaintracker.ChainTracker
    Storage      Storage
}
func (c *Config) Initialize(ctx, logger, deps *InitializeDeps) (*Services, error)

// After (params)
func (c *Config) Initialize(
    ctx context.Context,
    logger *slog.Logger,
    outputStore *txo.OutputStore,
    chainTracker chaintracker.ChainTracker,
    storage Storage,
) (*Services, error)
```

**Affected modules:**
- `pkg/overlay` - has `InitializeDeps`
- `pkg/fees` - has `InitializeDeps`
- `pkg/own` - has `InitializeDeps`

---

## Open Questions

- Do we need `remote` mode for all modules, or just some?

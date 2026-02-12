# Go Configuration Pattern Guide

Standard Viper-based configuration pattern for Go projects.

---

## Overview

| Context | Function | Purpose |
|---------|----------|---------|
| **Package** (library) | `SetDefaults(v *viper.Viper, prefix string)` | Register defaults, cascade to dependencies |
| **Package** (library) | `Initialize(ctx, logger, ...deps) (*Services, error)` | Create services from config |
| **Executable** (cmd/) | `Load() (*Config, error)` | Load from files, env vars, CLI flags |

---

## Package Pattern (Libraries)

Packages that can be embedded in other applications.

### config/config.go

```go
package config

import (
    "github.com/spf13/viper"
    depconfig "github.com/example/dependency/config"
)

type Config struct {
    // Package-specific fields
    StoragePath string `mapstructure:"storage_path"`
    LogLevel    string `mapstructure:"log_level"`

    // Embedded dependency configs (for cascading)
    Dependency depconfig.Config `mapstructure:"dependency"`
}

// SetDefaults registers defaults with optional prefix for embedding.
// Prefix enables namespacing when embedded: "arcade.storage_path" vs "storage_path"
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
    if prefix != "" {
        prefix = prefix + "."
    }

    // Package defaults
    v.SetDefault(prefix+"storage_path", "~/.myapp")
    v.SetDefault(prefix+"log_level", "info")

    // Cascade to embedded dependency configs
    c.Dependency.SetDefaults(v, prefix+"dependency")
}
```

### config/services.go

```go
package config

import (
    "context"
    "log/slog"
)

type Services struct {
    // Package services
    Store  *Store
    Client *Client

    // Dependency services (from cascaded init)
    DepServices *depconfig.Services

    Logger *slog.Logger
    Config *Config
}

// Initialize creates services from config.
// Accepts optional shared dependencies to avoid duplicate instances.
func (c *Config) Initialize(
    ctx context.Context,
    logger *slog.Logger,
    sharedDep SomeInterface, // nil = create own, non-nil = use shared
) (*Services, error) {
    s := &Services{
        Logger: logger,
        Config: c,
    }

    // Initialize or use shared dependency
    if sharedDep != nil {
        s.SharedDep = sharedDep
    } else {
        // Create own instance from embedded config
        depServices, err := c.Dependency.Initialize(ctx, logger)
        if err != nil {
            return nil, fmt.Errorf("dependency init: %w", err)
        }
        s.DepServices = depServices
        s.SharedDep = depServices.MainService
    }

    // Initialize package-specific services
    s.Store, err = NewStore(c.StoragePath)
    if err != nil {
        return nil, fmt.Errorf("store init: %w", err)
    }

    return s, nil
}

// Close gracefully shuts down services.
func (s *Services) Close() error {
    // Close in reverse initialization order
    if s.Store != nil {
        s.Store.Close()
    }
    if s.DepServices != nil {
        s.DepServices.Close()
    }
    return nil
}
```

---

## Executable Pattern (cmd/)

Applications that run standalone.

### cmd/server/main.go

```go
package main

import (
    "context"
    "log/slog"
    "os"

    "github.com/spf13/viper"
    "myapp/config"
)

func main() {
    cfg, err := Load()
    if err != nil {
        slog.Error("config load failed", "error", err)
        os.Exit(1)
    }

    ctx := context.Background()
    logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

    services, err := cfg.Initialize(ctx, logger, nil)
    if err != nil {
        slog.Error("init failed", "error", err)
        os.Exit(1)
    }
    defer services.Close()

    // Start server, etc.
}

// Load reads config from files, env vars, and CLI flags.
func Load() (*config.Config, error) {
    v := viper.New()

    // Set defaults (no prefix at top level)
    cfg := &config.Config{}
    cfg.SetDefaults(v, "")

    // Config file paths
    v.SetConfigName("config")
    v.SetConfigType("yaml")
    v.AddConfigPath(".")
    v.AddConfigPath("$HOME/.myapp")
    v.AddConfigPath("/etc/myapp")

    // Environment variables
    v.SetEnvPrefix("MYAPP")
    v.AutomaticEnv()
    v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

    // Legacy env var bindings (backward compatibility)
    v.BindEnv("storage_path", "STORAGE_PATH")
    v.BindEnv("dependency.url", "DEP_URL", "LEGACY_DEP_URL")

    // Read config file (ignore if not found)
    if err := v.ReadInConfig(); err != nil {
        if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
            return nil, fmt.Errorf("config file error: %w", err)
        }
    }

    // Unmarshal into struct
    if err := v.Unmarshal(cfg); err != nil {
        return nil, fmt.Errorf("config unmarshal: %w", err)
    }

    return cfg, nil
}
```

---

## Route Registration Pattern

For packages that expose HTTP routes.

### routes/fiber/routes.go

```go
package fiber

import (
    "github.com/gofiber/fiber/v2"
    "myapp/config"
)

type Routes struct {
    services *config.Services
}

func NewRoutes(services *config.Services) *Routes {
    return &Routes{services: services}
}

// Register adds routes to the provided router.
// Caller controls namespace: Register(app.Group("/myapp"))
func (r *Routes) Register(router fiber.Router) {
    router.Get("/health", r.healthHandler)
    router.Get("/status", r.statusHandler)
    router.Post("/action", r.actionHandler)
}
```

### Usage in embedding application

```go
// In 1sat-indexer or other host application
import myapproutes "github.com/example/myapp/routes/fiber"

func setupRoutes(app *fiber.App, services *config.Services) {
    // Mount at /myapp namespace
    myappRoutes := myapproutes.NewRoutes(services.MyAppServices)
    myappRoutes.Register(app.Group("/myapp"))

    // Or mount at root
    myappRoutes.Register(app)
}
```

---

## Config Cascading Example

When embedding multiple packages:

```yaml
# config.yaml for 1sat-indexer
network: main
storage_path: ~/.1sat

# Embedded arcade config (cascaded)
arcade:
  network: main
  storage_path: ~/.1sat/arcade

  # Arcade embeds chaintracks (double cascade)
  chaintracks:
    mode: embedded
    storage_path: ~/.1sat/chaintracks

# Embedded ordfs config
ordfs:
  redis_url: redis://localhost:6379
```

```go
// In 1sat-indexer config/config.go
type Config struct {
    Network     string `mapstructure:"network"`
    StoragePath string `mapstructure:"storage_path"`

    Arcade arcadeconfig.Config `mapstructure:"arcade"`
    ORDFS  ordfsconfig.Config  `mapstructure:"ordfs"`
}

func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
    if prefix != "" {
        prefix = prefix + "."
    }

    v.SetDefault(prefix+"network", "main")
    v.SetDefault(prefix+"storage_path", "~/.1sat")

    // Cascade to embedded configs
    c.Arcade.SetDefaults(v, prefix+"arcade")
    c.ORDFS.SetDefaults(v, prefix+"ordfs")
}
```

---

## Checklist

### Package (library)

- [ ] `config/config.go` with `Config` struct and `SetDefaults(v, prefix)`
- [ ] `config/services.go` with `Services` struct and `Initialize(ctx, logger, ...)`
- [ ] `Services.Close()` for graceful shutdown
- [ ] Embedded dependency configs cascade via `SetDefaults`
- [ ] `Initialize` accepts optional shared dependencies
- [ ] `routes/fiber/routes.go` with `Register(router)` if HTTP routes needed

### Executable (cmd/)

- [ ] `Load()` function that:
  - Creates Viper instance
  - Calls `SetDefaults(v, "")` (no prefix)
  - Sets config paths and env prefix
  - Binds legacy env vars
  - Reads config file
  - Unmarshals to Config struct
- [ ] Calls `Initialize()` with context and logger
- [ ] Handles graceful shutdown via `Services.Close()`

---

## Key Principles

1. **Packages never call `viper.New()`** - they receive `*viper.Viper` via `SetDefaults`
2. **Prefix enables embedding** - `SetDefaults(v, "arcade")` → keys become `arcade.storage_path`
3. **Shared dependencies prevent duplication** - pass existing Chaintracks to avoid creating two
4. **Routes are relative** - `Register(router)` works whether router is root or `app.Group("/ns")`
5. **Executables own the Viper instance** - they create it, configure paths/env, call `Load()`
6. **All defaults in SetDefaults()** - never set defaults in Initialize() or business logic
7. **Config files belong with their package** - `sync/config.go`, not `config/sync.go`

---

## Anti-Patterns

Common mistakes to avoid.

### 1. Fallback Defaults in Initialize()

**Wrong:** Setting defaults in Initialize() duplicates or bypasses SetDefaults.

```go
// BAD - default in Initialize()
func (c *Config) Initialize(ctx context.Context) (*Services, error) {
    url := c.StorageURL
    if url == "" {
        url = "badger:///tmp/queue"  // ❌ Should be in SetDefaults
    }
    // ...
}
```

**Right:** All defaults in SetDefaults(), Initialize() trusts config is populated.

```go
// GOOD - default in SetDefaults()
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
    v.SetDefault(prefix+"storage_url", "badger:///tmp/queue")  // ✓
}

func (c *Config) Initialize(ctx context.Context) (*Services, error) {
    // Trust that c.StorageURL has a value from SetDefaults or config file
    store, err := NewStore(c.StorageURL)
    // ...
}
```

### 2. Mode-Based Logic in Initialize()

**Wrong:** Deriving config values from other config values in Initialize().

```go
// BAD - mode inference in Initialize()
func (c *Config) Initialize(ctx context.Context) (*Services, error) {
    if c.URL != "" {
        c.Mode = "remote"  // ❌ Mutating config based on other fields
    }
    // ...
}
```

**Right:** Each field has its own default; users set both explicitly if needed.

```go
// GOOD - explicit defaults, no inference
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
    v.SetDefault(prefix+"mode", "embedded")
    v.SetDefault(prefix+"url", "")
}
```

### 3. SetDefault Calls Outside SetDefaults Method

**Wrong:** Calling `v.SetDefault()` in Load() for server-specific values.

```go
// BAD - SetDefault in Load()
func Load() (*AppConfig, error) {
    v := viper.New()
    v.SetDefault("server.port", 3000)  // ❌ Should be in SetDefaults
    cfg.Config.SetDefaults(v, "")
    // ...
}
```

**Right:** Server-specific config has its own SetDefaults that cascades.

```go
// GOOD - AppConfig has SetDefaults
type AppConfig struct {
    Server ServerConfig `mapstructure:"server"`
    config.Config `mapstructure:",squash"`
}

func (c *AppConfig) SetDefaults(v *viper.Viper, prefix string) {
    v.SetDefault(prefix+"server.port", 3000)  // ✓
    c.Config.SetDefaults(v, prefix)           // Cascade to library
}

func Load() (*AppConfig, error) {
    v := viper.New()
    cfg := &AppConfig{}
    cfg.SetDefaults(v, "")  // Single entry point for all defaults
    // ...
}
```

### 4. Hardcoded Values in Business Logic

**Wrong:** Magic numbers buried in service code.

```go
// BAD - hardcoded in business logic
func (ing *Ingester) Start() {
    limiter := make(chan struct{}, 20)           // ❌ Why 20?
    timeout := 2 * time.Minute                   // ❌ Why 2 minutes?
    immutableAfter := chaintip.Height - 10       // ❌ Why 10 blocks?
}
```

**Right:** Configurable values with documented defaults.

```go
// GOOD - in config with defaults
type IngestConfig struct {
    AuditConcurrency    int           `mapstructure:"audit_concurrency"`
    BroadcastTimeout    time.Duration `mapstructure:"broadcast_timeout"`
    ImmutabilityBlocks  int           `mapstructure:"immutability_blocks"`
}

func (c *IngestConfig) SetDefaults(v *viper.Viper, prefix string) {
    v.SetDefault(prefix+"audit_concurrency", 20)
    v.SetDefault(prefix+"broadcast_timeout", "2m")
    v.SetDefault(prefix+"immutability_blocks", 10)
}
```

### 5. Config Files in Wrong Package

**Wrong:** Config for a feature placed in central `config/` directory.

```
project/
├── config/
│   └── sync.go      ❌ Sync config in wrong package
└── sync/
    ├── libp2p.go
    └── sse.go
```

**Right:** Config lives with its package.

```
project/
├── config/
│   └── config.go    ✓ Root config only
└── sync/
    ├── config.go    ✓ Sync config with sync package
    ├── libp2p.go
    └── sse.go
```

### 6. Duplicate Defaults

**Wrong:** Same default defined in multiple places.

```go
// BAD - default in SetDefaults AND Initialize
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
    v.SetDefault(prefix+"url", "channels://")
}

func (c *Config) Initialize() (*Service, error) {
    url := c.URL
    if url == "" {
        url = "channels://"  // ❌ Duplicate! Which is authoritative?
    }
}
```

**Right:** Single source of truth in SetDefaults.

```go
// GOOD - SetDefaults is authoritative
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
    v.SetDefault(prefix+"url", "channels://")  // ✓ Only here
}

func (c *Config) Initialize() (*Service, error) {
    // Trust c.URL is populated - if empty, that's a config error
    if c.URL == "" {
        return nil, fmt.Errorf("url is required")
    }
}
```

### 7. Channel Buffers and Internal Constants

Some values are implementation details, not configuration. Use package constants, not config.

```go
// ACCEPTABLE - internal implementation detail as constant
const (
    channelBufferSize = 100  // Internal tuning, not user-facing config
)

func NewSubscriber() *Subscriber {
    return &Subscriber{
        events: make(chan Event, channelBufferSize),
    }
}
```

**Guideline:** If users would never reasonably need to change a value, it can be a constant. If operators might tune it for performance or behavior, it belongs in config.

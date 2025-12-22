# Per-Module Logging Pattern

This document describes how to add per-module log level configuration to any module in 1sat-stack.

## Overview

The logging system supports overriding log levels on a per-module basis. This allows you to enable debug logging for a specific module while keeping other modules at info level, which is useful for debugging without flooding logs.

## Implementation Steps

### 1. Add LogLevel to Config Struct

In your module's `config.go`, add a `LogLevel` field:

```go
type Config struct {
    Mode     string `mapstructure:"mode"`
    LogLevel string `mapstructure:"log_level"` // Log level (debug, info, warn, error)
    // ... other fields
}
```

### 2. Import the logging package

```go
import (
    "github.com/b-open-io/1sat-stack/pkg/logging"
    // ... other imports
)
```

### 3. Create Component Logger in Initialize()

In your `Initialize()` function, create a component-specific logger:

```go
func (c *Config) Initialize(ctx context.Context, logger *slog.Logger, deps *Deps) (*Services, error) {
    if logger == nil {
        logger = slog.Default()
    }

    // Create logger with optional level override
    moduleLogger := logging.NewComponentLogger(logger, "module-name", c.LogLevel)

    // Use moduleLogger for all services in this module
    svc := &Services{
        Sync: NewSyncService(deps, moduleLogger),
    }

    if c.Routes.Enabled {
        svc.Routes = NewRoutes(ctx, svc.Sync, moduleLogger)
    }

    return svc, nil
}
```

### 4. Update config.yaml

Add `log_level` to the module's configuration section:

```yaml
module_name:
  mode: embedded
  log_level: debug  # Per-module log level (debug, info, warn, error)
  routes:
    enabled: true
```

## How It Works

- `logging.NewComponentLogger(parent, component, levelOverride)` creates a new logger
- If `levelOverride` is empty, it inherits the parent's level but adds a `"component"` attribute
- If `levelOverride` is set (e.g., "debug"), it creates a new logger at that level
- Log output includes `"component": "module-name"` for easy filtering

## Example Log Output

```json
{"time":"2024-01-15T10:30:00Z","level":"DEBUG","msg":"OwnerSync starting","component":"owner","owner":"1ABC..."}
```

## Modules Currently Supporting Per-Module Logging

| Module | Config Key | Component Name |
|--------|------------|----------------|
| BSV21 Sync | `bsv21.sync.log_level` | `bsv21-sync` |
| Owner | `owner.log_level` | `owner` |

## Filtering Logs

When viewing JSON logs, you can filter by component:

```bash
# Using jq
./server 2>&1 | jq 'select(.component == "owner")'

# Using grep
./server 2>&1 | grep '"component":"owner"'
```

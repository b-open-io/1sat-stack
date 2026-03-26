# Per-Module Logging Pattern

This document describes the logging system in 1sat-stack: per-module component tagging, persistent SQLite storage, and admin UI log viewer.

## Overview

All logs are written to both stdout (JSON) and a persistent SQLite database (`{data_dir}/logs.db`) via a multi-handler. Every module is tagged with a `"component"` attribute for filtering. Logs are queryable via the admin UI or API with filters for component, level, time range, and text search.

Retention is 7 days by default. Logs are batched (100 records or 1 second, whichever comes first) and flushed on shutdown after all services close.

## Component Tags

Every module receives a tagged logger via `logging.NewComponentLogger()`. Tags are applied at the call site in `cmd/server/config.go`, except for modules that tag internally.

| Component | Tagged At | Config Key |
|-----------|-----------|------------|
| `store` | config.go call site | — |
| `pubsub` | config.go call site | — |
| `beef` | config.go call site | — |
| `arcade` | config.go call site | — |
| `txo` | config.go call site | — |
| `overlay` | config.go call site | — |
| `bsv21` | config.go call site | — |
| `bsv21-sync` | pkg/bsv21/sync.go | `bsv21.sync.log_level` |
| `bap` | config.go call site | — |
| `bsocial` | config.go call site | — |
| `opns` | config.go call site | — |
| `ordlock` | config.go call site | — |
| `spends` | config.go call site | — |
| `ordfs` | config.go call site | — |
| `indexer` | pkg/indexer/config.go | `indexer.log_level` |
| `owner` | pkg/owner/config.go | `owner.log_level` |
| `admin` | config.go call site | — |
| `sweep` | config.go call site | — |
| `landing` | config.go call site | — |
| `wallet` | pkg/wallet/service.go | — |
| `faucet` | config.go call site | — |
| `messagebox` | config.go call site | — |
| `paymail` | config.go call site | — |

## Adding Per-Module Log Level Override

To make a module's log level configurable:

### 1. Add LogLevel to the module's Config struct

```go
type Config struct {
    Mode     string `mapstructure:"mode"`
    LogLevel string `mapstructure:"log_level"`
}
```

### 2. Wrap the logger in Initialize()

```go
moduleLogger := logging.NewComponentLogger(logger, "module-name", c.LogLevel)
```

When `levelOverride` is non-empty, the logger filters at that level while preserving the parent's handler chain (stdout + SQLite).

### 3. Add to config.yaml

```yaml
module_name:
  log_level: debug
```

## Querying Logs

### Admin UI

The admin settings page has a **Logs** section with filters for component, level, and text search, plus pagination and auto-refresh.

### Admin API

```
GET /admin/api/logs?component=indexer&level=ERROR&limit=50
```

Query parameters: `component`, `level`, `since` (RFC3339), `until` (RFC3339), `search`, `limit` (1-1000), `offset`.

### SQLite Direct

The database is at `{data_dir}/logs.db`:

```sql
SELECT time_ns, level, component, msg, attrs FROM logs
WHERE component = 'indexer' AND level = 'ERROR'
ORDER BY time_ns DESC LIMIT 50;
```

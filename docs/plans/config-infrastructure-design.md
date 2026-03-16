# Configuration Infrastructure Design

Status: **Complete** (design finalized)

Linear: OPL-1185 (child of OPL-1183)

## Current State

The config system is Viper-based. A single `Config` struct holds all module configs. `LoadConfig()` reads from YAML files (`./config.yaml`, `~/.1sat/config.yaml`, `/etc/1sat/config.yaml`) and environment variables (`ONESAT_*` prefix). Everything is unmarshalled once at startup and frozen — no runtime reloading exists.

Each module follows: `SetDefaults()` → `Initialize(ctx, logger, deps)` → `Close()`.

The only already-dynamic pattern is overlay topic activation/deactivation in `pkg/overlay/services.go`.

## Proposed Architecture

### Two Configuration Layers

**Layer 1: Static config (pre-boot)**

Environment variables and/or config file. These are the same conceptual layer — different formats for the same purpose. Read once at startup, immutable at runtime.

What belongs here:
- Listen port, host, base path
- Data directories (`~/.1sat/store`, `~/.1sat/overlay`, etc.)
- Private key (server identity) — auto-generated if not provided
- Storage backend selections for always-on services (Badger vs Redis, SQLite vs Postgres)
- Network (mainnet/testnet)
- External service URLs (JungleBus URL, MongoDB URL)

These are deployment concerns. Changing them means restarting the process.

**Layer 2: Config store (runtime)**

A dedicated, lightweight store for application settings managed through the admin UI. Written by the setup wizard on first run, modifiable at runtime.

What belongs here:
- Auth mode (local vs authenticated)
- Admin identity key (first-admin wallet key in authenticated mode)
- Which overlays are enabled (BAP, OPNS, BSV21, BSocial, OrdLock)
- BSV21 topic scope (whitelist vs discovery, specific token list)
- JungleBus subscription IDs per overlay
- Paymail enabled/on
- Overlay remote peer configuration
- Tuning parameters (concurrency, batch sizes)

### Config Store Implementation

**Backend: SQLite**

SQLite is already a dependency (wallet, chaintracks, arcade all use it). A single `config.db` file in the data directory. Simple key-value schema:

```sql
CREATE TABLE config (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
```

Values stored as JSON strings for structured data. No ORM needed — raw `database/sql` with a thin wrapper.

**Interface:**

```go
type ConfigStore interface {
    Get(ctx context.Context, key string) (string, error)
    Set(ctx context.Context, key string, value string) error
    Delete(ctx context.Context, key string) error
    List(ctx context.Context, prefix string) (map[string]string, error)
    IsFirstRun(ctx context.Context) (bool, error)
}
```

**Location:** `~/.1sat/config.db` (or wherever the data dir points). Created automatically on first run.

**Why not Badger?** Badger is our data store backend. Using it for config would either mean a second Badger instance (heavier than needed) or mixing config into the data store (which we explicitly don't want). SQLite is simpler, has zero background goroutines, and the data is trivially inspectable.

### Config Precedence

At startup:

1. Module `SetDefaults()` establishes base defaults (unchanged from today)
2. Config file / env vars override defaults for static layer (unchanged from today)
3. Config store values override for runtime layer settings

The config store does NOT override static layer values. Port, data directories, private keys — those come from env/file only. The config store handles overlay toggles, auth mode, BSV21 topics, etc.

This means `LoadConfig()` still works as-is for the static layer. A new `ApplyRuntimeConfig(store ConfigStore)` step runs after Viper loads, merging runtime values into the Config struct before `Initialize()` runs.

### Config File: Keep or Drop?

Keep it, but reframe its role. The config file is a convenience for the static layer — an alternative to setting a dozen `ONESAT_*` env vars. For headless/automated deployments, operators may prefer a file. For container deployments, env vars.

The config file no longer holds overlay toggles, auth mode, or any runtime-configurable setting. Those move to the config store. This makes the config file much smaller — just infrastructure.

A minimal config file might look like:

```yaml
network: main
server:
  port: 8080
store:
  provider: badger
  badger:
    path: ~/.1sat/store
wallet:
  db: sqlite
  db_path: ~/.1sat/wallet.db
```

Everything else either defaults sensibly or lives in the config store.

### First-Run Flow

1. Server starts, `LoadConfig()` reads static layer (env/file/defaults)
2. Config store is opened (`~/.1sat/config.db`)
3. `IsFirstRun()` checks if config store is empty
4. If first run:
   - Always-on services initialize with defaults (Store, PubSub, Chaintracks, Arcade, Wallet, P2P, Beef, TXO, ORDFS, Indexer, MessageBox)
   - Admin UI serves the setup wizard (no overlays started yet)
   - Wizard writes choices to config store (auth mode, overlays, BSV21 topics, etc.)
   - Server reinitializes the overlay layer based on config store contents (or restarts — see below)
5. If not first run:
   - `ApplyRuntimeConfig()` merges config store values into the Config struct
   - Full initialization proceeds normally

### Runtime Reconfiguration: What's Feasible

Based on the module lifecycle analysis:

**Can be toggled at runtime (without restart):**
- Overlay enable/disable — topic activation/deactivation is already dynamic
- BSV21 topic whitelist — add/remove individual token topics
- JungleBus subscriptions — start/stop subscribers
- Overlay remote peer config — already has SaveRemoteConfig/GetRemoteConfig
- Tuning parameters (concurrency, batch sizes) — for next batch/cycle

**Requires restart:**
- Storage backends (Badger → Redis, SQLite → Postgres) — all consumers hold references
- Listen port, host, base path
- Private key
- Network (main/test)
- Auth mode change — middleware is wired at route registration time

**Needs investigation:**
- Auth mode — could potentially be made hot-swappable with middleware that checks config store per-request rather than being baked in at route registration. Worth exploring but not required for v1.

### Wizard → Overlay Init (Restart vs Hot-Init)

Two options after the wizard completes:

**Option A: Restart** — Wizard writes config store, tells user "restarting..." Server process restarts, reads config store, initializes everything normally. Simple, reliable, no new code paths.

**Option B: Hot-init** — After wizard writes config store, server initializes the overlay engine and selected overlays without restarting. This works because:
- Always-on services are already running (Store, TXO, Beef, etc.)
- Overlay topic activation is already dynamic
- We'd just need to call the overlay/BAP/OPNS/etc Initialize() functions mid-flight

Option A is simpler for v1. Option B is a nice-to-have that builds on the already-dynamic overlay activation pattern.

**Decision:** Option A for v1. Restart is fast (2-5 seconds) and the wizard is a one-time event. Hot-init can be added later if needed.

### Migration Path from Current Config

No automated migration. There are no production deployments. Dev environments will re-run the setup wizard manually.

The `ONESAT_*` env var prefix and config file locations continue to work for the static layer. Runtime settings (overlay modes, BSV21 topics, auth) move to the config store and are no longer read from Viper.

### Resolved Questions

1. ~~Wizard completion: restart or hot-init?~~ **Restart for v1.**
2. ~~Auth mode change: hot-swappable or restart?~~ **Restart.**
3. ~~Tuning params (concurrency, batch sizes): static or config store?~~ **Config store** — same amount of work, and it lets operators tune a running server.
4. ~~Migration from existing config files?~~ **No migration.** No production deployments exist. Dev environments will re-run the wizard manually.

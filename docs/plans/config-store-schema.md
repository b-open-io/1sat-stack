# Config Store Schema

Status: **In Progress**

## Design

The config store (SQLite) is the sole source of truth for all operational settings.
Only two values remain outside:

- `ONESAT_DATA_DIR` env var or `--data-dir` CLI flag (default `~/.1sat/`) — needed to locate the config store itself
- `ONESAT_PRIVATE_KEY` env var — server wallet private key (secret, never persisted to disk)

Everything else is managed through the setup wizard and admin settings UI.

## Key Format

Flat dotted keys with string values. Booleans stored as `"true"`/`"false"`, integers as decimal strings.

## Schema

### Server

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `server.port` | int | `8080` | Listen port |
| `server.host` | string | `0.0.0.0` | Listen host |
| `server.base_path` | string | `/1sat` | URL base path |
| `server.body_limit` | string | `100mb` | Max request body |

### Network

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `network` | string | `main` | BSV network (main/test) |

### Logging

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `logging.level` | string | `info` | Global log level |

### Auth

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `auth.mode` | string | `local` | `local` or `authenticated` |
| `auth.api_key` | string | `` | Admin API key |
| `auth.session_ttl` | string | `24h` | Session TTL |

### Setup

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `setup.complete` | bool | `false` | Whether initial setup has been completed |

### Store (Badger/Redis)

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `store.mode` | string | `embedded` | `embedded` or `disabled` |
| `store.provider` | string | `badger` | `badger` or `redis` |
| `store.badger.path` | string | `{data_dir}/store` | Badger DB path |
| `store.redis.url` | string | `redis://localhost:6379/0` | Redis URL |

### Beef Storage

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `beef.mode` | string | `embedded` | `embedded` or `disabled` |

Note: Beef chain config (providers, LRU size, filesystem path) is internal plumbing with sensible defaults. Not exposed in UI unless needed later.

### Wallet

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `wallet.mode` | string | `embedded` | `embedded` or `disabled` |
| `wallet.name` | string | `1sat-wallet` | Wallet display name |
| `wallet.db.engine` | string | `sqlite` | `sqlite` or `postgres` |
| `wallet.db.sqlite.path` | string | `{data_dir}/wallet.sqlite` | SQLite path |
| `wallet.db.postgres.url` | string | `` | Postgres connection string |

### Chaintracks

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `chaintracks.mode` | string | `embedded` | `embedded` or `remote` |
| `chaintracks.path` | string | `{data_dir}/chaintracks` | Embedded DB path |
| `chaintracks.url` | string | `` | Remote URL (when mode=remote) |

### Arcade

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `arcade.mode` | string | `embedded` | `embedded` or `remote` |
| `arcade.path` | string | `{data_dir}/arcade` | Embedded DB path |
| `arcade.url` | string | `` | Remote URL (when mode=remote) |

### JungleBus

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `junglebus.url` | string | `https://junglebus.gorillapool.io` | JungleBus server URL |
| `junglebus.token` | string | `` | Auth token |

### Indexer

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `indexer.mode` | string | `embedded` | `embedded` or `disabled` |
| `indexer.sync.enabled` | bool | `false` | Enable JungleBus sync |
| `indexer.sync.subscription_ids` | string | `` | Comma-separated subscription IDs |
| `indexer.sync.from_block` | int | `783968` | Start block height |
| `indexer.sync.concurrency` | int | `8` | Worker concurrency |
| `indexer.sync.batch_size` | int | `500` | Processing batch size |

### Overlay (shared engine)

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `overlay.storage_path` | string | `{data_dir}/overlay` | SQLite storage path |
| `overlay.storage_backend` | string | `sqlite` | `sqlite` or `postgres` |
| `overlay.storage_url` | string | `` | Postgres URL (when backend=postgres) |

Note: `overlay.mode` is derived — auto-enabled when any overlay module is enabled.

### BAP Overlay

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `overlay.bap.enabled` | bool | `false` | Enable BAP overlay |
| `overlay.bap.sub_id` | string | `` | JungleBus subscription ID |
| `overlay.bap.from_block` | int | `575000` | Start block |
| `overlay.bap.concurrency` | int | `8` | Worker concurrency |
| `overlay.bap.batch_size` | int | `1000` | Batch size |

### BSocial Overlay

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `overlay.bsocial.enabled` | bool | `false` | Enable BSocial overlay |
| `overlay.bsocial.sub_id` | string | `` | JungleBus subscription ID |
| `overlay.bsocial.from_block` | int | `575000` | Start block |
| `overlay.bsocial.concurrency` | int | `8` | Worker concurrency |
| `overlay.bsocial.batch_size` | int | `1000` | Batch size |

### OPNS Overlay

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `overlay.opns.enabled` | bool | `false` | Enable OPNS overlay |
| `overlay.opns.concurrency` | int | `8` | Genesis crawl concurrency |

Note: OPNS uses genesis crawl, not JungleBus sync.

### OrdLock Overlay

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `overlay.ordlock.enabled` | bool | `false` | Enable OrdLock overlay |
| `overlay.ordlock.sub_id` | string | `` | JungleBus subscription ID |
| `overlay.ordlock.from_block` | int | `575000` | Start block |
| `overlay.ordlock.concurrency` | int | `8` | Worker concurrency |
| `overlay.ordlock.batch_size` | int | `1000` | Batch size |

### BSV21

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `overlay.bsv21.enabled` | bool | `false` | Enable BSV21 token service |
| `overlay.bsv21.sub_id` | string | `` | JungleBus subscription ID |
| `overlay.bsv21.from_block` | int | `783968` | Start block |
| `overlay.bsv21.batch_size` | int | `1000` | Batch size |

### ORDFS

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `ordfs.enabled` | bool | `true` | Enable ORDFS content serving |
| `ordfs.redis.url` | string | `redis://localhost:6379/0` | Redis cache URL |

### Owner

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `owner.mode` | string | `disabled` | `embedded` or `disabled` |

### Paymail

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `paymail.mode` | string | `disabled` | `enabled` or `disabled` |
| `paymail.db_path` | string | `{data_dir}/paymail.db` | Database path |

### MessageBox

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `messagebox.mode` | string | `enabled` | `enabled` or `disabled` |
| `messagebox.db_path` | string | `{data_dir}/messagebox.db` | Database path |

### MongoDB (optional, for BMAP/legacy)

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `mongodb.url` | string | `` | MongoDB connection string |

## Not in Config Store

These fields from the current Viper config are **not** exposed in the config store:

- **Secrets**: `wallet.server_private_key`, `auth.api_key` (stays env var)
- **Internal plumbing**: `pubsub.*`, `txo.*`, `spends.*`, `p2p.*`, `merkle.*`, `beef.chain.*` — these have fixed defaults and don't need user configuration
- **Route prefixes**: Fixed by convention, not user-configurable
- **Pprof**: Dev-only, stays as env/flag if needed

## `{data_dir}` Expansion

All paths use `{data_dir}` as a base. The data dir is determined at startup from:
1. `--data-dir` CLI flag (highest priority)
2. `ONESAT_DATA_DIR` env var
3. Default: `~/.1sat/`

When the config store has a relative path, it's resolved relative to `data_dir`.
When the config store has an absolute path, it's used as-is.

## Setup Wizard Flow

1. Server starts, opens config store at `{data_dir}/config.db`
2. If `setup.complete` is not `"true"`, serve only admin UI in wizard mode
3. Wizard steps:
   - Auth mode (local vs authenticated)
   - Database config (defaults preset, advanced: customize paths/engines)
   - Overlay selection (which overlays to enable, subscription IDs)
   - Server config (advanced: port, host, base path)
   - Review and save
4. Save all values to config store, set `setup.complete = "true"`
5. Server initializes with stored config

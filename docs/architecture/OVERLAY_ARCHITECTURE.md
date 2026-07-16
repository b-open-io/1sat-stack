# Overlay Architecture

This document describes the overlay system architecture in 1sat-stack.

## Overview

The overlay system provides topic-based transaction filtering and indexing using the BSV Overlay Services protocol. Transactions are submitted with topic tags, validated by topic managers, and indexed by lookup services. Each topic gets its own isolated SQLite database (or shared PostgreSQL with topic_id isolation).

## Components

### Overlay Engine (`go-overlay-services`)

The external `go-overlay-services` package provides:
- `engine.Engine` - Coordinates topic managers and lookup services
- `engine.Storage` - Interface for output storage (implemented by `EngineAdapter`)
- `engine.TopicManager` - Interface for admission logic
- `engine.LookupService` - Interface for indexing admitted outputs

### Per-Topic Storage (`pkg/overlay/storage/`)

Each topic gets its own isolated database via `SQLiteFactory` or `PostgresStorage`:

**SQLite** (default): One file per topic at `{storagePath}/{topic}.db` with WAL mode, separate read/write connection pools (1 writer, 4 readers).

**PostgreSQL**: Shared database with `topic_id` column in compound keys for isolation.

Both backends implement `TopicStorage` — the per-topic interface used by lookup services.

A shared `TxTopicIndex` (`tx_topics.db`) maps txid→topics for cross-topic lookups.

### EngineAdapter (`pkg/overlay/storage/adapter.go`)

Bridges the overlay engine to per-topic storage:
- Routes `InsertOutputs` calls to the correct topic database
- Saves BEEF to shared `BeefStorage`
- Calls `OutputStore.IngestTx()` for general indexer integration
- Records txid→topic mappings in `TxTopicIndex`

### Topic Managers (`pkg/topic/`)

Decide which outputs to admit to a topic:

| Topic | Manager | Admits |
|-------|---------|--------|
| `tm_bsv21` | `Bsv21DiscoveryTopicManager` | All BSV21 deploy operations |
| `tm_{tokenId}` | `Bsv21ValidatedTopicManager` | Valid transfers for specific token |
| `tm_1sat_collection` | `DiscoveryTopicManager` (collection) | Collection mints (MAP + SIGMA) |
| `tm_col_{collectionId}` | `ItemTopicManager` (collection) | Collection item mints for one collection |
| `tm_1sat` | `OneSatTopicManager` | All outputs (catch-all) |
| `tm_bap` | `TopicManager` (BAP) | Structurally valid BAP+AIP outputs |
| `tm_bsocial` | `TopicManager` (BSocial) | BSocial protocol outputs |
| `tm_opns` | `TopicManager` (OPNS) | OPNS protocol outputs |
| `tm_ordlock` | `TopicManager` (OrdLock) | OrdLock listing outputs |

### Lookup Services (`pkg/lookup/`)

Index admitted outputs by adding custom schemas to the topic's database:

| Service | Custom Tables | Events Indexed |
|---------|--------------|----------------|
| `BSV21Lookup` | `token_outputs` per topic DB | Token operations (deploy, transfer, burn) |
| `OneSatLookup` | Uses shared events table | `own:`, `txid:`, tag-specific events |
| `BAPLookup` | `bap_identity_addresses`, `bap_attestations` | Identity rotations, attestations |
| `OrdLockLookup` | `listings` | Marketplace listings |

Lookup services access the topic database via `TopicStorage.DB()` and lazily create their custom tables on first use.

---

## Data Flow

### Submit Flow

```
Client/Worker
     │
     │ Overlay.Submit(TaggedBEEF{Topics: ["tm_xxx"]})
     ▼
Engine.Submit()
     │
     ├─→ TopicManager.IdentifyAdmissibleOutputs()
     │         │
     │         └─→ Returns OutputsToAdmit, CoinsToRetain
     │
     ├─→ EngineAdapter.InsertOutputs()
     │         │
     │         ├─→ BeefStorage.SaveBeef()          (shared BEEF store)
     │         ├─→ OutputStore.IngestTx()           (general indexer)
     │         ├─→ topicDB(topic).InsertOutput()    (per-topic SQLite)
     │         └─→ TxTopicIndex.Record()            (cross-topic index)
     │
     ├─→ EngineAdapter.InsertAppliedTransaction()
     │         │
     │         └─→ topicDB(topic).InsertAppliedTx()
     │
     └─→ LookupService.OutputAdmittedByTopic()
               │
               └─→ topicDB(topic).DB().Exec(...)    (custom tables)
```

### Query Flow

```
Client
     │
     │ GET /1sat/lookup/{service}?query=...
     ▼
LookupService.Lookup()
     │
     └─→ topicDB(topic).DB().Query(...)
               │
               └─→ Read from custom tables (token_outputs, listings, etc.)
```

---

## Per-Topic Database Schema

Each topic's SQLite database contains these engine-managed tables:

### `outputs`
| Column | Type | Purpose |
|--------|------|---------|
| `outpoint` | BLOB | Primary key (36 bytes) |
| `txid` | BLOB | Transaction ID |
| `satoshis` | INTEGER | Output value |
| `spend_txid` | BLOB | Spending transaction (NULL if unspent) |
| `score` | REAL | HeightScore for ordering |
| `deps` | BLOB | Dependency data |
| `inputs_consumed` | BLOB | Inputs consumed by this output |
| `consumed_by` | BLOB | Who consumed this output |

### `applied_txs`
Transaction membership tracking per topic.

### `events`
| Column | Type | Purpose |
|--------|------|---------|
| `event` | TEXT | Event key (e.g., `own:{address}`) |
| `outpoint` | BLOB | Associated output |
| `score` | REAL | HeightScore for ordering |

### `peer_interactions`
GASP peer sync state tracking.

Lookup services add custom tables as needed (e.g., `token_outputs` for BSV21, `listings` for OrdLock).

---

## Storage Configuration

```yaml
overlay:
  storage_path: ~/.1sat/overlay    # Base directory for SQLite files
  storage_backend: sqlite           # "sqlite" or "postgres"
  storage_url: ""                   # PostgreSQL URL (when backend=postgres)
```

SQLite files created per topic:
```
~/.1sat/overlay/
├── tm_bsv21.db          # BSV21 discovery topic
├── tm_{tokenId}.db      # Per-token topics (created dynamically)
├── tm_bap.db            # BAP identity topic
├── tm_bsocial.db        # BSocial topic
├── tm_opns.db           # OPNS topic
├── tm_ordlock.db        # OrdLock marketplace topic
└── tx_topics.db         # Shared txid→topics index
```

---

## Topic Registration

Topics are registered dynamically with the overlay engine:

```go
// Register topic manager factory
overlay.RegisterTopicManagerFactory("bsv21", func(topic string) engine.TopicManager {
    return topic.NewBsv21ValidatedTopicManager(topic, storage, logger)
})

// Activate a topic (creates manager instance + per-topic DB)
overlay.ActivateTopic("tm_abc123...i0")
```

The Token Manager (`pkg/bsv21/manager.go`) dynamically registers and unregisters topics for active tokens during its periodic lifecycle check.

---

## Score Consistency

All scores use `types.HeightScore()`:
- **Confirmed**: `blockHeight + blockIdx/1e9`
- **Unconfirmed**: `time.Now().UnixNano() / 1e9`

Helper functions:
- `types.HeightScore(height, idx)` - Build from block position
- `types.ScoreFromTx(tx, txid)` - Extract from parsed transaction
- `types.ScoreFromBeef(beef)` - Extract from BEEF bytes

---

## Related Documentation

- `pkg/store/KEYS.md` - Storage key reference for the main data store (Badger/Redis)
- `docs/architecture/BSV21_PIPELINE.md` - BSV21-specific processing pipeline

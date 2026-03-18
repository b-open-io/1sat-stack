# Overlay Storage Isolation

**Status: Complete**

Move overlay data out of the shared TXO store into per-topic SQLite databases with proper schemas. The shared store keeps only the general-purpose TXO index (owner events, spends, satoshis, merkle state). Each overlay gets a self-contained database with its own membership tracking, GASP state, lookup events, and domain-specific data.

## Current State

All overlay data lives in the shared Badger/Redis store alongside the general TXO index, separated only by key prefixes. The overlay engine writes `tp:*`, `dp:*`, `in:*`, `dt:*` keys into the same keyspace as `ev:*`, `sats`, `spnd`, etc. BAP and BSocial additionally use MongoDB for domain-specific queries. OPNS and BSV21 store everything in the shared store.

### Processing Paths

Separate JungleBus subscriptions feed each concern:

| Subscription | Queue | Processing |
|---|---|---|
| Ingest (1+ IDs) | `q:ingest` | General indexer — parses all outputs, saves events/sats/spends to shared store |
| BSV21 | `q:bsv21` | Overlay engine → `InsertOutputs` → triggers `IngestTx` callback → general indexer |
| BAP | `q:bap` | Same pattern (sequential, concurrency=1) |
| BSocial | `q:bsocial` | Same pattern (concurrency=8) |

Overlay subscriptions cascade into the general indexer via the `IngestTx` callback wired at `cmd/server/config.go#L657`. Every overlay-admitted output gets full general indexer treatment (events, sats, spends).

### Current engine.Storage No-Ops

The `go-overlay-services` engine defines a `Storage` interface. 1sat-stack's `OutputStore` implements it, but several methods are no-ops because the shared store allowed shortcuts:

| Method | Status | Resolution |
|---|---|---|
| `UpdateConsumedBy` | **No-op** (derived on the fly) | Must become a real persist with isolated DBs |
| `ReconcileMerkleRoot` | **No-op** | Not needed — PendingAuditor handles proof validation globally |
| `FindOutpointsByMerkleState` | **Stub** (queries empty ZSet) | Not needed — `SyncInvalidatedOutputs` is never called |

### Existing Overlay Storage Patterns

- **BAP**: MongoDB — `identity`, `attestation`, `profile` collections
- **BSocial**: MongoDB — `post`, `like`, `follow`, `unfollow` collections
- **OPNS**: Shared store ZSets — `tm_opns:name:*`, `tm_opns:mine:*`
- **BSV21**: Most entangled — dynamic per-token topics, holder balance ZSets (`tm_{tokenId}:*`), tag data (`dt:bsv21`), whitelist/blacklist sets

### Old Standalone Overlay Pattern (Reference)

The `overlay/storage/` package (in the standalone `bsv21-overlay` project) created one SQLite per topic. Factory at `overlay/storage/sqlite.go#L83` generates paths like `overlay_tm_{tokenId}.db`. Each DB had:
- `outputs` table: outpoint, txid, spend, block_height, score, metadata (deps/inputs as JSON), data (protocol-specific JSON)
- `events` table: event string, outpoint, score

Topics lazy-loaded via `sync.Map`. WAL mode with separate read/write connections.

## Target Architecture

```
Shared TXO Store (Badger/Redis)
├── ev:{event} sorted sets          (general events: own:*, txid:*, type:*, etc.)
├── ev:{event}:spnd sorted sets     (spent general events)
├── sats hash                       (satoshi values per outpoint)
├── spnd hash                       (spend txid per outpoint)
├── h:{outpoint} hash               (ev + ms fields only — no dt:*, dp:*, in:*)
├── q:* queues                      (JungleBus ingest and overlay subscription queues)
├── tx:pending/immutable/rollback   (transaction lifecycle logs)
└── prog hash                       (JungleBus + owner sync progress)

BEEF Store (shared, unchanged)
└── Transaction data + merkle proofs (tiered: LRU → Redis → JungleBus → FS/Badger)

Per-Topic SQLite (one DB per topic)
├── outputs table                   (membership, GASP state, spend tracking)
├── applied_txs table               (dedup for applied transactions)
├── events table                    (overlay-scoped lookup events)
├── peer_interactions table         (GASP sync timestamps per host)
└── (optional custom tables per overlay for complex lookups)
```

The `IngestTx` callback continues to fire when overlays admit transactions, populating the general TXO index for owner sync.

### Data Duplication (Intentional)

Outputs admitted by overlays exist in both the shared TXO store and the overlay's SQLite. The duplicated fields per output are small (~100 bytes: outpoint, spend status, satoshis, timestamp). The shared store serves owner sync and general queries (scored by block height). Overlay databases serve overlay-specific queries (scored by ingestion timestamp). Different purposes, different access patterns.

### BEEF and Proof Handling

BEEF is always loaded from the shared store at read time — `FindOutput(includeBEEF=true)` calls `BeefStore.LoadBeef()` (engine_storage.go#L116). Overlays never store or cache BEEF.

Proof lifecycle is handled by two independent mechanisms:

1. **PendingAuditor** (pkg/indexer/pending_auditor.go) — global per-transaction audit. Runs on each new block, validates proofs against chaintracks, refetches from Arcade/JungleBus if invalid, promotes to `tx:immutable` (100+ blocks deep) or rolls back (3+ hours unconfirmed). Updates BEEF in the shared store.

2. **HandleNewMerkleProof** (overlay engine) — propagates proofs forward through the UTXO graph. Walks `consumed_by` to find child transactions and updates their BEEFs in the shared store. This traversal is why overlays need the `consumed_by` field.

The engine's `MerkleState` enum, `ReconcileMerkleRoot`, `FindOutpointsByMerkleState`, and `SyncInvalidatedOutputs` are all stubs/unused in 1sat-stack. The overlay schema does not need `merkle_state` or `block_height` columns.

## Per-Topic SQLite Schema

```sql
CREATE TABLE outputs (
    outpoint          BLOB PRIMARY KEY,  -- 36 bytes (txid 32 LE + vout 4 BE)
    txid              BLOB NOT NULL,     -- 32 bytes (for rollback-by-txid queries)
    satoshis          INTEGER,
    spend_txid        BLOB,              -- NULL if unspent
    score             REAL NOT NULL,     -- ingestion timestamp (not block height)
    deps              BLOB,              -- concatenated 32-byte ancillary txids
    inputs_consumed   BLOB,              -- concatenated 36-byte consumed outpoints
    consumed_by       BLOB,              -- concatenated 36-byte outpoints that consumed this
    created_at        INTEGER NOT NULL   -- unix timestamp
);
CREATE INDEX idx_outputs_txid ON outputs(txid);
CREATE INDEX idx_outputs_unspent ON outputs(spend_txid) WHERE spend_txid IS NULL;

CREATE TABLE applied_txs (
    txid    BLOB PRIMARY KEY,  -- 32 bytes
    score   REAL NOT NULL
);

CREATE TABLE events (
    event       TEXT NOT NULL,     -- overlay-scoped event string
    outpoint    BLOB NOT NULL,     -- 36 bytes
    score       REAL NOT NULL,     -- ingestion timestamp
    PRIMARY KEY (event, outpoint)
);
CREATE INDEX idx_events_score ON events(event, score);

CREATE TABLE peer_interactions (
    host  TEXT PRIMARY KEY,
    since REAL NOT NULL           -- Unix timestamp of last GASP sync
);
```

### Score: Ingestion Timestamp

Overlay databases use ingestion timestamp as score. Write-once — never updates when mempool tx confirms. Sufficient for overlay ordering and GASP sync. The general TXO store continues using HeightScore for chain-ordered owner sync.

### Blob Columns

- `deps` and `inputs_consumed` — loaded for individual outputs only, never filtered/queried across rows.
- `consumed_by` — also a blob, but **always loaded** with the output record. The engine iterates it for merkle proof propagation (walking the UTXO graph forward) and `deleteUTXODeep` (checking if an output is a leaf node). Not queried across rows.
- All three use concatenated fixed-width binary, same encoding as today.

## Go Interface

```go
// TopicStorage is the per-topic overlay database.
// The overlay engine writes membership/GASP state.
// Lookup services write scoped events and optionally use DB() for custom tables.
type TopicStorage interface {
    // --- Engine writes (generic for all overlays) ---
    InsertOutput(ctx context.Context, op *transaction.Outpoint, txid *chainhash.Hash, satoshis uint64, deps []byte, inputsConsumed []byte, score float64) error
    GetOutput(ctx context.Context, op *transaction.Outpoint) (*OutputRecord, error)
    FindOutputs(ctx context.Context, outpoints []*transaction.Outpoint) ([]OutputRecord, error)
    FindOutputsForTransaction(ctx context.Context, txid *chainhash.Hash) ([]OutputRecord, error)
    MarkSpent(ctx context.Context, op *transaction.Outpoint, spendTxid *chainhash.Hash) error
    UpdateConsumedBy(ctx context.Context, op *transaction.Outpoint, consumedBy []byte) error
    FindUTXOs(ctx context.Context, opts *QueryOpts) ([]OutputRecord, error)
    DeleteOutput(ctx context.Context, op *transaction.Outpoint) error
    Rollback(ctx context.Context, txid *chainhash.Hash) error

    InsertAppliedTx(ctx context.Context, txid *chainhash.Hash, score float64) error
    HasAppliedTx(ctx context.Context, txid *chainhash.Hash) (bool, error)

    // --- GASP peer sync ---
    UpdateLastInteraction(ctx context.Context, host string, since float64) error
    GetLastInteraction(ctx context.Context, host string) (float64, error)

    // --- Lookup service writes (overlay-scoped events) ---
    SaveEvent(ctx context.Context, event string, op *transaction.Outpoint, score float64) error
    DeleteEvent(ctx context.Context, event string, op *transaction.Outpoint) error
    FindByEvent(ctx context.Context, event string, opts *QueryOpts) ([]OutputRecord, error)

    // --- Escape hatch for custom lookup schemas ---
    DB() *sql.DB

    Close() error
}

// TopicStorageFactory creates a TopicStorage instance per topic.
// SQLite impl creates {basePath}_{topic}.db with WAL mode.
// Interface allows alternative backends (Postgres, MySQL) in the future.
type TopicStorageFactory func(topic string) (TopicStorage, error)
```

### Adapter to engine.Storage

The `engine.Storage` interface (from go-overlay-services) is what the overlay engine calls. An adapter will sit between the engine and `TopicStorage`:

- Routes topic-specific operations to the correct per-topic `TopicStorage` instance
- Delegates BEEF operations (`UpdateTransactionBEEF`, `LoadAncillaryBeef`) to the shared `BeefStore`
- Stubs `ReconcileMerkleRoot`, `FindOutpointsByMerkleState` (unused)
- Delegates `UpdateOutputBlockHeight` — TBD whether this is needed or can be a no-op

## What Moves Out of the Shared Store

| Current Key/Field | Currently In | Moves To | Notes |
|---|---|---|---|
| `tp:{topic}` ZSet | Shared store | `outputs` table | Topic membership |
| `tp:{topic}:tx` ZSet | Shared store | `applied_txs` table | Applied tx dedup |
| `dp:{topic}` hash field | Per-output hash | `deps` column on outputs | Ancillary txids |
| `in:{topic}` hash field | Per-output hash | `inputs_consumed` column on outputs | Consumed inputs |
| `ConsumedBy` | Derived (no-op) | `consumed_by` column on outputs | Now persisted |
| `dt:{tag}` hash field | Per-output hash | Lookup service events or custom tables | Overlay-specific data |
| `tm_{tokenId}:*` ZSets | Shared store | BSV21 lookup events/custom tables | Token holder tracking |
| `tm_opns:*` ZSets | Shared store | OPNS events table | Domain name lookups |
| `bsv21:whitelist/blacklist` Sets | Shared store | BSV21 custom table via `DB()` | Token config |
| `merkle:{topic}:{state}` ZSet | Shared store (never written) | Removed | Not needed |
| Overlay-scoped `ev:*` entries | Shared `ev:*` ZSets | `events` table | e.g. `bsv21:{tokenId}`, `bap:type:*` |
| `peer:{topic}:{host}` in `prog` | Shared `prog` hash | `peer_interactions` table | GASP peer sync timestamps |

## What Stays in the Shared Store

| Key/Field | Purpose |
|---|---|
| `ev:own:{addr}` / `ev:txid:{hash}` / `ev:type:*` etc. | General events from parser — owner sync, search API |
| `ev:own:{addr}:spnd` | Spent event tracking for owner sync |
| `sats` hash | Satoshi values for balance queries |
| `spnd` hash | Spend tracking for general search filtering |
| `h:{outpoint}` with `ev` + `ms` fields only | General event list + merkle state per output |
| `q:*` queues | JungleBus ingest and overlay subscription queues |
| `tx:pending/immutable/rollback` | Transaction lifecycle audit (PendingAuditor) |
| `prog` hash (minus peer interactions) | JungleBus subscription + owner sync progress |

## Per-Overlay Migration Notes

### BSV21
- Most entangled with shared store today
- TokenManager creates dynamic per-token topics — each gets its own SQLite
- Lookup service currently reads `dt:bsv21` tag data and `tm_*` ZSets from shared store — both must move
- `bsv21:whitelist/blacklist` moves to a config table via `DB()`
- Token fee balance checking currently queries `ev:own:{feeAddr}` in shared store — this stays (general owner query)
- General indexer continues writing `dt:bsv21` to the shared store for the general search API — that's a separate concern

#### BSV21 Custom Schema (via `DB()`)

```sql
CREATE TABLE token_outputs (
    outpoint     BLOB PRIMARY KEY,  -- 36 bytes
    token_id     TEXT NOT NULL,      -- deploy outpoint string
    op           TEXT NOT NULL,      -- deploy+mint, deploy+auth, transfer, mint, burn, auth
    lock_type    TEXT NOT NULL,      -- p2pkh, cos
    address      TEXT NOT NULL,      -- lock address
    amount       INTEGER NOT NULL,   -- token amount
    sym          TEXT,               -- deploy only
    dec          INTEGER,            -- deploy only
    icon         TEXT,               -- deploy only
    spend_txid   BLOB,              -- NULL if unspent
    score        REAL NOT NULL
);
CREATE INDEX idx_token_utxos ON token_outputs(token_id, lock_type, address, score) WHERE spend_txid IS NULL;
```

- Balance: `SELECT SUM(amount) ... WHERE spend_txid IS NULL` — replaces loading every outpoint and deserializing JSON
- Token metadata: read sym/dec/icon from the deploy output row by token_id
- Paginated UTXOs/history: ordered by score via the index
- Replaces `tm_{tokenId}:{lockType}:{address}` ZSets and `dt:bsv21` tag data reads in the lookup service

### OPNS
- Simple. `tm_opns:name:{domain}` and `tm_opns:mine:{prefix}` become events like `name:{domain}` and `mine:{prefix}`
- Append-only pattern (highest score wins) maps naturally to events table queries

### BAP
- Already uses MongoDB for domain data (identity/attestation/profile collections)
- **Keep MongoDB** for lookup service — schema is mature and working
- Generic overlay storage (SQLite) replaces the `tp:tm_bap` bookkeeping in shared store

### BSocial
- Already uses MongoDB for social graph (post/like/follow/unfollow collections)
- **Keep MongoDB** for lookup service — same rationale as BAP
- Generic overlay storage (SQLite) replaces shared store bookkeeping

## Implementation Order

1. **Define the `TopicStorage` interface and SQLite implementation** in a new `pkg/overlay/storage/` package
2. **Create the `TopicStorageFactory`** with per-topic SQLite database creation (WAL mode, separate read/write connections, lazy-loaded via sync.Map)
3. **Build the engine.Storage adapter** that routes to per-topic `TopicStorage` instances + shared `BeefStore`
4. **Update `engine_storage.go`** to use the adapter instead of writing directly to the shared `store.Store`
5. **Migrate OPNS lookup service** — simplest overlay, validates the pattern
6. **Migrate BSV21 lookup service and routes** — ✅ lookup service uses `token_outputs` custom table via `DB()`. Routes migrate: ValidateOutputs/GetTokenOutput check `token_outputs` directly, GetTransaction queries overlay `outputs.txid` joined with `token_outputs`, GetBlockData intersects general indexer HeightScore query with `token_outputs`
7. **Migrate BAP/BSocial** — ✅ No changes needed. MongoDB lookups stay as-is. Generic overlay storage (topic membership, applied_txs, GASP state) already routed through engine adapter (Steps 3-4). OverlaySync's shared store usage is queue-only (operational, not overlay storage).
8. **Multi-tenant BSV21 lookup and shared store cleanup** — ✅ BSV21Lookup takes factory reference for multi-tenant per-topic databases. TokenManager/Worker migrated off `tp:*` ZSets to use BSV21Lookup methods. Remaining: `engine_storage.go` deletion blocked by rollback handlers in `status_handler.go` and `merkle/service.go` (see Step 8b).
8b. **Rollback handler migration** — ✅ Created `TxTopicIndex` (dedicated SQLite database for txid→topics mapping). Refactored `SQLiteFactory` from closure to struct holding both per-topic databases and the index. `EngineAdapter.InsertOutputs` records txid→topic at admission time. `FindOutputsForTransaction` uses the index to query the right topic databases. Rollback handlers (`status_handler.go`, `merkle/service.go`) now take `engine.Storage` instead of `*txo.OutputStore`. `engine_storage.go` can now be deleted.
9. **Remove overlay-scoped events from shared `ev:*` space** — ✅ Removed dead `bap:type:*` and `bap:id:*` events from parser (written but never read). `bsv21:{tokenId}` events kept — legitimately part of general indexer (provides HeightScore ordering for GetBlockData route). OPNS/BSocial already use per-topic SQLite events, not shared store.
10. **Move peer interaction tracking** — ✅ Already done. EngineAdapter routes `UpdateLastInteraction`/`GetLastInteraction` to per-topic SQLite `peer_interactions` table (built in Steps 3-4). OutputStore versions in `engine_storage.go` are dead code.

## Open Questions

None — all resolved. See Decision Log.

## Decision Log

| Date | Decision | Rationale |
|------|----------|-----------|
| 2026-03-10 | Per-topic SQLite with generic schema | Matches old overlay pattern, clean isolation, proper relational schema |
| 2026-03-10 | Ingestion timestamp as score (not HeightScore) | Write-once, no updates on confirmation, sufficient for overlay ordering |
| 2026-03-10 | Blob columns for deps/inputs_consumed/consumed_by | Never queried across rows, only loaded per-output. Keeps schema simple. |
| 2026-03-10 | consumed_by must be persisted (not derived) | Isolated DB can't derive from shared store. Used by merkle propagation and deleteUTXODeep. |
| 2026-03-10 | Generic events table for lookup services | Covers OPNS and BSV21 without custom schema. BAP/BSocial can use DB() for complex lookups. |
| 2026-03-10 | Peer interactions move to overlay DB | Per-topic GASP sync state, not a shared concern |
| 2026-03-10 | BEEF store stays shared | Transaction data and merkle proofs are not overlay-specific |
| 2026-03-10 | BEEF loaded from shared store at read time | FindOutput(includeBEEF=true) calls BeefStore.LoadBeef() — overlays never cache BEEF |
| 2026-03-10 | No merkle_state/block_height in overlay schema | Proof validation is PendingAuditor + shared BEEF store. Overlay provides consumed_by for graph traversal only. |
| 2026-03-10 | Engine merkle stubs remain no-ops | ReconcileMerkleRoot, FindOutpointsByMerkleState, SyncInvalidatedOutputs — never called in 1sat-stack |
| 2026-03-10 | BAP/BSocial keep MongoDB for lookups | Mature schemas, working well. Only generic overlay storage moves to SQLite. |
| 2026-03-10 | OPNS uses generic events table for lookups | Simple key→outpoint patterns fit the events table. No custom schema needed. |
| 2026-03-10 | BSV21 uses custom `token_outputs` table via DB() | Fixed schema with typed columns enables SQL aggregation for balance queries. Replaces JSON deserialization + in-memory summing. |
| 2026-03-10 | General indexer keeps writing dt:bsv21 to shared store | General search API serves tag data independently. BSV21 lookup service must not read from shared store. |
| 2026-03-10 | Lookup storage is per-package, not generic | Each overlay package owns its lookup strategy. No need for a shared lookup abstraction. |
| 2026-03-10 | Start SQLite-only for TopicStorageFactory | Interface exists for future backends. No need to build Postgres/MySQL impl now. |
| 2026-03-10 | No overlay data in general TXO search | General search only sees indexer-written events. Overlay lookups are separate concerns. No consumer migration needed. |
| 2026-03-11 | BSV21 routes query overlay DB, not shared store | ValidateOutputs/GetTokenOutput check `token_outputs` directly (no token_id filter needed — presence = membership). GetTransaction joins overlay `outputs.txid` with `token_outputs`. No shared store `tp:*` or `ev:txid:*` queries. |
| 2026-03-11 | Remove `ev:txid:*` events from general indexer | Only consumer was BSV21 GetTransaction route. Overlay's `outputs` table has txid column with index. Stop writing these events entirely. |
| 2026-03-11 | GetBlockData uses general indexer + overlay intersection | Query `ev:bsv21:{tokenId}` by HeightScore range for outpoints at a block height, then query `token_outputs` for those outpoints to get BSV21 data. General indexer provides height ordering, overlay provides data. |
| 2026-03-11 | `token_outputs` and `outputs` share same SQLite DB | Both tables in the `tm_bsv21` topic database. Can join on outpoint when needed (e.g. txid lookup via `outputs.txid`). |
| 2026-03-11 | BAP/BSocial need no migration (Step 7 complete) | MongoDB lookups untouched. Generic overlay storage already provided by engine adapter (Steps 3-4). OverlaySync shared store usage is queue consumption only — not overlay storage. |
| 2026-03-11 | BSV21Lookup uses factory for multi-tenant per-topic databases | Lookup service references the topic storage system (factory) instead of a single TopicStorage. `OutputAdmittedByTopic` resolves `factory(payload.Topic)` → per-topic SQLite. Each topic's database gets its own `token_outputs` table. `tm_bsv21` has only deploys, `tm_{tokenId}` has only that token's outputs. |
| 2026-03-11 | TokenManager/Worker migrate off `tp:*` ZSets | TokenManager discovery: query `tm_bsv21` token_outputs for distinct token_ids (no WHERE needed — only deploys in that topic). Worker output count: query `tm_{tokenId}` token_outputs COUNT. Both via BSV21Lookup methods backed by factory. |
| 2026-03-11 | `engine_storage.go` on OutputStore is dead code | Engine uses EngineAdapter (Steps 3-4). OutputStore's engine.Storage methods are no longer called by the engine. Still called directly by rollback handlers — blocked on Step 8b. |
| 2026-03-11 | Rollback txid→topics mapping in shared store | Rollback handlers need to know which topic databases contain outputs for a txid. Store `tx:{txid}` → `[]topics` in shared store at admission time (EngineAdapter.InsertOutputs). Read on rollback. Avoids iterating all topic databases. |

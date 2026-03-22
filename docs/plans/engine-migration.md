# 1sat-engine: Redis Protocol Migration

Status: **In Progress**

Linear Project: **1sat-engine** (OPL)
Branch: `engine` (worktree at `/Users/davidcase/Source/1sat/1sat-engine/`)

## Goal

Replace the custom `Store` interface with the Redis protocol as the canonical data interface. Consumers use go-redis (or any Redis client in any language). Backend is redcon + Badger in-process.

This is the foundation for the distributed compute vision (see `docs/research/distributed-compute-vision.md`).

## Dual Channel Model

Modules receive named storage channels, not Go interfaces:

- **Redis channel** — KV, hashes, sorted sets, pub/sub. Hot-path operational state.
- **SQLite channel** — structured queries, aggregates, joins, text search. Relational data.

Both deterministic. Both well-specified. Both compile to WASM. Both universally known.

## Migration Progress

### Phase 1: Store → go-redis (Package Migration)

| Package | Status | Store Methods Used | Notes |
|---------|--------|--------------------|-------|
| `pkg/store/redcon/` | ✅ Done | — | RESP server backed by BadgerStore |
| `pkg/spends/` | ✅ Done | HGet, HMGet, HSet, HDel | 4 hash calls, simplest package |
| `pkg/overlay/services.go` | ✅ Done | Get, Set, Del | 3 KV calls for remote config |
| `pkg/overlay/event_bridge.go` | ✅ Done | ZAdd | 1 sorted set call |
| `pkg/overlay/p2p.go` | ✅ Done | ZAdd | 1 sorted set call |
| `pkg/overlay/topic.go` | ✅ Done | ZCard | 1 sorted set call |
| `pkg/worker/` | ✅ Done | ZRangeByScore, ZAdd, ZRem | Queue processing |
| `pkg/gasp/` | ✅ Done | ZRangeByScore, ZAdd | 3 files, queue + SSE |
| `pkg/jbsync/` | ✅ Done | ZAdd (batch + single) | JungleBus subscription |
| `pkg/merkle/` | ✅ Done | ZAdd, ZRem | Log lifecycle (pending/immutable/rollback) |
| `pkg/bsv21/manager+sync` | Pending | ZAdd, ZCard | Token service queue ops |
| `pkg/indexer/` | Pending | ZAdd, ZRem, ZRange | Pending auditor + status handler |
| `pkg/txo/` | Pending | HSet, HGet, HGetAll, HDel, HMGet, ZAdd, ZRem, ZScore, Scan | Heaviest user — 19 hash + sorted set calls |

### Phase 2: Wiring

| Task | Status | Notes |
|------|--------|-------|
| Update `cmd/server/config.go` | Pending | Central wiring — create redcon server, pass `*redis.Client` to all packages |
| Remove `Store` interface | Pending | After all packages migrated |
| Multi-store configuration | Pending | Named stores (txo, beef, topics, config) with separate backends |

### Phase 3: Engine Storage Redesign

Move `EngineAdapter`/`TopicStorage` from native SQLite back to Redis channel.

| Task | Status | Notes |
|------|--------|-------|
| Pull `SaveEvent`/`DeleteEvent`/`FindByEvent` out of `TopicStorage` | Pending | Move to lookup layer — these serve lookups, not the engine |
| Reimplement engine output ops against `*redis.Client` | Pending | Follow pattern from deleted `engine_storage.go` (commit `71570c9`). Hash per outpoint, sorted set for topic membership/UTXO ordering, KV for applied txs and peer timestamps |
| Update `EngineAdapter` to route to per-topic Redis keys | Pending | Replace per-topic SQLite factory with per-topic Redis key prefixes |
| Remove `pkg/overlay/storage/sqlite.go` engine tables | Pending | Keep `outputs` table removal separate from lookup schema |

### Phase 4: Overlay Lookup Services

Lookups stay on SQLite channel. Engine storage (Phase 3) and lookup storage are now cleanly separated.

| Overlay | Lookup Storage | Channel | Notes |
|---------|---------------|---------|-------|
| OPNS | TopicStorage events | Redis | Already event-shaped, no relational queries |
| OrdLock | Custom in-memory struct | Redis | Minimal storage |
| BAP | 3 tables, joins, text search | SQLite | Relational queries required |
| BSV21 | `token_outputs` table (per-token DB via `DB()` escape hatch) | SQLite | Balance aggregates, address queries, history |
| BSocial | MongoDB (aggregation pipelines) | TBD | Needs migration — evaluate Redis vs SQLite |

## Key Decisions

1. **Redis protocol is the canonical data interface** — not a Go abstraction
2. **Modules receive channels, not typed clients** — the channel is the address/stream, consumer wraps it in whatever Redis client they prefer
3. **Multiple named stores per app** — txo, beef, topics, config are separate databases, not namespaces
4. **SQLite as second channel type** — for relational data (BAP, BSV21) where Redis primitives are insufficient
5. **Host manages isolation** — which stores a module can access is configuration, not module choice
6. **Engine storage moves back to Redis channel** — The `EngineAdapter`/`TopicStorage` currently uses native SQLite for overlay output membership. This was originally Badger-backed (`pkg/txo/engine_storage.go`, deleted in `71570c9`). The core engine operations (InsertOutput, GetOutput, FindOutputs, MarkSpent, FindUTXOs, DeleteOutput, Rollback, applied txs, peer interactions) are all single-table CRUD that maps cleanly to Redis hashes and sorted sets. Move back to Redis channel using the pattern from the deleted `engine_storage.go`.
7. **Event methods belong in lookup layer, not engine storage** — `SaveEvent`, `DeleteEvent`, `FindByEvent` are in `TopicStorage` but serve lookups, not the engine. `FindByEvent` does a JOIN between events and outputs — that's a lookup concern. Pull these out of `TopicStorage` into the lookup services. Lookups that need to join event data back to output data do the join in code.
8. **BSV21 already demonstrates both channels** — BSV21 has three layers: (a) queue ops via `store.Store` (ZAdd, ZCard) → Redis channel, (b) `BSV21Lookup` with `token_outputs` table via `TopicStorage.DB()` → SQLite channel, (c) routes join across both channels in application code (e.g. `GetBlockData` searches Redis OutputStore events then loads from SQLite lookup). This is the pattern for all overlays.

## Store Interface Audit Summary

18 of 25 Store methods actually used, 63 total calls across codebase.
7 unused methods: SAdd, SMembers, SRem, SIsMember, HMSet, ZRevRange, ZIncrBy, ZSum, ZKeys.
No transactions, no pipelines, no conditional operations.
Heavy sorted set usage (31 calls across 8 packages).
Hash operations concentrated in txo (19 of 23 total).

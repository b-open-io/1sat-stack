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

### Phase 3: Overlay Lookup Services

| Overlay | Current Store | Migration Path |
|---------|--------------|----------------|
| OPNS | TopicStorage events (sorted sets) | Redis channel — already event-shaped |
| OrdLock | Custom in-memory struct | Redis channel |
| BAP | SQLite (3 tables, joins, text search) | SQLite channel |
| BSV21 | Per-topic SQLite (token_outputs table) | SQLite channel |
| BSocial | MongoDB (aggregation pipelines) | Needs migration anyway — evaluate Redis vs SQLite |

## Key Decisions

1. **Redis protocol is the canonical data interface** — not a Go abstraction
2. **Modules receive channels, not typed clients** — the channel is the address/stream, consumer wraps it in whatever Redis client they prefer
3. **Multiple named stores per app** — txo, beef, topics, config are separate databases, not namespaces
4. **SQLite as second channel type** — for relational data (BAP, BSV21) where Redis primitives are insufficient
5. **Host manages isolation** — which stores a module can access is configuration, not module choice

## Store Interface Audit Summary

18 of 25 Store methods actually used, 63 total calls across codebase.
7 unused methods: SAdd, SMembers, SRem, SIsMember, HMSet, ZRevRange, ZIncrBy, ZSum, ZKeys.
No transactions, no pipelines, no conditional operations.
Heavy sorted set usage (31 calls across 8 packages).
Hash operations concentrated in txo (19 of 23 total).

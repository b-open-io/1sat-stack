# 1sat-engine: Redis Protocol Migration

Status: **In Progress**

Linear Project: **1sat-engine** (OPL)
Branch: `engine` (worktree at `/Users/davidcase/Source/1sat/1sat-engine/`)

## Goal

Replace the custom `Store` interface with the Redis protocol as the canonical data interface. Modules receive bidirectional byte streams (channels) carrying RESP protocol. Backend is redcon + Badger in-process.

This is the foundation for the distributed compute vision (see `docs/research/distributed-compute-vision.md`).

## Dual Channel Model

Modules receive named storage channels — bidirectional byte streams:

- **Redis channel** — RESP protocol. KV, hashes, sorted sets, pub/sub. Hot-path operational state.
- **SQLite channel** — SQL protocol. Structured queries, aggregates, joins, text search. Relational data.

A module doesn't receive a Go client or a TCP address. It receives a stream. The module wraps the stream with whatever RESP/SQL client its language provides. The host provides the other end of the stream, backed by whatever storage (Badger, real Redis, SQLite file, IndexedDB, etc.).

The stream abstraction must map naturally to Go, Zig, TypeScript/AssemblyScript, and any other WASM-targeting language. It's just bytes in, bytes out.

## Migration Progress

### Phase 0: Foundation (Complete)

| Task | Status | Linear | Notes |
|------|--------|--------|-------|
| Research redcon + Badger | ✅ Done | OPL-1524 | Spike validated, go-redis connects, commands work |
| Audit Store interface usage | ✅ Done | OPL-1525 | 18 of 25 methods used, 63 calls, command matrix built |
| Map Store to Redis commands | ✅ Done | OPL-1507 | All methods map cleanly to Redis commands |
| Redcon RESP server | ✅ Done | OPL-1506 | `pkg/store/redcon.go` — full command handler coverage |

### Phase 1: Channel Abstraction (Next)

Design spec: `docs/research/channel-spec.md`

Multiple data channels multiplexed over WASI stdin/stdout with lightweight framing (`[channel_id][length][payload]`). Each channel carries RESP or SQL bytes. Modules see independent connections via per-language adapters. Function calls (module contract) stay as normal WASM imports/exports. stderr reserved for diagnostics.

| Task | Status | Linear | Notes |
|------|--------|--------|-------|
| Channel spec | **Draft** | OPL-1526 | `docs/research/channel-spec.md` — framing, mux, adapters, WASI compat |
| Build mux + Go adapter | **Not Started** | OPL-1526 | Framing protocol, `net.Conn` adapter for go-redis |
| Migrate modules to channels | **Not Started** | OPL-1527 | Modules receive channels, create own clients internally |
| Remove `Store` interface | **Not Started** | OPL-1508 | After all modules migrated to channels |

**IMPORTANT**: Previous work (commits `93ec622`..`271d5e3`) swapped `store.Store` → `*redis.Client` across 12 packages. This was the **wrong approach** — it just replaced one Go-specific dependency with another. Modules must receive channels (byte streams), not injected clients. That work needs to be redone once the channel abstraction exists.

### Phase 2: Engine Storage Redesign

Move `EngineAdapter`/`TopicStorage` from native SQLite back to Redis channel.

| Task | Status | Linear | Notes |
|------|--------|--------|-------|
| Pull `SaveEvent`/`DeleteEvent`/`FindByEvent` out of `TopicStorage` | Not Started | OPL-1529 | Move to lookup layer — these serve lookups, not the engine |
| Reimplement engine output ops on Redis channel | Not Started | OPL-1530 | Follow pattern from deleted `engine_storage.go` (commit `71570c9`) |
| Update `EngineAdapter` to route to per-topic Redis channels | Not Started | | Replace per-topic SQLite factory with per-topic channel routing |
| Remove `pkg/overlay/storage/sqlite.go` engine tables | Not Started | | Keep `outputs` table removal separate from lookup schema |

### Phase 3: Overlay Lookup Services

Lookups stay on SQLite channel. Engine storage (Phase 2) and lookup storage are now cleanly separated.

| Overlay | Lookup Storage | Channel | Notes |
|---------|---------------|---------|-------|
| OPNS | TopicStorage events | Redis | Already event-shaped, no relational queries |
| OrdLock | Custom in-memory struct | Redis | Minimal storage |
| BAP | 3 tables, joins, text search | SQLite | Relational queries required |
| BSV21 | `token_outputs` table (per-token DB via `DB()` escape hatch) | SQLite | Balance aggregates, address queries, history |
| BSocial | MongoDB (aggregation pipelines) | TBD | Needs migration — evaluate Redis vs SQLite |

## Key Decisions

1. **Channels are bidirectional byte streams** — not Go interfaces, not TCP addresses, not injected clients. A stream that carries RESP or SQL bytes. The module wraps it with whatever client library its language provides.
2. **Channel abstraction must be language-agnostic** — must map naturally to Go, Zig, TypeScript, AssemblyScript, and any WASM-targeting language. Just bytes in, bytes out.
3. **Modules create their own clients from the stream** — the host provides the channel, the module wraps it. Module is self-contained.
4. **Multiple named channels per module** — a module might receive `txo` (redis) and `bap_db` (sqlite). Each is a separate stream.
5. **Host manages isolation** — which channels a module can access is configuration, not module choice.
6. **Redis protocol is the data interface** — not a Go abstraction. RESP bytes are the contract.
7. **SQLite as second channel type** — for relational data (BAP, BSV21) where Redis primitives are insufficient.
8. **Engine storage moves back to Redis channel** — `EngineAdapter`/`TopicStorage` engine ops are single-table CRUD that maps to Redis hashes and sorted sets.
9. **Event methods belong in lookup layer, not engine storage** — `SaveEvent`/`DeleteEvent`/`FindByEvent` serve lookups, not the engine. Pull them out.
10. **BSV21 demonstrates both channels** — queue ops on Redis channel, token_outputs on SQLite channel, joined in application code.

## Store Interface Audit Summary

18 of 25 Store methods actually used, 63 total calls across codebase.
7 unused methods: SAdd, SMembers, SRem, SIsMember, HMSet, ZRevRange, ZIncrBy, ZSum, ZKeys.
No transactions, no pipelines, no conditional operations.
Heavy sorted set usage (31 calls across 8 packages).
Hash operations concentrated in txo (19 of 23 total).

# 1sat-engine: Redis Protocol Migration

Status: **In Progress**

Linear Project: **1sat-engine** (OPL)
Branch: `engine` (worktree at `/Users/davidcase/Source/1sat/1sat-engine/`)

## Goal

Replace the custom `Store` interface with the Redis protocol as the canonical data interface. Modules receive bidirectional byte streams (channels) carrying RESP protocol. Backend is redcon + Badger in-process.

This is the foundation for the distributed compute vision (see `docs/research/distributed-compute-vision.md`).

## Dual Storage Model

Modules have two storage mechanisms:

- **Redis channel** — RESP protocol over multiplexed stdin/stdout stream. KV, hashes, sorted sets, pub/sub. Hot-path operational state. Module wraps the stream with whatever RESP client its language provides.
- **SQLite file** — Native file I/O. Structured queries, aggregates, joins. Used by lookup services only. Module uses SQLite directly. Host handles replication/redundancy (Litestream, LiteFS, etc.) transparently.

The Redis channel is the stream abstraction — bidirectional bytes, language-agnostic, maps naturally to Go, Zig, TypeScript/AssemblyScript. SQLite is orthogonal — it's just a file, not a stream.

## Data Serialization

Structured data crossing WASM module boundaries uses **Protocol Buffers**. Static codegen for Go, Zig, TypeScript/AssemblyScript. No runtime reflection.

Key protobuf types: `ParsedBeef` (pre-deserialized, txids computed), `AdmittanceInstructions`, `LookupQuestion/Answer`, `OutputData`.

The engine parses raw BEEF bytes once, serializes to `ParsedBeef` protobuf, passes that across module boundaries. Topic managers skip the expensive rehashing.

See `docs/research/channel-spec.md` for full details on channels, serialization, and WASM architecture.

## Migration Progress

### Phase 0: Foundation (Complete)

| Task | Status | Linear | Notes |
|------|--------|--------|-------|
| Research redcon + Badger | ✅ Done | OPL-1524 | Spike validated, go-redis connects, commands work |
| Audit Store interface usage | ✅ Done | OPL-1525 | 18 of 25 methods used, 63 calls, command matrix built |
| Map Store to Redis commands | ✅ Done | OPL-1507 | All methods map cleanly to Redis commands |
| Redcon RESP server | ✅ Done | OPL-1506 | `pkg/store/redcon.go` — full command handler coverage |

### Phase 1: Channel Abstraction & Module Contract (Next)

Design spec: `docs/research/channel-spec.md`

RESP channels multiplexed over WASI stdin/stdout with lightweight framing (`[channel_id][length][payload]`). Modules see independent Redis connections via per-language adapters. Function calls (module contract) stay as normal WASM imports/exports. stderr reserved for diagnostics. SQLite is file I/O, not a channel.

| Task | Status | Linear | Notes |
|------|--------|--------|-------|
| Channel spec | **Draft** | OPL-1526 | `docs/research/channel-spec.md` — framing, mux, adapters, WASI compat |
| Define `.proto` types | **Not Started** | | ParsedBeef, AdmittanceInstructions, LookupQuestion/Answer, OutputData |
| Define module contract (exported functions) | **Not Started** | OPL-1510 | `admit(ParsedBeef) → AdmittanceInstructions`, `lookup(LookupQuestion) → LookupAnswer` |
| Build mux + Go adapter | **Not Started** | OPL-1526 | Framing protocol, `net.Conn` adapter for go-redis |
| Build Go shim (WasmTopicManager, WasmLookupService) | **Not Started** | | Implements Go interfaces, calls WASM modules via protobuf |
| Migrate modules to channels | **Not Started** | OPL-1527 | Modules receive channels, create own clients internally |
| Remove `Store` interface | **Not Started** | OPL-1508 | After all modules migrated to channels |

**IMPORTANT**: Previous work (commits `93ec622`..`271d5e3`) swapped `store.Store` → `*redis.Client` across 10 packages. This was the **wrong approach** — it just replaced one Go-specific dependency with another. Modules must receive channels (byte streams), not injected clients. That work needs to be redone once the channel abstraction exists.

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

1. **Redis channel is a bidirectional byte stream** — RESP bytes multiplexed over stdin/stdout. Module wraps it with whatever RESP client its language provides. Not Go interfaces, not TCP addresses, not injected clients.
2. **SQLite is a file, not a channel** — Lookup services use SQLite natively. Host handles replication (Litestream, LiteFS, custom VFS). No SQL wire protocol over the mux.
3. **Channel abstraction is language-agnostic** — maps naturally to Go, Zig, TypeScript, AssemblyScript. Just bytes in, bytes out.
4. **Modules create their own clients from the stream** — host provides the channel, module wraps it. Module is self-contained.
5. **Host manages isolation** — which channels/files a module can access is configuration, not module choice.
6. **Protobuf for module boundary data** — structured data crossing WASM boundaries uses protobuf. Static codegen, no runtime reflection. `.proto` files define the module contract.
7. **Engine targets WASM** — pure decision logic. Host owns concurrency and I/O. Engine module calls topic managers via host-mediated `call_module()`.
8. **Go shim for host integration** — implements Go interfaces (`TopicManager`, `LookupService`, `Storage`), internally calls WASM modules via protobuf. Native Go modules keep working alongside WASM modules.
9. **Function calls are WASM imports/exports** — module contract functions (`admit`, `lookup`, `parse`) are direct WASM calls with protobuf bytes. Data channels are for storage access.
10. **Engine storage moves back to Redis channel** — `EngineAdapter`/`TopicStorage` engine ops are single-table CRUD that maps to Redis hashes and sorted sets.
11. **Event methods belong in lookup layer, not engine storage** — `SaveEvent`/`DeleteEvent`/`FindByEvent` serve lookups, not the engine. Pull them out.
12. **BSV21 demonstrates both storage types** — queue ops on Redis channel, token_outputs on SQLite file, joined in application code.

## Store Interface Audit Summary

18 of 25 Store methods actually used, 63 total calls across codebase.
7 unused methods: SAdd, SMembers, SRem, SIsMember, HMSet, ZRevRange, ZIncrBy, ZSum, ZKeys.
No transactions, no pipelines, no conditional operations.
Heavy sorted set usage (31 calls across 8 packages).
Hash operations concentrated in txo (19 of 23 total).

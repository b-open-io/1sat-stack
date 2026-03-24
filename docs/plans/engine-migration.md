# 1sat-engine: Module Runtime Migration

Status: **In Progress**

Linear Project: **1sat-engine** (OPL)
Branch: `engine` (worktree at `/Users/davidcase/Source/1sat/1sat-engine/`)

## Goal

Transform 1sat-stack from a Go monolith into a **module runtime** — a thin orchestrator that loads WASM modules, resolves typed channel providers, and wires everything together. Modules are WASM binaries written in Zig. Channels carry protobuf-defined protocols. The same modules run on servers, desktops, and browsers.

See `docs/research/module-runtime-architecture.md` for the full architectural vision.

## Architecture Summary

- **Channels** carry protobuf-defined protocols (not RESP). Each channel type has a `.proto` service definition.
- **SQLite** is file I/O for lookup services, not a channel. Host handles replication.
- **Protobuf** for all data crossing module boundaries. Static codegen, no runtime reflection.
- **On-chain interface contracts** — proto files inscribed as outpoints for immutable, universally addressable protocol definitions.
- **Identity** — all peer communication authenticated through wallet root identity key (= libp2p peer ID).

See `docs/research/module-runtime-architecture.md` for the full vision including channel types, runtime model, provider resolution, and on-chain contracts.

## Migration Progress

### Phase 0: Foundation (Complete)

| Task | Status | Linear | Notes |
|------|--------|--------|-------|
| Research redcon + Badger | ✅ Done | OPL-1524 | Spike validated, go-redis connects, commands work |
| Audit Store interface usage | ✅ Done | OPL-1525 | 18 of 25 methods used, 63 calls, command matrix built |
| Map Store to Redis commands | ✅ Done | OPL-1507 | All methods map cleanly to Redis commands |
| Redcon RESP server | ✅ Done | OPL-1506 | `pkg/store/redcon.go` — full command handler coverage |

### Phase 1: Channel Abstraction & Module Contract

Design spec: `docs/research/channel-spec.md`

RESP channels multiplexed over WASI stdin/stdout with lightweight framing (`[channel_id][length][payload]`). Modules see independent Redis connections via per-language adapters. Function calls (module contract) stay as normal WASM imports/exports. stderr reserved for diagnostics. SQLite is file I/O, not a channel.

| Task | Status | Linear | Notes |
|------|--------|--------|-------|
| Channel spec | **Draft** | OPL-1526 | `docs/research/channel-spec.md` — framing, mux, adapters, WASI compat |
| Build mux + Go adapter | ✅ Done | OPL-1526 | `pkg/channel/` — frame.go, mux.go, conn.go. Tests pass against redcon. |
| Define `.proto` types | **Not Started** | OPL-1552 | ParsedBeef, AdmittanceInstructions, LookupQuestion/Answer, OutputData |
| Define module contract (exported functions) | **Not Started** | OPL-1510 | `admit(ParsedBeef) → AdmittanceInstructions`, `lookup(LookupQuestion) → LookupAnswer` |
| Build Wazero host scaffold | **Not Started** | OPL-1509 | Load .wasm, call exports, pass protobuf bytes |
| Build Go shim (WasmTopicManager, WasmLookupService) | **Not Started** | OPL-1553 | Implements Go interfaces, calls WASM modules via protobuf |
| Wire parse WASM module into Go ingestion flow | **Not Started** | | Replace `ParseTxn()` with WASM module call |
| Remove `Store` interface | **Not Started** | OPL-1508 | After all modules migrated to channels |

**Note**: Previous work (commits `93ec622`..`271d5e3`) swapped `store.Store` → `*redis.Client` across 10 packages. This was the wrong approach — modules must receive channels (byte streams), not injected clients. Channel mux is now built; modules need to be rewired to use it once WASM host is in place.

### Phase 1b: Zig WASM Modules

WASM modules written in Zig, compiled to .wasm, loaded by Wazero host in Go. Parsers are libraries statically compiled into each module that needs them.

| Task | Status | Linear | Notes |
|------|--------|--------|-------|
| bsvz ScriptIterator | ✅ Done | | Extracted from parseAlloc, committed to bsvz |
| Parse module scaffolding | ✅ Done | | `zig/src/parse/` — context, pipeline, 1sat/P2PKH built-in |
| Cosign parser | ✅ Done | | 7-chunk sliding window, bsvz ScriptIterator |
| Lock parser | ✅ Done | | Full LockPrefix + LockSuffix from go-templates |
| Inscription parser | ✅ Done | | OP_FALSE OP_IF "ord" envelope, field-value pairs |
| Bitcom family (base, B, MAP, AIP, BAP, SIGMA) | ✅ Done | | OP_RETURN pipe-splitting, per-protocol sub-parsers |
| OPNS parser | ✅ Done | | Full contract prefix via @embedFile, genesis validation |
| OrdLock parser | ✅ Done | | Full OrdLockPrefix + OrdLockSuffix |
| Shrug parser | ✅ Done | | Tag + outpoint + OP_2DROP + amount + OP_DROP |
| BSV21 parser | **Not Started** | | Needs JSON parsing (inscription content is JSON) |
| `parseBeefBytes` transaction-level function | ✅ Done | | Takes raw BEEF bytes, uses bsvz to deserialize, runs all parsers on outputs+spends, returns BeefParseResult |
| WASM build target | ✅ Done | OPL-1511 | 70KB `parse_tx.wasm` with protobuf — exports: alloc, dealloc, parse_beef. Separate wasm_exports.zig root for WASM build. |
| Protobuf wire format | ✅ Done | OPL-1552 | `proto/parse.proto` defines OutPoint, IndexedOutput, BeefParseResult. Generated: Zig (`parse_pb.zig`), Go (`proto/parsepb/parse.pb.go`). WASM exports encode as protobuf. |

### Phase 2: Wazero Host + WASM Integration

Same `.wasm` binary loaded by Go (Wazero) and TypeScript (WebAssembly API). Same protobuf wire format decoded by standard protobuf libraries in each language.

| Task | Status | Linear | Notes |
|------|--------|--------|-------|
| Add Wazero dependency to Go | **Not Started** | OPL-1509 | Load `parse_tx.wasm`, instantiate, call exports |
| Go-side parse_beef caller | **Not Started** | | Call alloc → write BEEF → call parse_beef → read protobuf → decode with parsepb |
| Wire into Go ingestion flow | **Not Started** | | Replace `IndexContext.ParseTxn()` with WASM module call |
| TypeScript WASM loader | **Not Started** | | `WebAssembly.instantiate` for 1sat-sdk. Same .wasm, protobufjs for decoding |
| Replace 1sat-sdk TypeScript parsers | **Not Started** | | Unify parsing: both Go and TS consume same WASM binary |
| Topic manager WASM interface | **Not Started** | | `admit(beef_ptr, len) → instructions_ptr` export. OPNS or OrdLock first. |
| Topic manager Zig implementation | **Not Started** | | Statically links parser library. OPNS mine detection as first candidate. |

### Phase 3: Engine Storage Redesign

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

1. **Each channel type is a protobuf service definition** — not RESP, not a Go interface. Strongly typed request/response. RESP is superseded.
2. **SQLite is a file, not a channel** — lookup services use SQLite natively. Host handles replication.
3. **Protobuf for everything** — channel payloads, function arguments, inter-module data. Static codegen. `.proto` files define the module contract.
4. **On-chain interface contracts** — proto files can be inscribed as outpoints. Immutable, addressable, verifiable.
5. **Module runtime replaces monolith** — 1sat-stack becomes a thin orchestrator. Business logic lives in WASM modules.
6. **Zig for WASM modules, Go for the runtime** — no Go-to-WASM via TinyGo. Zig compiles to small, efficient WASM binaries.
7. **Channels are provider-agnostic** — a module's beef channel could be backed by local Badger, a REST API, a libp2p peer, or a fallback chain. Module doesn't know.
8. **Identity is the root** — wallet identity key = libp2p peer ID. All peer communication is authenticated through it.
9. **HTTP and P2P are transport adapters** — same channel protocol, same auth, different wire transport.
10. **Parser libraries are statically compiled** — same Zig parser source gets compiled into standalone parse module AND into topic manager modules. No shared libraries at runtime.
11. **Event methods belong in lookup layer, not engine storage** — `SaveEvent`/`DeleteEvent`/`FindByEvent` serve lookups, not the engine.

## Store Interface Audit Summary

18 of 25 Store methods actually used, 63 total calls across codebase.
7 unused methods: SAdd, SMembers, SRem, SIsMember, HMSet, ZRevRange, ZIncrBy, ZSum, ZKeys.
No transactions, no pipelines, no conditional operations.
Heavy sorted set usage (31 calls across 8 packages).
Hash operations concentrated in txo (19 of 23 total).

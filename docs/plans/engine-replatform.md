# 1sat-engine: Replatform to TypeScript + Zig

Status: **Not Started**

## Direction Change

The original engine-migration plan focused on porting Go business logic to Zig WASM modules hosted by a Go runtime (Wazero). Research into runtime environments revealed a better path:

**1sat-engine becomes a TypeScript package with Zig native modules.** It replaces both the Go server and the Go sidecar in ElectroBun. The same package serves two deployment modes:
- **Embedded** — loaded directly in the ElectroBun Bun backend, no sidecar process
- **Server** — standalone with an HTTP API layer on top

Browser and frontend code gets WASM builds of pure-function modules (parser, basket routing) via the standard WebAssembly API.

## Architecture

```
1sat-engine (TypeScript package)
├── TypeScript runtime layer
│   ├── Module lifecycle and configuration
│   ├── HTTP API (for server mode)
│   ├── Storage backends (SQLite via bun:sqlite, filesystem)
│   ├── Network clients (JungleBus, ARC, ORDFS, chain tracker)
│   └── Wallet integration (imports @bsv/wallet-toolbox, @1sat/wallet)
├── Zig native modules (NAPI addons)
│   ├── Parser (already built — 14 protocol parsers)
│   ├── Topic managers (admission logic per overlay)
│   ├── Lookup services (query logic with SQLite access)
│   ├── Overlay engine (admission, GASP sync, proof tracking)
│   ├── ORDFS (ordinal content resolution, spend chain traversal)
│   └── Beef assembly (ancestor walking, proof merging)
└── WASM builds (for browser/frontend)
    └── Pure-function modules only (parser, basket routing)
```

### Zig Integration

Zig modules compile to native NAPI addons (.node files) for the Bun runtime. They use `napi_create_async_work` to run blocking operations on the libuv thread pool. The TypeScript layer calls them as normal async functions that return Promises.

For browser use, the same Zig source compiles to WASM and runs via the standard WebAssembly API. Only pure-function modules (no host callbacks needed) are available in this mode.

### Relationship to Existing Projects

| Project | Role |
|---------|------|
| 1sat-engine (this) | Core overlay services package — TS + Zig |
| 1sat-sdk | Wallet SDK, actions, client libraries — consumes 1sat-engine |
| wallet-desktop | ElectroBun app — embeds 1sat-engine in Bun backend |
| 1sat-stack (Go) | Current production server — continues running, not actively ported |

The Go server stays as-is for production. New development focuses on 1sat-engine. Over time, the Go server's role shrinks as 1sat-engine covers more functionality.

## What Carries Forward

From the Zig/WASM work already done:
- 14 Zig parsers in zig/src/parse/
- Proto definitions (beef.proto, parse.proto, per-parser protos)
- BeefParseResult protobuf format
- AtomicBeef canonical transaction representation
- WASM binary for browser/Go consumption

From the Go server (reference for porting):
- Overlay engine architecture (topic managers, lookup services, GASP)
- Beef storage with multi-tier fallback
- ORDFS content resolution and spend chain traversal
- BSV21 token pipeline
- Address sync and internalization
- Admin configuration model

From the TS SDK (direct dependencies):
- @bsv/wallet-toolbox — BRC-100 wallet implementation
- @1sat/wallet-node — Node/Bun wallet with SQLite
- @1sat/actions — wallet actions (ordinals, tokens, OPNS, locks)
- @1sat/client — API clients for 1sat services
- @1sat/templates — script templates (to be replaced by Zig parsers)

## What Needs Planning

- Package structure within 1sat-sdk monorepo or standalone repo
- Module configuration (which overlays are enabled, JungleBus config)
- How the wallet-desktop Bun backend evolves to use 1sat-engine
- NAPI addon build system (napigen, cross-compilation, npm distribution)
- First module to port (beef storage? ORDFS? a topic manager?)
- Testing strategy for Zig NAPI addons in Bun

## References

- docs/research/runtime-strategy.md — three-target compile strategy decision
- docs/research/wasm-architecture-audit.md — Go/TS codebase audit
- docs/research/module-runtime-architecture.md — original module runtime vision
- docs/plans/engine-migration.md — original migration plan (partially superseded)

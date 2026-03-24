# Runtime Strategy

Status: **Decided**

## Decision

All business logic modules are written in Zig. Three compile targets serve three runtime environments from the same source code.

### Runtime Environments

| Environment | Compile Target | Host Runtime | Capabilities |
|-------------|---------------|-------------|-------------|
| Browser / standard JS | wasm32-wasi | WebAssembly API | Pure functions only (parser, basket routing, protocol assignment). No host callbacks. |
| Go server | wasm32-wasi | Wazero | Full stack. WASM modules with synchronous host functions for storage, network, overlay engine. |
| ElectroBun desktop | native (per-platform) | NAPI addon in Bun | Full stack. Zig compiled natively, loaded as .node addon. Blocking I/O on NAPI thread pool. |

### Why Three Targets

**Browser JS** cannot do async I/O inside WASM host functions (JavaScriptCore limitation, no JSPI). Limited to pure-function modules that take data in and return data out.

**Go/Wazero** handles blocking host functions naturally via goroutines. Full overlay stack runs as WASM modules. This is the production server path.

**ElectroBun/Bun** needs full overlay stack running in-process (the user's node on the overlay network). Standard WASM imports in Bun can't do async I/O. Native Zig compiled as a NAPI addon uses `napi_create_async_work` to run on the thread pool where blocking I/O works. No WASM indirection needed — same Zig source, compiled natively.

### Compile Targets for Native Builds

Zig cross-compiles all targets from a single machine:
- `aarch64-macos` — Mac Apple Silicon
- `x86_64-macos` — Mac Intel
- `x86_64-linux-gnu` — Linux x64
- `aarch64-linux-gnu` — Linux ARM64
- `x86_64-windows` — Windows x64

Per-platform `.node` files distributed as npm optional dependencies (same pattern as NAPI-RS projects like SWC, Prisma).

### Interface Pattern for Zig Modules

Each service dependency (BeefStore, OverlayStorage, LookupDb) is a Zig interface — a struct with function pointers or method dispatch. The consuming module code is identical across all targets:

```
const store = BeefStore.init(allocator);
const beef = store.get(txid);
```

At compile time, the implementation differs:
- **WASM target** — methods route through `extern "env"` host function imports
- **Native target** — methods call the storage backend directly (file I/O, SQLite, network)

The business logic module never knows which target it's compiled for.

### NAPI Addon Approach (ElectroBun)

The Zig NAPI addon uses napigen or raw NAPI C headers. Key mechanics:

1. Bun loads the `.node` addon
2. JS calls an exported function
3. Zig creates a Promise via `napi_create_promise`
4. `napi_create_async_work` schedules blocking work on the thread pool
5. Thread pool thread: Zig runs the module logic with blocking I/O (file, SQLite, network)
6. Complete callback resolves the Promise on the main thread
7. JS gets the result

Thread pool threads can safely do all blocking I/O. The main Bun event loop is never blocked.

### What This Replaces

Currently the ElectroBun wallet spawns the Go server as a sidecar child process and communicates over HTTP on localhost. This works but means two processes, two runtimes, and HTTP overhead for local calls.

With the native Zig NAPI addon, the overlay stack runs in-process in the Bun backend. No sidecar, no HTTP for local operations. The wallet and overlay engine share memory.

### Future Direction

As business logic migrates from Go to Zig, the Go server becomes thinner. Eventually the Go server could be replaced by a native Zig application. The HTTP API, config management, and process lifecycle that Go currently handles can all be implemented in Zig. This is not an immediate goal — Go remains the production server.

## Research That Led Here

### Options Evaluated

| Option | Verdict | Reason |
|--------|---------|--------|
| Standard WASM API in Bun | Works for pure functions | Can't do async in host imports |
| JSPI (Promise Integration) | Chrome-only | JavaScriptCore (Bun) doesn't support it |
| Bun FFI (bun:ffi) | No async, experimental | Blocks event loop, Bun's own docs warn against production use |
| NAPI-RS + Wasmtime (Rust) | Viable but adds Rust | Same result as pure Zig NAPI, extra language in toolchain |
| Go c-shared + Bun | Fragile | Signal handling conflicts, GC fights, 10-20MB runtime overhead |
| Zig NAPI addon (native) | Selected | Pure Zig, async via thread pool, ~1MB, no extra languages |
| Zig + Wasmtime C API | Viable alternative | Keeps WASM portability but adds Wasmtime dependency (~2MB) |
| Extism JS SDK | Same limitation | Uses standard WASM API under the hood |
| Wasmer/Wasmtime npm | No async host functions | JS bindings don't expose async capabilities |

### Key Findings

- Bun implements ~95% of Node-API (NAPI). NAPI addons work in Bun.
- `napi_create_async_work` runs on libuv thread pool. Blocking I/O is safe there.
- napigen provides comptime NAPI bindings for Zig. No node-gyp needed.
- Zig cross-compiles to all desktop platforms from a single machine.
- ElectroBun's backend is a standard Bun process. No special plugin API beyond NAPI/FFI.
- Wasmtime C API has async host function support, but native Zig eliminates the need for WASM on the client entirely.

## References

- docs/research/module-runtime-architecture.md — full module/channel vision (still valid conceptually, channel transport details superseded by this decision)
- docs/research/wasm-architecture-audit.md — Go/TS audit that identified the divergences
- docs/research/channel-spec.md — mux framing (superseded for client, still relevant for WASM host functions in Go/Wazero)

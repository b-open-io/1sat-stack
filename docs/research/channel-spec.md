# Channel Specification

Status: **Draft**

## Overview

Modules communicate with host storage backends through multiplexed data channels over WASI stdin/stdout. Each channel carries a specific protocol (RESP for Redis, SQL wire format for SQLite). Modules see what looks like independent connections; the host demultiplexes and routes to the appropriate backends.

stderr is reserved for module diagnostics and logging.

## Two Interaction Types

A module interacts with the host in two distinct ways:

### 1. Function Calls (WASM imports/exports)

Standard WASM function calls. Synchronous, typed, direct.

Used for the module contract:
- **Module exports** (host calls module): `encode()`, `decode()`, `parse()`, `admit()`, `lookup()`
- **Module imports** (module calls host): `get_raw_tx()`, `get_proof()`, `chain_height()`, `publish_event()`

These are normal WASM mechanics — no special infrastructure needed.

### 2. Data Channels (multiplexed on stdin/stdout)

Bidirectional byte streams carrying storage protocols (RESP, SQL). Multiplexed over stdin/stdout with a lightweight framing protocol.

Used for storage access:
- Redis commands (GET, SET, HSET, ZADD, etc.)
- SQLite queries
- PubSub subscriptions and messages

Each channel appears as an independent connection to the module's client libraries. A Go module wraps a channel in a `net.Conn` and passes it to go-redis. A Zig module wraps it in a reader/writer and passes it to whatever RESP library it uses. The client library never knows it's going through a mux.

## Framing Protocol

Every message on stdin/stdout is framed:

```
[4 bytes: channel_id, big-endian uint32]
[4 bytes: payload_length, big-endian uint32]
[N bytes: payload]
```

- **channel_id**: Identifies which logical channel this message belongs to. Assigned by the host when channels are provisioned to the module.
- **payload_length**: Length of the payload in bytes.
- **payload**: Raw protocol bytes (RESP-encoded Redis command/response, or SQL wire bytes).

8 bytes overhead per message. No compression, no encryption — the host and module are in the same process.

## Channel Lifecycle

1. **Provisioning**: Before instantiating the module, the host decides which channels the module receives. This is configuration — e.g., "BSV21 engine module gets channels `txo` (redis) and `bsv21:{tokenId}` (sqlite)."

2. **Channel table**: The host passes the channel table to the module at initialization (via a function call or a well-known memory location). The table maps channel IDs to names and types:

```
channel_id=1, name="txo", type="redis"
channel_id=2, name="bsv21:abc123_0", type="sqlite"
```

3. **Communication**: Module sends framed messages on stdout. Host reads, demuxes by channel_id, routes payload to the appropriate backend, gets response, frames response with same channel_id, writes to module's stdin.

4. **Teardown**: Host closes stdin. Module sees EOF and shuts down.

## Channel Types

### Redis Channel

Payload is RESP2-encoded bytes. Identical to what goes over the wire to a real Redis server.

Module sends:
```
*3\r\n$3\r\nSET\r\n$3\r\nfoo\r\n$3\r\nbar\r\n
```

Host responds:
```
+OK\r\n
```

The module's Redis client library handles RESP encoding/decoding. The channel is just the transport.

### SQLite Channel

**Decision: SQLite is not a channel type.** Lookup services use SQLite as a native file, not as a wire protocol over the mux. The module reads/writes a SQLite file directly. The host manages the file lifecycle and handles redundancy/replication (via Litestream to S3, LiteFS, custom VFS, or other mechanisms). The module doesn't know or care how replication works.

This means the mux only carries RESP. SQLite is orthogonal — it's file I/O, not a stream.

For WASM modules, the SQLite file is accessed through WASI filesystem APIs (`fd_open`, `fd_read`, `fd_write`). SQLite compiles to WASM natively (sql.js, etc.), so modules can carry their own SQLite instance.

Available SQLite replication backends (host concern, transparent to module):
- **Litestream** — streams WAL to S3/GCS/Azure Blob
- **LiteFS** — FUSE-based distributed SQLite
- **Turso/libSQL** — SQLite fork with built-in replication
- **rqlite/dqlite** — distributed SQLite over Raft
- **cr-sqlite** — CRDT-based conflict-free replication
- **Custom VFS** — SQLite's VFS layer is pluggable

## PubSub

PubSub works through the same channel mux. The module sends a RESP `SUBSCRIBE` command on a Redis channel. The host subscribes internally and pushes messages back on that channel_id as they arrive.

From the module's perspective, it issued a `SUBSCRIBE` and the channel is now delivering messages — same as a real Redis connection. The mux framing handles interleaving PubSub messages with request/response traffic from other channels.

The host manages the actual subscription lifecycle and bridges to external transports (SSE, P2P, WebSocket).

## I/O Paths

```
Module                              Host
  │                                   │
  │ stdout ──[framed messages]──►     │  demux by channel_id
  │                                   │  route to backend (Badger, SQLite, Redis)
  │                                   │  execute command
  │     ◄──[framed responses]── stdin │  mux response with channel_id
  │                                   │
  │ stderr ──[diagnostics]──►         │  captured for logging
  │                                   │
```

## Per-Language Adapters

Each language needs a thin adapter that:
1. Reads/writes the framing protocol on stdin/stdout
2. Demuxes incoming frames to the correct virtual connection
3. Presents each virtual connection as whatever stream type the language's Redis/SQLite client expects

### Go Adapter

Wraps each channel as a `net.Conn`. go-redis connects via custom `Dialer`:

```go
func ChannelConn(mux *Mux, channelID uint32) net.Conn {
    // returns a net.Conn whose Read/Write go through the mux
}

client := redis.NewClient(&redis.Options{
    Dialer: func(ctx context.Context, _, _ string) (net.Conn, error) {
        return ChannelConn(mux, channelID), nil
    },
})
```

### Zig Adapter

Wraps each channel as a reader/writer pair using Zig's `std.io` interfaces. Reads from stdin, filters by channel_id, delivers payload bytes to the correct channel's reader.

### Other Languages

Same pattern. The framing protocol is simple enough (8-byte header + payload) that implementing it in any language is trivial.

## In-Process Go Shortcut

For native Go modules (not compiled to WASM), the mux can be backed by `io.Pipe()` pairs instead of actual stdin/stdout. Same framing, same adapter code, but no WASM boundary. This is how existing 1sat-stack packages will work during migration — they receive channels, not `*redis.Client`.

## Host-Side Architecture

```
                    ┌─────────────┐
                    │   Module    │
                    │  stdin/out  │
                    └──────┬──────┘
                           │
                    ┌──────┴──────┐
                    │     Mux     │
                    └──┬───┬───┬──┘
                       │   │   │
              ┌────────┘   │   └────────┐
              ▼            ▼            ▼
        ┌──────────┐ ┌──────────┐ ┌──────────┐
        │ channel 1│ │ channel 2│ │ channel 3│
        │  "txo"   │ │ "bap_db" │ │ "topics" │
        │  (redis) │ │ (sqlite) │ │  (redis) │
        └────┬─────┘ └────┬─────┘ └────┬─────┘
             │             │             │
             ▼             ▼             ▼
        ┌─────────┐  ┌─────────┐  ┌─────────┐
        │ Badger   │  │ SQLite  │  │ Badger   │
        │ (redcon) │  │  file   │  │ (redcon) │
        └─────────┘  └─────────┘  └─────────┘
```

## WASI Compatibility

This design uses only WASI Preview 1 primitives:
- `fd_read` (stdin, fd 0)
- `fd_write` (stdout, fd 1 / stderr, fd 2)

No resource handles, no Preview 2, no component model required. Works on Wazero, Wasmtime, Wasmer, and any other WASI Preview 1 runtime. When Preview 2 resource handles become available across runtimes, channels could migrate to native stream resources — but the framing protocol and adapter pattern would stay the same.

## Module Contract & Serialization

Data crossing the WASM boundary (function call arguments and return values) uses **Protocol Buffers**. Protobuf provides:
- Schema-defined types with static codegen for Go, Zig, TypeScript/AssemblyScript
- No runtime reflection or dynamic parsing inside WASM
- Compact binary format
- Schema evolution via field numbering

The `.proto` files define the module contract alongside the exported function signatures.

### Key protobuf types needed:

- **ParsedBeef** — pre-deserialized BEEF with txids already computed (avoids rehashing per module)
- **AdmittanceInstructions** — topic manager output
- **LookupQuestion / LookupAnswer** — lookup service interface
- **OutputData** — outpoint metadata for engine storage operations

The engine parses raw BEEF bytes once, serializes to `ParsedBeef` protobuf, then passes that across module boundaries. Topic managers skip the expensive hashing.

### Why protobuf, not WIT/Component Model:

WIT is the standards-track future for WASM interfaces but Zig support isn't there yet (no `wit-bindgen` backend). Protobuf works across all target languages today. When WIT matures, the boundary layer can migrate without changing the data formats.

**Karmem** (github.com/inkeliz/karmem) is a potential alternative — specifically optimized for WASM, supports Zig/Go/AssemblyScript, claims better performance than FlatBuffers. Worth evaluating against protobuf.

## WASM Module Architecture

### The engine is also a WASM module

The overlay engine targets WASM — it's pure decision logic (receive parsed BEEF, run through topic pipeline, return results). The host owns concurrency and I/O. The engine module:

- Receives `ParsedBeef` via exported functions
- Uses its Redis channel for output membership state
- Calls topic managers through host-mediated function calls: `call_module(module_id, function_name, protobuf_bytes)`
- Returns `AdmittanceInstructions` as protobuf bytes

### Go shim for host integration

The current Go engine expects Go interfaces (`TopicManager`, `LookupService`, `Storage`). A thin shim implements these Go interfaces, internally calling into WASM modules:

```go
type WasmTopicManager struct {
    runtime WasmRuntime
    module  WasmModule
}

func (w *WasmTopicManager) IdentifyAdmissibleOutputs(beef *ParsedBeef) (*AdmittanceInstructions, error) {
    reqBytes, _ := proto.Marshal(beef)
    respBytes := w.runtime.Call(w.module, "identify_admissible_outputs", reqBytes)
    var resp AdmittanceInstructions
    proto.Unmarshal(respBytes, &resp)
    return &resp, nil
}
```

Native Go topic managers keep working as-is alongside WASM modules. The shim is just translation between Go interfaces and the WASM module contract.

### Inter-module communication

Modules don't call each other directly. All inter-module calls are host-mediated:
- **Function calls**: `call_module(module_id, function, protobuf_bytes)` — host routes to target module
- **Shared state**: Two modules that need shared data both receive channels to the same backing store

## Open Questions

1. **Concurrency**: Can a module have multiple in-flight requests on the same channel? If so, the framing needs a request ID for correlation. RESP pipelining assumes ordered responses, which works if the mux preserves ordering per channel.
2. **Channel provisioning**: Exact mechanism for passing the channel table to the module at init time.
3. **Backpressure**: What happens when the module writes faster than the host can process? Stdout buffering handles this to a point, but may need explicit flow control.
4. **Error signaling**: How does the host signal a channel-level error (e.g., backend unavailable)?
5. **Protobuf vs Karmem**: Evaluate Karmem for WASM-optimized serialization. May be worth benchmarking against protobuf for our use case.

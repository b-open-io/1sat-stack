# GASP Binary Wire Protocol Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

Status: **In Progress**

Linear: OPL-1135 (child)

**Goal:** Define and implement a binary wire protocol for GASP that replaces JSON/HTTP transport with a compact binary envelope suitable for libp2p streams, WebSockets, or raw TCP.

**Architecture:** Two layers. The **GASP wire format** defines binary serialization for all GASP data types (Node, InitialRequest, etc.) — this is transport-agnostic and contributable back to gasp-core and go-overlay-services. The **overlay P2P envelope** is our transport layer that wraps GASP messages with method, status, topic, and payment fields for use over libp2p streams. The P2P remote implements the existing `gasp.Remote` interface, so it plugs into the GASP sync machinery with zero changes to the core algorithm.

**Tech Stack:** Go, `encoding/binary`, libp2p streams, `go-overlay-services` gasp package

---

## Protocol Spec

### Envelope

Every message on the stream uses this envelope. The envelope is self-describing — you can read it without knowing the method in advance.

```
ENVELOPE (request or response):
[1 byte  version]       // protocol version, currently 0
[1 byte  method]        // which GASP operation
[1 byte  status]        // 0=request, 1=ok, 2=error, 3=payment_required
[varint  topic len][topic bytes]
[uint64  price]         // sats (on 402/payment_required responses, or 0)
[varint  payment len][payment bytes]  // payment tx bytes, empty if free
[varint  body len][body bytes]        // method-specific binary payload
```

### Methods

```
0x01  INITIAL_REQUEST     // GetInitialResponse request
0x02  INITIAL_RESPONSE    // GetInitialResponse response
0x03  INITIAL_REPLY_REQ   // GetInitialReply request
0x04  INITIAL_REPLY_RES   // GetInitialReply response
0x05  REQUEST_NODE        // RequestNode request
0x06  NODE                // RequestNode response (or SubmitNode request)
0x07  NODE_RESPONSE       // SubmitNode response
```

### Status Codes

```
0x00  STATUS_REQUEST           // this is a request
0x01  STATUS_OK                // success
0x02  STATUS_ERROR             // error (body contains error message bytes)
0x03  STATUS_PAYMENT_REQUIRED  // payment needed (price field populated)
0x04  STATUS_NOT_FOUND         // requested data not available
```

### Body Layouts

#### INITIAL_REQUEST (method 0x01)
```
[uint32  version]       // GASP protocol version
[float64 since]         // timestamp (IEEE 754 double)
[uint32  limit]         // max UTXOs to return, 0=unlimited
```

#### INITIAL_RESPONSE (method 0x02)
```
[float64 since]         // responder's since value for reply direction
[varint  count]
  [32 bytes txid][uint32 outputIndex][float64 score]  // repeated
```

#### INITIAL_REPLY_REQ (method 0x03)
Same layout as INITIAL_RESPONSE — a UTXO list from the initial response.

#### INITIAL_REPLY_RES (method 0x04)
```
[varint  count]
  [32 bytes txid][uint32 outputIndex][float64 score]  // repeated
```

#### REQUEST_NODE (method 0x05)
```
[32 bytes graphID txid][uint32 graphID index]
[32 bytes outpoint txid][uint32 outpoint index]
[1 byte   metadata]    // 0=no, 1=yes
```

#### NODE (method 0x06)
Used as both RequestNode response and SubmitNode request.
```
[32 bytes graphID txid][uint32 graphID index]
[uint32   outputIndex]
[varint   rawTx len][rawTx bytes]
[varint   proof len][proof bytes]         // merkle path binary, empty if unconfirmed
[varint   txMetadata len][txMetadata bytes]
[varint   outputMetadata len][outputMetadata bytes]
[varint   input count]
  [varint hash len][hash bytes]           // input hash string as UTF-8
```

#### NODE_RESPONSE (method 0x07)
```
[varint  count]
  [32 bytes txid][uint32 index][1 byte metadata]  // requested inputs
```

### Stream Lifecycle

A libp2p stream on protocol `/overlay/gasp/1.0.0` carries a sequence of envelope messages. The stream is bidirectional — either side can send requests and responses. Messages are length-prefixed by the envelope's body len field, so the reader always knows when one message ends and the next begins.

**Real-time flow (after steak broadcast):**
```
Peer B → Peer A:  ENVELOPE(method=REQUEST_NODE, status=REQUEST, topic="tm_bap", body=outpoint)
Peer A → Peer B:  ENVELOPE(method=NODE, status=OK, body=node)
```

**Real-time with payment:**
```
Peer B → Peer A:  ENVELOPE(method=REQUEST_NODE, status=REQUEST, topic="tm_bap", body=outpoint)
Peer A → Peer B:  ENVELOPE(method=NODE, status=PAYMENT_REQUIRED, price=50)
Peer B → Peer A:  ENVELOPE(method=REQUEST_NODE, status=REQUEST, payment=<tx>, body=outpoint)
Peer A → Peer B:  ENVELOPE(method=NODE, status=OK, body=node)
```

**Full sync flow:**
```
B → A:  ENVELOPE(method=INITIAL_REQUEST, body={version, since, limit})
A → B:  ENVELOPE(method=INITIAL_RESPONSE, body={utxo list, since})
B → A:  ENVELOPE(method=REQUEST_NODE, body=outpoint1)
A → B:  ENVELOPE(method=NODE, body=node1)
B → A:  ENVELOPE(method=REQUEST_NODE, body=outpoint2)
A → B:  ENVELOPE(method=NODE, body=node2)
...
B → A:  ENVELOPE(method=INITIAL_REPLY_REQ, body={utxo list from response})
A → B:  ENVELOPE(method=INITIAL_REPLY_RES, body={additional utxos})
B → A:  ENVELOPE(method=NODE, body=node_from_B)    // SubmitNode
A → B:  ENVELOPE(method=NODE_RESPONSE, body={requested inputs})
```

---

## File Structure

| File | Responsibility |
|------|---------------|
| `pkg/p2p/envelope.go` | Envelope struct, Serialize/Deserialize, constants |
| `pkg/p2p/envelope_test.go` | Round-trip tests for envelope |
| `pkg/p2p/bodies.go` | Binary serialize/deserialize for each method body |
| `pkg/p2p/bodies_test.go` | Round-trip tests for each body type |
| `pkg/p2p/remote.go` | `P2PGASPRemote` implementing `gasp.Remote` over libp2p stream |
| `pkg/p2p/handler.go` | Stream handler (server side) dispatching to engine methods |
| `pkg/p2p/handler_test.go` | Handler tests with mock engine |
| `pkg/overlay/p2p.go` | Wire handler registration + remote creation (modify existing) |

---

## Chunk 1: Envelope + Body Serialization

### Task 1: Envelope constants and struct

**Files:**
- Create: `pkg/p2p/envelope.go`

- [ ] **Step 1: Write failing test for envelope round-trip**

Create `pkg/p2p/envelope_test.go` with a test that creates an envelope with all fields populated, serializes it, deserializes it, and asserts equality.

```go
func TestEnvelope_RoundTrip(t *testing.T) {
    env := &Envelope{
        Version: 0,
        Method:  MethodRequestNode,
        Status:  StatusRequest,
        Topic:   "tm_bap",
        Price:   0,
        Payment: nil,
        Body:    []byte{0x01, 0x02, 0x03},
    }
    data := env.Serialize()
    got, err := DeserializeEnvelope(data)
    if err != nil {
        t.Fatal(err)
    }
    // assert all fields match
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/p2p/ -run TestEnvelope_RoundTrip -v`
Expected: FAIL — types not defined

- [ ] **Step 3: Implement envelope constants, struct, Serialize, Deserialize**

Define method/status constants, `Envelope` struct, `Serialize() []byte`, `DeserializeEnvelope([]byte) (*Envelope, error)`. Use the same `appendUint32List`/`readUint32List` helpers from `message.go` where applicable. Use `binary.AppendUvarint` for varint fields.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/p2p/ -run TestEnvelope_RoundTrip -v`
Expected: PASS

- [ ] **Step 5: Add edge case tests**

Tests for: empty body, empty payment, empty topic, payment_required with price, error status with message body, maximum size body.

- [ ] **Step 6: Run all tests**

Run: `go test ./pkg/p2p/ -v`
Expected: all PASS

- [ ] **Step 7: Commit**

```bash
git add pkg/p2p/envelope.go pkg/p2p/envelope_test.go
git commit -m "Add GASP wire protocol envelope format"
```

### Task 2: Body serialization for REQUEST_NODE and NODE

**Files:**
- Create: `pkg/p2p/bodies.go`
- Create: `pkg/p2p/bodies_test.go`

- [ ] **Step 1: Write failing test for RequestNodeBody round-trip**

```go
func TestRequestNodeBody_RoundTrip(t *testing.T) {
    body := &RequestNodeBody{
        GraphID:  transaction.Outpoint{Txid: randomHash(t), Index: 0},
        Outpoint: transaction.Outpoint{Txid: randomHash(t), Index: 2},
        Metadata: true,
    }
    data := body.Serialize()
    got, err := DeserializeRequestNodeBody(data)
    // assert fields match
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/p2p/ -run TestRequestNodeBody -v`

- [ ] **Step 3: Implement RequestNodeBody Serialize/Deserialize**

Fixed layout: `[32+4][32+4][1]` = 69 bytes exactly.

- [ ] **Step 4: Write failing test for NodeBody round-trip**

Test with populated rawTx, proof, metadata, and inputs map.

- [ ] **Step 5: Implement NodeBody Serialize/Deserialize**

Varint-prefixed byte fields for rawTx, proof, txMetadata, outputMetadata. Varint count + varint-prefixed strings for inputs.

- [ ] **Step 6: Run all tests**

Run: `go test ./pkg/p2p/ -v`

- [ ] **Step 7: Commit**

```bash
git add pkg/p2p/bodies.go pkg/p2p/bodies_test.go
git commit -m "Add binary body layouts for RequestNode and Node"
```

### Task 3: Body serialization for InitialRequest, InitialResponse, InitialReply, NodeResponse

**Files:**
- Modify: `pkg/p2p/bodies.go`
- Modify: `pkg/p2p/bodies_test.go`

- [ ] **Step 1: Write failing tests for all four body types**

One round-trip test per type, including empty lists and populated lists.

- [ ] **Step 2: Implement all four body Serialize/Deserialize**

- `InitialRequestBody`: fixed layout `[4][8][4]` = 16 bytes
- `InitialResponseBody`: `[8][varint count][44 bytes per entry]`
- `InitialReplyBody`: same UTXO list format as InitialResponseBody (without the since field)
- `NodeResponseBody`: `[varint count][37 bytes per entry]`

- [ ] **Step 3: Run all tests**

Run: `go test ./pkg/p2p/ -v`

- [ ] **Step 4: Commit**

```bash
git add pkg/p2p/bodies.go pkg/p2p/bodies_test.go
git commit -m "Add binary body layouts for all GASP operations"
```

---

## Chunk 2: P2P GASP Remote + Stream Handler

### Task 4: P2PGASPRemote implementing gasp.Remote

**Files:**
- Create: `pkg/p2p/remote.go`

- [ ] **Step 1: Write failing test for RequestNode over a pipe**

Create a `net.Pipe()`, write a NODE response on one end, call `remote.RequestNode()` on the other, verify the returned `gasp.Node` matches.

- [ ] **Step 2: Implement P2PGASPRemote struct**

Wraps a `network.Stream` (or `io.ReadWriteCloser` for testability). Implements `gasp.Remote`:
- `GetInitialResponse`: serialize InitialRequest envelope → write → read InitialResponse envelope → deserialize
- `GetInitialReply`: serialize InitialReplyReq envelope → write → read InitialReplyRes envelope → deserialize
- `RequestNode`: serialize RequestNode envelope → write → read Node envelope → deserialize → convert to `gasp.Node`
- `SubmitNode`: serialize Node envelope → write → read NodeResponse envelope → deserialize

Each method handles `STATUS_PAYMENT_REQUIRED` and `STATUS_ERROR` responses.

- [ ] **Step 3: Run test**

Run: `go test ./pkg/p2p/ -run TestP2PRemote -v`

- [ ] **Step 4: Add tests for error and payment_required responses**

- [ ] **Step 5: Commit**

```bash
git add pkg/p2p/remote.go pkg/p2p/remote_test.go
git commit -m "Add P2PGASPRemote implementing gasp.Remote over streams"
```

### Task 5: Stream handler (server side)

**Files:**
- Create: `pkg/p2p/handler.go`
- Create: `pkg/p2p/handler_test.go`

- [ ] **Step 1: Define handler interface**

```go
type GASPProvider interface {
    ProvideForeignSyncResponse(ctx context.Context, req *gasp.InitialRequest, topic string) (*gasp.InitialResponse, error)
    ProvideForeignGASPNode(ctx context.Context, graphID, outpoint *transaction.Outpoint, topic string) (*gasp.Node, error)
}
```

This is a subset of the engine contract — keeps the handler decoupled from the full engine.

- [ ] **Step 2: Write failing test for handler processing a RequestNode**

Use `net.Pipe()`, send a RequestNode envelope on one end, run the handler on the other end with a mock `GASPProvider`, verify the response envelope contains the expected Node.

- [ ] **Step 3: Implement GASPStreamHandler**

Read envelope from stream → dispatch by method:
- `INITIAL_REQUEST` → `provider.ProvideForeignSyncResponse()` → write INITIAL_RESPONSE
- `REQUEST_NODE` → `provider.ProvideForeignGASPNode()` → write NODE
- `NODE` (SubmitNode) → not implemented initially, return STATUS_ERROR

Loop until stream closes or context cancelled.

- [ ] **Step 4: Run tests**

Run: `go test ./pkg/p2p/ -run TestHandler -v`

- [ ] **Step 5: Add test for not-found and error propagation**

- [ ] **Step 6: Commit**

```bash
git add pkg/p2p/handler.go pkg/p2p/handler_test.go
git commit -m "Add GASP stream handler dispatching to engine"
```

### Task 6: Wire into P2PBus

**Files:**
- Modify: `pkg/overlay/p2p.go`
- Modify: `pkg/overlay/config.go`

- [ ] **Step 1: Register stream handler on P2PBus**

Add `RegisterGASPHandler(provider GASPProvider)` to `P2PBus`. Calls `b.client.SetStreamHandler("/overlay/gasp/1.0.0", handler)`.

- [ ] **Step 2: Wire in overlay config initialization**

After engine is created and P2PBus is wired, call `p2pBus.RegisterGASPHandler(engine)` since the engine implements `GASPProvider`.

- [ ] **Step 3: Add NewP2PGASPRemote factory to P2PBus**

`func (b *P2PBus) NewRemote(peerID string, topic string) (gasp.Remote, error)` — opens a stream to the peer via `b.client.NewStream()` and returns a `P2PGASPRemote` wrapping it.

- [ ] **Step 4: Build and verify**

Run: `go build ./cmd/server`
Expected: clean build

- [ ] **Step 5: Commit**

```bash
git add pkg/overlay/p2p.go pkg/overlay/config.go
git commit -m "Wire GASP stream handler and remote into overlay P2P bus"
```

---

## Chunk 3: Clean up steak message version

### Task 7: Change steak version to 0

**Files:**
- Modify: `pkg/p2p/message.go`
- Modify: `pkg/p2p/message_test.go`
- Modify: `pkg/overlay/p2p.go`

- [ ] **Step 1: Replace SteakVersion2 with SteakVersion0**

Rename constant, update Serialize to write `0`, update Deserialize to expect `0`.

- [ ] **Step 2: Update tests**

Change version assertions from 2 to 0.

- [ ] **Step 3: Update PublishSteak in p2p.go**

Change `Version: p2p.SteakVersion2` to `Version: p2p.SteakVersion0`.

- [ ] **Step 4: Run all tests**

Run: `go test ./pkg/p2p/ -v`

- [ ] **Step 5: Commit**

```bash
git add pkg/p2p/message.go pkg/p2p/message_test.go pkg/overlay/p2p.go
git commit -m "Set steak message version to 0 (pre-release)"
```

---

## Status: In Progress

### Progress
- Chunk 1 (envelope + bodies): **Complete** — envelope in 1sat-stack, bodies moved to go-overlay-services with version byte
- Chunk 2 (remote + handler): **Not Started**
- Chunk 3 (steak version cleanup): **Not Started**

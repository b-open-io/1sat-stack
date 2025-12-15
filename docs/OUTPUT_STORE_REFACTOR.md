# OutputStore Simplification Plan

## Background

This work originated from comparing `/bsv21` routes with planned `/txo` routes. Analysis showed:

1. Both use `OutputStore.SearchOutputs()` under the hood
2. `bsv21` routes are topic-scoped (e.g., `GetTransactionByTopic`)
3. `txo` routes would be global (e.g., `GetTransaction` across all topics)
4. The per-topic store abstraction in `EventDataStorage` was over-engineered

**Conclusion**: Unify the storage layer first, then consolidate routes.

### Route Consolidation (Phase 2 - after storage refactor)

| Current bsv21 Route | Unified Route | Notes |
|---------------------|---------------|-------|
| `/:tokenId/tx/:txid` | `/tx/:txid?topic=` | Topic becomes optional query param |
| `/:tokenId/:lockType/:address/balance` | `/balance?events=` | Client builds event keys |
| `/:tokenId/:lockType/:address/history` | `/history?events=` | Client builds event keys |
| `/:tokenId/:lockType/:address/unspent` | `/unspent?events=` | Client builds event keys |
| `/:tokenId` (GetToken) | `/outpoint/:outpoint` | Generic output lookup |

**Key insight**: Event key formatting can be pushed to the client. The server just does event-based lookups.

**BSV21-specific**: `GetBalance` sums token `amt` field, not satoshis - this stays BSV21-specific or becomes a separate endpoint.

---

## Overview

Consolidate `EventDataStorage` and `OutputStore` into a single `OutputStore` struct that implements `engine.Storage`. Remove the per-topic store abstraction.

## Key Design Decisions

1. **Single global store** - No per-topic `OutputStore` instances

2. **Key structure**:
   ```
   # Per-outpoint hash (binary key: h: + 36 byte outpoint)
   h:{outpoint:36}           → Hash with fields:
     ev                      → events array (JSON strings)
     ms                      → merkle state (binary: height, idx, root, state)
     dt:{tag}                → indexer-specific data
     dp:{topic}              → ancillary txids (binary: 32 * n bytes)
     in:{topic}              → inputs consumed (binary: 36 * n bytes)

   # Bulk lookup hashes (for HMGET across many outpoints)
   h:sats                    → {outpoint:36} → satoshis (uint64, for balance calcs only)
   h:spnd                    → {outpoint:36} → spend txid (32 bytes)

   # Event indexes (ZSets, score = block height)
   z:{event}                 → outpoints (36 byte members)
   z:{event}:spnd            → spent outpoints (36 byte members)
   z:tp:{topic}              → outpoints in topic
   z:tp:{topic}:tx           → applied txids in topic (32 byte members)

   # Peer interactions
   h:pi:{topic}              → {host} → timestamp
   ```

   **Binary encoding**:
   - Outpoint: 36 bytes (32 byte txid + 4 byte vout, big-endian)
   - Txid: 32 bytes binary
   - Event keys: strings (user-searchable)

   **Note**: No txid index needed - use `Range(h:{txid:32}*)` to find all outputs for a txid.

3. **Store interface additions** - Add KV operations:
   ```go
   // New methods for store.Store interface
   Get(ctx context.Context, key []byte) ([]byte, error)
   Set(ctx context.Context, key, value []byte) error
   Del(ctx context.Context, key []byte) error
   Range(ctx context.Context, prefix []byte, limit int) ([]KV, error)
   ```

4. **Two search concepts**:
   - **General search** (`store.Search`): ZSet search, returns `[]ScoredMember` (member, score, key)
   - **TXO search** (`SearchOutputs`): Same search on event ZSets where members are outpoints, then loads additional data based on Include options

5. **OutputSearchCfg** extends store.SearchCfg:
   ```go
   type OutputSearchCfg struct {
       store.SearchCfg

       // Filtering
       FilterSpent bool

       // Data loading (from h:{outpoint} hash)
       IncludeTags []string   // Load dt:{tag} fields

       // Data loading (from BeefStore)
       IncludeOutput      bool  // Load tx output (script + satoshis)
       IncludeRawtx       bool  // Load full raw transaction
       IncludeMerkleProof bool  // Load merkle proof
   }
   ```

6. **ConsumedBy derived on the fly** - No storage needed
   - Get spend txid: `HGet(h:spnd, outpoint)`
   - Find outputs of spend tx in topic: `Range(h:{spendTxid}*)` filtered by topic

7. **OutputsConsumed stored** in `in:{topic}` field (topic-specific)

8. **Lookup services and indexers are compatible** - both write to the same event indexes (`z:{event}`) and data tags (`dt:{tag}`) using the same format. Configure your deployment with the appropriate combination based on your needs (overlay-only, indexer-only, or both).

   **Example: BSV21 Lookup Service** (`OutputAdmittedByTopic`):
   ```go
   // Events generated (stored in z:{event} indexes)
   events := []string{
       "id:{outpoint}",           // e.g., "id:abc123...def_0"
       "sym:{symbol}",            // e.g., "sym:PEPE" (mint only)
       "p2pkh:{addr}:{id}",       // e.g., "p2pkh:1ABC...:{id}"
       "list:{id}",               // ordlock listings
   }

   // Data stored (in dt:bsv21 field)
   data := map[string]interface{}{
       "bsv21": {
           "id":      "abc123...def_0",
           "op":      "deploy+mint",
           "amt":     "1000000",
           "sym":     "PEPE",
           "dec":     8,
           "address": "1ABC...",
       },
   }

   // Calls SaveEvents which writes to same indexes as indexer's SaveOutputs
   storage.SaveEvents(ctx, outpoint, events, data, score)
   ```

   **Future**: Convert BSV21 lookup service to a BSV21 indexer (deferred until after storage refactor).

9. **Separation of concerns - Overlay vs Indexer flows**:

   **Overlay flow** (engine.Storage methods) - topic-related only:
   - `InsertOutputs` → `in:{topic}`, `dp:{topic}`, `z:tp:{topic}` (no events, no data)
   - `InsertAppliedTransaction` → `ZAdd(z:tp:{topic}:tx)` + **triggers indexer run**
   - `MarkUTXOsAsSpent` → no-op (spend tracking handled by indexer via `SaveSpend`)

   **Indexer flow** (via OutputStore methods):
   - `SaveOutputs` → Sets `ev`, `ms`, `dt:{tag}`, `h:sats`, updates `z:{event}` indexes
   - `SaveEvents` → Sets `ev`, `dt:{tag}`, updates `z:{event}` indexes (subset of SaveOutputs, no sats/merkle)
   - `SaveSpend(outpoint, events, spendTxid, score)` → `HSet(h:spnd, ...)` + `ZAdd` to `z:{event}:spnd`

   The indexer parses both outputs AND inputs (via `ParseSpends`), so `SaveSpend` receives events from the already-parsed spend - no database read needed.

   **Key principle**: No database reads during write operations.

---

## File Changes

### 1. `output_store.go` - Rewrite

**Key builders**:
```go
// Binary keys (txid = chainhash.Hash, outpoint = transaction.Outpoint)
func keyOutHash(op *transaction.Outpoint) []byte {
    key := make([]byte, 2+36)  // "h:" + 36 byte outpoint
    copy(key, "h:")
    copy(key[2:], op.Txid[:])
    binary.BigEndian.PutUint32(key[34:], op.Index)
    return key
}

func keyTxidPrefix(txid *chainhash.Hash) []byte {
    key := make([]byte, 2+32)  // "h:" + 32 byte txid
    copy(key, "h:")
    copy(key[2:], txid[:])
    return key  // Range will match all h:{txid}{vout} keys
}

// String keys (event-based, user-searchable)
func keyEvent(event string) []byte       { return []byte("z:" + event) }
func keyEventSpnd(event string) []byte   { return []byte("z:" + event + ":spnd") }
func keyTopicOut(topic string) []byte    { return []byte("z:tp:" + topic) }
func keyTopicTx(topic string) []byte     { return []byte("z:tp:" + topic + ":tx") }
func keyPeerInteraction(topic string) []byte { return []byte("h:pi:" + topic) }

// Bulk lookup hash keys
const hashSats = "h:sats"
const hashSpnd = "h:spnd"
```

**Hash field names** (within `h:{outpoint}`):
```go
const (
    fldEvent  = "ev"    // events (JSON)
    fldMerkle = "ms"    // merkle state (binary)
    fldData   = "dt:"   // prefix for dt:{tag}
    fldDeps   = "dp:"   // prefix for dp:{topic}
    fldInputs = "in:"   // prefix for in:{topic}
)
```

**Methods**:

*Write operations*:
- `SaveOutputs(outputs, score)` → Sets `ev`, `ms`, `dt:{tag}`, `h:sats`, updates `z:{event}` indexes (indexer)
- `SaveEvents(outpoint, events, data, score)` → Sets `ev`, `dt:{tag}`, updates `z:{event}` indexes (lookup services)

*Search operations* (public API):
- `Search(cfg *OutputSearchCfg)` → ZSet search, returns outpoints + scores, applies FilterSpent
- `SearchOutputs(cfg *OutputSearchCfg)` → Search + load data based on Include options
- `SearchBalance(cfg *OutputSearchCfg)` → Search + bulk `HMGet(h:sats, ...)` for sum

*Txid-based operations*:
- `LoadOutputsByTxid(txid, cfg *OutputSearchCfg)` → `Range(h:{txid}*)`, apply Include options
- `LoadOutputsByTxidForTopic(txid, topic, cfg)` → Same but filtered by topic membership
- `Rollback(txid)` → `Range(h:{txid}*)` then delete + remove from indexes

*Spend operations*:
- `SaveSpend(outpoint, events, spendTxid, score)` → `HSet(h:spnd, ...)` + `ZAdd` to `z:{event}:spnd` sets
- `GetSpend/GetSpends` → `HGet/HMGet(h:spnd, ...)`

Events come from the already-parsed spend (via `ParseSpends`) - no database read needed.

*Internal*:
- `loadOutputs(outpoints, cfg)` → Load output data based on Include options

**Removed** (unused):
- `LoadOutput`, `LoadOutputFull`, `LoadOutputWithSpend`

### 2. `engine_storage.go` - engine.Storage methods on OutputStore

**Write operations** (topic-related only, no events):
- `InsertOutputs` → `in:{topic}` + `dp:{topic}` + `ZAdd(z:tp:{topic})` (topic membership only)
- `InsertAppliedTransaction` → `ZAdd(z:tp:{topic}:tx, txid)` + **trigger indexer**
- `MarkUTXOsAsSpent` → no-op (spend tracking handled by indexer)
- `UpdateConsumedBy` → no-op (derived on fly)
- `UpdateLastInteraction` → `HSet(h:pi:{topic}, host, timestamp)`

**Read operations**:
- `FindOutput/FindOutputs` → SearchOutputs with spent filter
- `FindOutputsForTransaction` → LoadOutputsByTxid
- `FindUTXOsForTopic` → Search `z:tp:{topic}` filtered unspent
- `DoesAppliedTransactionExist` → `ZScore(z:tp:{topic}:tx, txid)`
- `GetLastInteraction` → `HGet(h:pi:{topic}, host)`

### 3. `event_data.go` - DELETE

### 4. `search.go` - Update OutputSearchCfg

Rename/update Include options:
- `IncludeScript` → `IncludeOutput`
- Add `IncludeMerkleProof`

### 5. Update callers

- `bsv21/routes.go` - direct `OutputStore` calls
- `bsv21/lookup.go` - direct calls
- `cmd/server/` - wire single `OutputStore`

---

## Implementation Order

### Phase 1: Storage Unification (current focus)

1. **Add KV methods to `store.Store` interface** (Get, Set, Del, Range)
2. **Update `search.go`** - OutputSearchCfg Include options
3. **Rewrite `output_store.go`** with Hash + KV operations
4. **Rewrite `engine_storage.go`** to implement engine.Storage
5. **Delete `event_data.go`**
6. **Update callers** (bsv21, indexer, cmd/server)
7. **Compile and test**

### Phase 2: Route Consolidation (future)

1. Create unified `/txo` routes with optional topic filtering
2. Deprecate bsv21-specific routes that duplicate txo functionality
3. Keep BSV21-specific balance calculation as separate endpoint
4. Update clients to build event keys

---

## Notes

- `HeightScore` exists in both `txo/output.go` and `indexer/context.go` - consolidate to txo
- `ZRange` uses `ScoreRange` struct, not `(start, stop)` params
- Satoshis stored in `h:sats` for balance calcs only, not in output object (also in BEEF)
- Script/satoshis for display come from BEEF via `IncludeOutput`

## Implementation Clarifications

1. **Binary keys internally** - Outpoints (`*transaction.Outpoint`, 36 bytes) and txids (`*chainhash.Hash`, 32 bytes) must be used as binary byte arrays everywhere internally. Strings only at API boundaries (JSON responses) and within event keys (e.g., `"id:{tokenId}"`).

2. **No bulk operations needed** - With Badger's LSM architecture, individual writes within a transaction are already batched efficiently. Simple loops are preferred over complex bulk APIs.

3. **Score distinction** (important!):
   - **Indexer flow** uses `HeightScore(blockHeight, idx)` - block-ordered data
   - **Overlay/topic flow** (`InsertOutputs`, `SaveEvents`) uses `time.Now().UnixNano()` - these events are NOT parsed by the indexer, so they use timestamp ordering instead of block position

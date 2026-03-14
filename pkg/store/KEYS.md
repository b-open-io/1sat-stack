# Store Keys Reference

All keys, data types, and member formats used with the `store.Store` interface.

## Type Discrimination

Application keys are **type-agnostic** — the store method called (`ZAdd` vs `HSet` vs `SAdd`) determines the data type. The Badger adapter adds internal prefixes (`zs:`, `zm:`, `h:`, `s:`, `k:`) to discriminate types in its flat key-value store. Redis uses native data types and needs no prefix.

Application code should never include type prefixes in keys.

## Application Namespace Prefixes

| Prefix | Purpose | Example |
|--------|---------|---------|
| `ev:` | Event namespace (indexed outputs) | `ev:own:1A1zP1...` |
| `q:` | Queue convention (ZSets used as work queues) | `q:sub_abc123` |

## Score Format

All sorted set scores use `types.HeightScore()`:
- **Confirmed**: `blockHeight + blockIdx/1e9` (e.g., `850000.000000123`)
- **Unconfirmed**: `unixTimestamp + nanos/1e9` (e.g., `1703097600.123456789`)

## Member Encoding

- **Binary outpoint**: 36 bytes = txid (32 bytes, LE) + vout (4 bytes, BE)
- **Binary txid**: 32 bytes, little-endian hash
- **String**: UTF-8 encoded

---

## Key Builders (`pkg/txo/keys.go`)

```go
// Application prefixes
const PfxQueue = "q:"   // Queue keys
const PfxTopic = "tp:"  // Topic namespace
const PfxEvent = "ev:"  // Event namespace

// Bulk lookup hash keys ([]byte)
var KeySatoshis = []byte("sats")  // field: outpoint, value: satoshis
var KeySpends   = []byte("spnd")  // field: outpoint, value: spend txid
var KeyProgress = []byte("prog")  // field: subscription/owner, value: height

// Key builders
func KeyEvent(event string) []byte              // ev:{event}
func KeyEventSpent(event string) []byte         // ev:{event}:spnd
func KeyLog(logName string) []byte              // {logName}
func KeyQueue(queueName string) []byte          // q:{queueName}
func KeyTokenQueue(tokenId string) []byte       // q:tok:{tokenId}
func KeySet(name string) []byte                 // {name}
func KeyOutHash(op) []byte                      // {outpoint} (36 bytes)
func KeyTxidPrefix(txid) []byte                 // {txid} (32 bytes, for prefix scan)
```

---

## Queue Keys (Sorted Sets)

| Key Pattern | Member Type | Score | Purpose |
|-------------|-------------|-------|---------|
| `q:{subscription}` | binary txid (32 bytes) | HeightScore | JungleBus subscription queue |
| `q:tok:{tokenId}` | binary outpoint (36 bytes) | HeightScore | Per-token BSV21 queue |

## Transaction Log Keys (Sorted Sets)

| Key Pattern | Member Type | Score | Purpose |
|-------------|-------------|-------|---------|
| `tx:pending` | hex string txid | HeightScore | Pending confirmation |
| `tx:immutable` | hex string txid | HeightScore | Confirmed and archived |
| `tx:rollback` | hex string txid | HeightScore | Rolled back transactions |

> **TODO**: tx logs use hex string members instead of binary txids. Should be
> migrated to binary chainhash format on next store clear for consistency.

---

## Hash Keys

### Per-Output Hash: `{outpoint}`

Binary key: 36 bytes outpoint

| Field | Value Type | Value Encoding | Purpose |
|-------|------------|----------------|---------|
| `ev` | events | JSON string array | Event list for this output |
| `ms` | merkle state | binary (12 bytes: height[4] + idx[8]) | Block position |
| `dt:{tag}` | tag data | JSON object | Parser-specific data |

### Bulk Lookup Hashes

| Key | Field Type | Field Encoding | Value Type | Value Encoding | Purpose |
|-----|------------|----------------|------------|----------------|---------|
| `sats` | outpoint | binary (36 bytes) | satoshis | uint64 BE (8 bytes) | Satoshi values |
| `spnd` | outpoint | binary (36 bytes) | spend txid | binary (32 bytes) | Spend tracking |
| `prog` | varies | string | height/timestamp | uint32 BE (4 bytes) or string | Progress tracking |

### Progress Hash Fields (`prog`)

| Field Pattern | Purpose | Value |
|---------------|---------|-------|
| `{subscriptionId}` | JungleBus sync progress | uint32 BE block height |
| `{ownerAddress}` | Owner sync progress | uint32 BE block height |

---

## Event/Index Keys (Sorted Sets)

| Key Pattern | Member Type | Score | Purpose |
|-------------|-------------|-------|---------|
| `ev:{event}` | binary outpoint (36 bytes) | HeightScore | Event index |
| `ev:{event}:spnd` | binary outpoint (36 bytes) | HeightScore | Spent event index |

### Common Event Patterns

| Event Pattern | Full Key | Purpose |
|---------------|----------|---------|
| `own:{address}` | `ev:own:1A1zP1...` | Owner/address index |
| `txid:{txidHex}` | `ev:txid:abc123...` | Transaction outputs |
| `id:{tokenId}` | `ev:id:abc123...i0` | BSV21 token by ID |
| `sym:{symbol}` | `ev:sym:PEPE` | BSV21 token by symbol |
| `p2pkh:{addr}:{tokenId}` | `ev:p2pkh:1A1z...:abc...i0` | P2PKH token holder |

---

## Set Keys

| Key Pattern | Member Type | Purpose |
|-------------|-------------|---------|
| `bsv21:whitelist` | string tokenId | Always-active tokens |
| `bsv21:blacklist` | string tokenId | Never-active tokens |
| `admin:users` | string pubkey (field) → JSON `AdminUser` (value) | Admin user entries (hash, not set) |
| `admin:requests` | string pubkey (field) → JSON `AccessRequest` (value) | Pending access requests (hash) |

---

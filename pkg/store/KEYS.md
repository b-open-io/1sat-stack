# Store Keys Reference

This document catalogs all keys, data types, and member formats used with the `store.Store` interface.

## Conventions

### Key Prefixes
All keys use a type prefix:
- `h:` - Hash keys (field-value maps)
- `z:` - Sorted set keys (scored members)
- `s:` - Set keys (unordered members)
- `q:` - Queue keys (sorted sets used as work queues)

### Score Format
All sorted set scores use `types.HeightScore()` for consistent ordering:
- **Confirmed**: `blockHeight + blockIdx/1e9` (e.g., `850000.000000123`)
- **Unconfirmed**: `unixTimestamp + nanos/1e9` (e.g., `1703097600.123456789`)

Since unix timestamps (~1.7e9) > block heights (~850k), unconfirmed always sorts after confirmed.

### Member Encoding
- **Binary outpoint**: 36 bytes = txid (32 bytes, little-endian) + vout (4 bytes, big-endian)
- **Binary txid**: 32 bytes, little-endian hash
- **String**: UTF-8 encoded

---

## Key Builders (`pkg/txo/keys.go`)

All keys should be built using exported functions from `pkg/txo/keys.go`:

```go
// Prefixes
const PfxHash  = "h:"   // Hash keys
const PfxZSet  = "z:"   // Sorted set keys
const PfxSet   = "s:"   // Set keys
const PfxQueue = "q:"   // Queue keys
const PfxTopic = "tp:"  // Topic prefix within ZSet

// Bulk lookup hash keys ([]byte)
var KeySatoshis = []byte("h:sats")  // field: outpoint, value: satoshis
var KeySpends   = []byte("h:spnd")  // field: outpoint, value: spend txid
var KeyProgress = []byte("h:prog")  // field: subscription/owner, value: height

// Key builders
func KeyEvent(event string) []byte              // z:{event}
func KeyEventSpent(event string) []byte         // z:{event}:spnd
func KeyTopicOutputs(topic string) []byte       // z:tp:{topic}
func KeyTopicTxs(topic string) []byte           // z:tp:{topic}:tx
func KeyLog(logName string) []byte              // z:{logName}
func KeyMerkleState(topic string, state uint32) []byte  // z:merkle:{topic}:{state}
func KeyQueue(queueName string) []byte          // q:{queueName}
func KeyTokenQueue(tokenId string) []byte       // q:tok:{tokenId}
func KeySet(name string) []byte                 // s:{name}
func KeyOutHash(op *transaction.Outpoint) []byte       // h:{outpoint}
func KeyTxidPrefix(txid *chainhash.Hash) []byte        // h:{txid} (for prefix scan)
func PeerInteractionField(topic, host string) []byte   // peer:{topic}:{host} (field for h:prog)
```

---

## Queue Keys (Sorted Sets)

Queues are sorted sets where items are processed in score order and removed after processing.

| Key Pattern | Member Type | Score | Purpose | Source |
|-------------|-------------|-------|---------|--------|
| `q:{subscription}` | **binary** txid (32 bytes) | HeightScore | JungleBus subscription queue | `pkg/jbsync/subscriber.go` |
| `q:tok:{tokenId}` | **binary** outpoint (36 bytes) | HeightScore | Per-token BSV21 queue | `pkg/bsv21/sync.go` |

---

## Hash Keys

### Per-Output Hash: `h:{outpoint}`

Binary key: 2 bytes prefix + 36 bytes outpoint

| Field | Value Type | Value Encoding | Purpose |
|-------|------------|----------------|---------|
| `ev` | events | JSON string array | Event list for this output |
| `ms` | merkle state | binary (12 bytes: height[4] + idx[8]) | Block position |
| `dt:{tag}` | tag data | JSON object | Parser-specific data |
| `dp:{topic}` | dep txids | binary (N × 32 bytes) | Dependency transaction IDs |
| `in:{topic}` | inputs | binary (N × 36 bytes) | Consumed input outpoints |

### Bulk Lookup Hashes

| Key | Field Type | Field Encoding | Value Type | Value Encoding | Purpose |
|-----|------------|----------------|------------|----------------|---------|
| `h:sats` | outpoint | **binary** (36 bytes) | satoshis | uint64 BE (8 bytes) | Satoshi values |
| `h:spnd` | outpoint | **binary** (36 bytes) | spend txid | binary (32 bytes) | Spend tracking |
| `h:prog` | varies | **string** | height/timestamp | uint32 BE (4 bytes) or string | Progress tracking |

### Progress Hash Fields (`h:prog`)

| Field Pattern | Purpose | Value |
|---------------|---------|-------|
| `{subscriptionId}` | JungleBus sync progress | uint32 BE block height |
| `{ownerAddress}` | Owner sync progress | uint32 BE block height |
| `peer:{topic}:{host}` | Peer interaction timestamp | string float64 |

---

## Event/Index Keys (Sorted Sets)

Event keys index outputs by various criteria for efficient lookups.

| Key Pattern | Member Type | Score | Purpose |
|-------------|-------------|-------|---------|
| `z:{event}` | **binary** outpoint (36 bytes) | HeightScore | Event index |
| `z:{event}:spnd` | **binary** outpoint (36 bytes) | HeightScore | Spent event index |
| `z:tp:{topic}` | **binary** outpoint (36 bytes) | HeightScore | Topic outputs |
| `z:tp:{topic}:tx` | **binary** txid (32 bytes) | HeightScore | Applied transactions |
| `z:merkle:{topic}:{state}` | **binary** outpoint (36 bytes) | HeightScore | Merkle state index |

### Common Event Patterns

Events are stored within `z:{event}` keys:

| Event Pattern | Example | Purpose |
|---------------|---------|---------|
| `own:{address}` | `own:1A1zP1...` | Owner/address index |
| `txid:{txidHex}` | `txid:abc123...` | Transaction outputs |
| `id:{tokenId}` | `id:abc123...i0` | BSV21 token by ID |
| `sym:{symbol}` | `sym:PEPE` | BSV21 token by symbol |
| `p2pkh:{addr}:{tokenId}` | `p2pkh:1A1z...:abc...i0` | P2PKH token holder |
| `cos:{addr}:{tokenId}` | `cos:1A1z...:abc...i0` | Cosigner token holder |
| `ltm:{tokenId}` | `ltm:abc123...i0` | LTM tokens |
| `pow20:{tokenId}` | `pow20:abc123...i0` | POW20 tokens |
| `list:{tokenId}` | `list:abc123...i0` | Listed tokens |
| `list:{addr}:{tokenId}` | `list:1A1z...:abc...i0` | Listed by seller |

---

## Set Keys

Simple sets for membership testing.

| Key Pattern | Member Type | Purpose | Source |
|-------------|-------------|---------|--------|
| `s:bsv21:whitelist` | **string** tokenId | Always-active tokens | `pkg/bsv21/manager.go` |
| `s:bsv21:blacklist` | **string** tokenId | Never-active tokens | `pkg/bsv21/manager.go` |

---

## Best Practices

1. **Always use key builder functions** - Never construct keys inline
2. **Binary members for outpoints/txids** - Use `outpoint.Bytes()` and `txid[:]`
3. **Use HeightScore for all scores** - Call `types.HeightScore()` or `types.ScoreFromTx()`
4. **Document member encoding** - Binary vs string must be explicit
5. **All keys must have type prefix** - `h:`, `z:`, `s:`, or `q:`

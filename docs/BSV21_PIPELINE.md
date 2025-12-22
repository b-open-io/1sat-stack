# BSV21 Token Pipeline

This document describes the BSV21 token processing pipeline in 1sat-stack.

## Overview

BSV21 is a fungible token protocol on BSV. The pipeline:
1. Discovers all token deployments from JungleBus
2. Evaluates which tokens should be actively indexed (whitelist or paid fees)
3. Processes token transactions through validated topic managers
4. Serves token data via HTTP API

## Components

| Component | File | Purpose |
|-----------|------|---------|
| JungleBus Subscriber | `pkg/jbsync/subscriber.go` | Receives transactions, queues to `q:bsv21` |
| BSV21 Dispatcher | `pkg/bsv21/sync.go` | Routes transactions to per-token queues |
| Token Manager | `pkg/bsv21/manager.go` | Manages worker lifecycle |
| Token Worker | `pkg/bsv21/manager.go` | Processes single token via GASP |
| Discovery Topic | `pkg/topic/discovery.go` | Admits all deploy operations |
| Validated Topic | `pkg/topic/bsv21.go` | Validates token transfers |
| Lookup Service | `pkg/lookup/bsv21.go` | Indexes BSV21 data |

---

## Pipeline Stages

### Stage 1: Discovery

JungleBus subscription receives all BSV21 transactions and queues them.

```
JungleBus
     │
     │ OnTransaction(txn)
     ▼
Subscriber
     │
     └─→ q:bsv21  ← binary txid (32 bytes), score = HeightScore
```

**Keys Written:**
- `q:bsv21` - ZAdd binary txid

**Progress Tracking:**
- `h:prog` field `{subscriptionId}` - uint32 BE block height

### Stage 2: Dispatch

Dispatcher reads from main queue, parses BSV21 outputs, and routes to per-token queues.

```
q:bsv21
     │
     │ Worker reads txid
     ▼
Dispatcher
     │
     ├─→ Load transaction from BEEF storage
     │
     ├─→ Parse BSV21 outputs
     │
     ├─→ For deploy operations:
     │         │
     │         └─→ Overlay.Submit(tm_bsv21)
     │                   │
     │                   ├─→ z:tp:tm_bsv21      ← outpoint
     │                   ├─→ z:tp:tm_bsv21:tx   ← txid
     │                   └─→ BSV21Lookup.OutputAdmittedByTopic()
     │                             │
     │                             ├─→ h:{outpoint} ev, dt:bsv21
     │                             └─→ z:id:{tokenId}, z:sym:{symbol}
     │
     └─→ For all BSV21 outputs:
               │
               └─→ q:tok:{tokenId}  ← binary outpoint (36 bytes)
```

**Keys Written:**
- `q:tok:{tokenId}` - ZAdd binary outpoint
- Via overlay: `z:tp:tm_bsv21`, `z:tp:tm_bsv21:tx`
- Via lookup: `z:id:*`, `z:sym:*`, `h:{outpoint}`

### Stage 3: Token Manager Lifecycle

Runs periodically (default: every 5 minutes) to evaluate token status.

```
Token Manager
     │
     ├─→ Read z:tp:tm_bsv21 (all discovered tokens)
     │
     ├─→ Read s:bsv21:whitelist
     │
     ├─→ Read s:bsv21:blacklist
     │
     └─→ For each token:
               │
               ├─→ Skip if blacklisted
               │
               ├─→ If whitelisted OR has balance:
               │         │
               │         ├─→ Register tm_{tokenId} topic
               │         │
               │         └─→ Create Token Worker
               │
               └─→ If not whitelisted AND no balance:
                         │
                         └─→ Stop worker if running
```

**Keys Read:**
- `z:tp:tm_bsv21` - ZRange to get all token outpoints
- `s:bsv21:whitelist` - SMembers
- `s:bsv21:blacklist` - SMembers

### Stage 4: Token Processing

Token workers process their queue using GASP for dependency resolution.

```
q:tok:{tokenId}
     │
     │ Worker reads outpoint
     ▼
Token Worker
     │
     └─→ GASP Processor
               │
               ├─→ Load transaction from BEEF storage
               │
               ├─→ Resolve dependencies (recursive)
               │
               └─→ Overlay.Submit(tm_{tokenId})
                         │
                         ├─→ Bsv21ValidatedTopicManager
                         │         │
                         │         └─→ Validate: tokens_in >= tokens_out
                         │
                         ├─→ z:tp:tm_{tokenId}      ← outpoint
                         ├─→ z:tp:tm_{tokenId}:tx   ← txid
                         │
                         └─→ BSV21Lookup.OutputAdmittedByTopic()
                                   │
                                   ├─→ h:{outpoint} ev, dt:bsv21
                                   └─→ z:id:*, z:p2pkh:*, etc.
```

**Keys Written:**
- Via overlay: `z:tp:tm_{tokenId}`, `z:tp:tm_{tokenId}:tx`
- Via lookup: `z:id:*`, `z:p2pkh:*`, `z:list:*`, `h:{outpoint}`

---

## Key Summary

### Queue Keys
| Key | Member | Score | Purpose |
|-----|--------|-------|---------|
| `q:bsv21` | binary txid (32) | HeightScore | Main dispatcher queue |
| `q:tok:{tokenId}` | binary outpoint (36) | HeightScore | Per-token processing queue |

### Topic Keys
| Key | Member | Score | Purpose |
|-----|--------|-------|---------|
| `z:tp:tm_bsv21` | binary outpoint (36) | HeightScore | Discovered tokens |
| `z:tp:tm_bsv21:tx` | binary txid (32) | HeightScore | Applied discovery txs |
| `z:tp:tm_{tokenId}` | binary outpoint (36) | HeightScore | Token outputs |
| `z:tp:tm_{tokenId}:tx` | binary txid (32) | HeightScore | Applied token txs |

### Event Keys
| Key | Member | Score | Purpose |
|-----|--------|-------|---------|
| `z:id:{tokenId}` | binary outpoint (36) | HeightScore | Token by ID |
| `z:sym:{symbol}` | binary outpoint (36) | HeightScore | Token by symbol |
| `z:p2pkh:{addr}:{tokenId}` | binary outpoint (36) | HeightScore | P2PKH holder |
| `z:list:{tokenId}` | binary outpoint (36) | HeightScore | Listed tokens |

### Set Keys
| Key | Member | Purpose |
|-----|--------|---------|
| `s:bsv21:whitelist` | string tokenId | Always-active tokens |
| `s:bsv21:blacklist` | string tokenId | Never-active tokens |

---

## Token Validation

The `Bsv21ValidatedTopicManager` validates transfers by checking:

1. Parse BSV21 data from all outputs
2. Parse BSV21 data from all inputs (via BEEF)
3. Sum token amounts by tokenId
4. Reject if `tokens_out > tokens_in` for any tokenId

Deploy operations bypass validation (handled by discovery topic).

---

## Fee Address Derivation

Token indexing can require payment. Fee addresses are derived using HD keys:

```go
// Derive fee address for a token
feeAddress := GenerateFeeAddress(tokenId)

// Check balance via OwnerSync
ownerSync.Sync(ctx, feeAddress)
balance, _ := outputStore.SearchBalance(ctx, cfg.WithEvents("own:" + feeAddress))
```

---

## Related Documentation

- `docs/OVERLAY_ARCHITECTURE.md` - General overlay system
- `pkg/store/KEYS.md` - Complete key reference

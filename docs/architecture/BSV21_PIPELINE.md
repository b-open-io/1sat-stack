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
| Token Manager | `pkg/bsv21/manager.go` | Manages worker lifecycle, balance tracking |
| TopicWorker | `pkg/gasp/topic_worker.go` | Processes single token via GASP |
| Discovery Topic | `pkg/topic/discovery.go` | Admits all deploy operations |
| Validated Topic | `pkg/topic/bsv21.go` | Validates token transfers |
| Lookup Service | `pkg/lookup/bsv21.go` | Indexes BSV21 data into per-topic SQLite |

---

## Pipeline Stages

### Stage 1: Discovery

JungleBus subscription receives all BSV21 transactions and queues them.
The subscription ID is configured via `overlay.bsv21.sub_id` in config store
(or `ONESAT_OVERLAY_BSV21_SUB_ID` env var).

```
JungleBus
     │
     │ OnTransaction(txn)
     ▼
Subscriber (pkg/jbsync)
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
     │                   └─→ Discovery topic DB: outputs, applied_txs
     │                       BSV21Lookup: token_outputs table
     │
     └─→ For all BSV21 outputs:
               │
               └─→ q:tm_{tokenId}  ← binary outpoint (36 bytes)
```

**Keys Written:**
- `q:tm_{tokenId}` - ZAdd binary outpoint (per-token processing queue)
- Via overlay engine: per-topic SQLite tables (outputs, applied_txs)
- Via BSV21Lookup: `token_outputs` table in topic database

### Stage 3: Token Manager Lifecycle

Runs periodically (default: every 5 minutes) to evaluate token status.

```
Token Manager
     │
     ├─→ Phase 1: Register whitelisted tokens upfront
     │
     ├─→ Phase 2: Query tm_bsv21 topic for discovered tokens
     │         │
     │         └─→ For each funded or whitelisted token:
     │                   │
     │                   ├─→ Register tm_{tokenId} topic manager
     │                   │
     │                   └─→ Create TopicWorker
     │
     └─→ Phase 3: Unregister topics for tokens no longer active
```

**Token Status Tracking:**
Each active token has an in-memory `TokenStatus` tracking credits, debits, and live balance. When balance is exhausted during processing, triggers async re-sync of the fee address.

**Keys Read:**
- `s:bsv21:whitelist` - SMembers (always-active tokens)
- `s:bsv21:blacklist` - SMembers (never-active tokens)

### Stage 4: Token Processing

TopicWorkers process their queue using GASP for dependency resolution.

```
q:tm_{tokenId}
     │
     │ TopicWorker reads outpoint
     ▼
TopicWorker (pkg/gasp/topic_worker.go)
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
                         ├─→ Per-topic SQLite: outputs, applied_txs
                         │
                         └─→ BSV21Lookup.OutputAdmittedByTopic()
                                   │
                                   └─→ token_outputs table in topic DB
```

---

## Per-Topic Storage

BSV21 uses the overlay's per-topic SQLite isolation. Each token topic (`tm_{tokenId}`) gets its own database file.

### BSV21Lookup Custom Schema

Added lazily to each topic's database on first use:

```sql
CREATE TABLE IF NOT EXISTS token_outputs (
    outpoint     BLOB PRIMARY KEY,
    token_id     TEXT NOT NULL,
    op           TEXT NOT NULL,
    lock_type    TEXT NOT NULL,
    address      TEXT NOT NULL,
    amount       INTEGER NOT NULL,
    score        REAL NOT NULL,
    spend_txid   BLOB,
    spend_score  REAL
);

CREATE INDEX idx_token_utxos ON token_outputs(token_id, lock_type, address, score)
    WHERE spend_txid IS NULL;
CREATE INDEX idx_token_history ON token_outputs(token_id, lock_type, address, score);
CREATE INDEX idx_token_deploy ON token_outputs(token_id)
    WHERE op IN ('deploy+mint', 'deploy+auth');
```

Lookup services access this via `TopicStorage.DB()` — the raw SQL connection for the topic's database.

---

## Queue Keys

| Key | Member | Score | Purpose |
|-----|--------|-------|---------|
| `q:bsv21` | binary txid (32) | HeightScore | Main dispatcher queue |
| `q:tm_{tokenId}` | binary outpoint (36) | HeightScore | Per-token processing queue |

## Set Keys

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

Token indexing can require payment. Fee addresses are derived using HD keys from the token's outpoint. Balance is tracked in-memory and re-synced via OwnerSync when exhausted.

---

## Related Documentation

- `docs/architecture/OVERLAY_ARCHITECTURE.md` - General overlay system and per-topic storage
- `pkg/store/KEYS.md` - Storage key reference for the main data store

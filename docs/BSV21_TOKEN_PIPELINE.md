# BSV21 Token Pipeline

This document describes the complete flow for discovering, tracking, and processing BSV21 tokens with fee-based gating.

## Overview

The system is simple and direct:

- **Dispatcher**: Parses transactions, routes to per-token queues, submits deploy ops to discovery topic
- **TokenManager**: Periodically evaluates tokens, registers/unregisters topics directly with overlay engine
- **TokenWorker**: Processes transactions for active tokens
- **OwnerSync**: Ingests transactions for fee addresses from JungleBus
- **Overlay Engine**: Admits outputs to topics, tracks which outpoints belong to which topics

### Core Principles

1. **Direct registration**: TokenManager registers/unregisters topics directly with overlay engine
2. **No intermediate state**: TokenManager calculates balance and acts immediately
3. **Whitelist/Blacklist**: BSV21-specific lists control token activation:
   - `bsv21:whitelist` - tokens always active (bypass fee check)
   - `bsv21:blacklist` - tokens never activated

## Periodic Processes

1. **Dispatcher** - reads `q:bsv21`, parses BSV21 outputs, routes to `q:tok:{tokenId}`, submits deploys to `tm_bsv21`

2. **TokenManager** (every ~1 minute):
   - Reads `z:tp:tm_bsv21` for all discovered tokens
   - Loads `bsv21:whitelist` and `bsv21:blacklist`
   - For each token: check lists, derive fee address, sync via OwnerSync, calculate balance
   - If whitelisted OR balance > 0 (and not blacklisted): register topic with overlay, create worker
   - If blacklisted OR (not whitelisted AND balance <= 0): unregister topic, stop worker

3. **TokenWorker** (per active token) - reads `q:tok:{tokenId}`, submits to `tm_{tokenId}` overlay topic

## Components

### 1. JungleBus Subscriber (`pkg/jbsync`)

Listens to JungleBus for BSV21 transactions and queues txids. Configured in `jbsync.subscribers` - not BSV21-specific.

- **Input**: JungleBus subscription
- **Output**: Txids added to `q:bsv21` sorted set (score = block height)

### 2. Dispatcher (`pkg/bsv21/sync.go`)

Processes transactions from the main queue and routes them appropriately.

- **Input**: Txids from `q:bsv21`
- **Actions**:
  1. Load transaction from BEEF storage
  2. Parse BSV21 outputs to extract tokenIds
  3. For each output: queue txid to `q:tok:{tokenId}`
  4. For deploy operations (`deploy+mint`, `deploy+auth`): submit BEEF to `tm_bsv21`
- **Output**: 
  - Txids in per-token queues
  - Deploy outputs admitted to `tm_bsv21` topic

### 3. Token Discovery Topic (`pkg/topic/discovery.go`)

The `tm_bsv21` topic admits all deploy operations for token discovery.

- **TopicManager**: `Bsv21DiscoveryTopicManager`
- **Admits**: `deploy+mint` and `deploy+auth` operations
- **Storage**: Outpoints stored in `z:tp:tm_bsv21` sorted set
- **Purpose**: Source of truth for all known tokenIds

### 4. TokenManager (`pkg/bsv21/sync.go`)

Manages per-token workers and topic registration.

#### Periodic Lifecycle Management (every ~1 minute)

```
1. Query known tokens from z:tp:tm_bsv21
2. Load whitelist from bsv21:whitelist
3. Load blacklist from bsv21:blacklist

4. For each known token:
   a. If blacklisted → skip (never active)
   b. If whitelisted → active (bypass fee check)
   c. Otherwise:
      - Derive fee address (BIP32 from tokenId)
      - Call OwnerSync to ingest fee payments
      - Calculate balance:
        credits = unspent satoshis at fee address
        debits = ZCard(z:tp:tm_{tokenId}) × feePerOutput
        balance = credits - debits
      - Active if balance > 0

5. For active tokens with no worker:
   - Register tm_{tokenId} topic with overlay engine
   - Create TokenWorker

6. For existing workers where token is no longer active:
   - Unregister tm_{tokenId} topic
   - Stop TokenWorker
```

### 5. OwnerSync (`pkg/owner/sync.go`)

Syncs transactions for an address from JungleBus and ingests into the system.

- **Input**: Bitcoin address (fee address)
- **Actions**:
  1. Fetch transactions from JungleBus for the address
  2. Load BEEF for each transaction
  3. Ingest outputs into OutputStore
- **Purpose**: Ensures fee payments are indexed so balance queries return fresh data

### 6. TokenWorker (`pkg/bsv21/sync.go`)

Processes transactions for a single token.

- **Input**: Txids from `q:tok:{tokenId}`
- **Actions**:
  1. Load BEEF with inputs
  2. Submit to overlay with topic `tm_{tokenId}`

### 7. Per-Token TopicManager (`pkg/topic/bsv21.go`)

Validates token transfers for a specific token.

- **TopicManager**: `Bsv21ValidatedTopicManager`
- **Validates**: Input/output balance for transfers
- **Admits**: Deploy ops (always), transfers (if inputs >= outputs)

## Data Flow Diagram

```
                         JungleBus
                             │
                             ▼
                    ┌────────────────┐
                    │  Subscriber    │
                    └────────┬───────┘
                             │ txids
                             ▼
                      ┌──────────────┐
                      │  q:bsv21     │
                      └──────┬───────┘
                             │
                             ▼
                    ┌────────────────┐
                    │   Dispatcher   │
                    └────────┬───────┘
                             │
              ┌──────────────┼──────────────┐
              │              │              │
              ▼              ▼              ▼
     ┌─────────────┐  ┌─────────────┐  ┌─────────────┐
     │q:tok:{id1}  │  │q:tok:{id2}  │  │  Overlay    │
     └─────────────┘  └─────────────┘  │  tm_bsv21   │
                                       └──────┬──────┘
                                              │
                                              ▼
                                       ┌─────────────┐
                                       │z:tp:tm_bsv21│ (known tokens)
                                       └──────┬──────┘
                                              │
                              ┌───────────────┴───────────────┐
                              │                               │
                              ▼                               │
              ┌───────────────────────────────┐               │
              │         TokenManager          │               │
              │    (periodic lifecycle)       │               │
              │                               │               │
              │  For each token:              │               │
              │  - Check blacklist            │               │
              │  - Derive fee address         │               │
              │  - OwnerSync fee address      │               │
              │  - Calculate balance          │               │
              │  - Register/Unregister topic  │               │
              │  - Create/Stop worker         │               │
              └───────────────┬───────────────┘               │
                              │                               │
                    ┌─────────┼─────────┐                     │
                    │         │         │                     │
                    ▼         ▼         ▼                     │
             ┌──────────┐ ┌──────────┐ ┌──────────┐           │
             │ Worker 1 │ │ Worker 2 │ │ Worker N │           │
             └────┬─────┘ └────┬─────┘ └────┬─────┘           │
                  │            │            │                 │
                  ▼            ▼            ▼                 │
             ┌──────────┐ ┌──────────┐ ┌──────────┐           │
             │q:tok:{1} │ │q:tok:{2} │ │q:tok:{N} │           │
             └────┬─────┘ └────┬─────┘ └────┬─────┘           │
                  │            │            │                 │
                  ▼            ▼            ▼                 │
             ┌──────────┐ ┌──────────┐ ┌──────────┐           │
             │tm_{tok1} │ │tm_{tok2} │ │tm_{tokN} │◄──────────┘
             │ (overlay)│ │ (overlay)│ │ (overlay)│   registered by
             └──────────┘ └──────────┘ └──────────┘   TokenManager
```

## Storage Keys

| Key Pattern | Type | Description |
|-------------|------|-------------|
| `q:bsv21` | Sorted Set | Main BSV21 transaction queue (score = height) |
| `q:tok:{tokenId}` | Sorted Set | Per-token transaction queue |
| `z:tp:tm_bsv21` | Sorted Set | All known token outpoints (discovery topic) |
| `z:tp:tm_{tokenId}` | Sorted Set | Outputs admitted to per-token topic |
| `own:{address}` | Sorted Set | Outputs owned by address (for balance queries) |
| `bsv21:whitelist` | Set | Token IDs that are always active (bypass fee check) |
| `bsv21:blacklist` | Set | Token IDs that will never be activated |

## Activation Flow

A token becomes active when:

1. **Discovered**: Deploy transaction submitted to `tm_bsv21` and admitted
2. **Funded**: Someone sends satoshis to the fee address
3. **Synced**: TokenManager calls OwnerSync to ingest the payment
4. **Evaluated**: TokenManager calculates `credits - debits > 0`
5. **Registered**: TokenManager registers `tm_{tokenId}` with overlay and creates worker

A token becomes inactive when:
- Blacklisted → TokenManager skips token on next cycle
- Not whitelisted AND balance drops to 0 or negative → TokenManager unregisters topic, stops worker

## Configuration

### JungleBus Subscription (fills the queue)

```yaml
jbsync:
  subscribers:
    - subscription_id: "your-bsv21-subscription-id"
      queue_name: "bsv21"
      from_block: 783968
      autostart: true
```

### BSV21 Sync (processes the queue)

```yaml
bsv21:
  mode: embedded
  sync:
    enabled: true
    dispatch_workers: 8
    token_workers: 16
    fee_per_output: 1000
```

### Token Whitelist/Blacklist

Tokens can be managed via admin API:

```bash
# Whitelist a token (always active, bypasses fee check)
curl -X POST http://localhost:8080/admin/api/whitelist \
  -H "Content-Type: application/json" \
  -d '{"topic": "abc123..."}'

# Blacklist a token (never active)
curl -X POST http://localhost:8080/admin/api/blacklist \
  -H "Content-Type: application/json" \
  -d '{"topic": "abc123..."}'

# Remove from whitelist
curl -X DELETE http://localhost:8080/admin/api/whitelist/abc123...

# Remove from blacklist
curl -X DELETE http://localhost:8080/admin/api/blacklist/abc123...
```

## Key Design Decisions

1. **Direct Registration**: TokenManager registers/unregisters topics directly with overlay engine - no intermediate state

2. **Fee Calculation as Utility**: `GenerateFeeAddress()` is a simple function, not a service

3. **Single Periodic Process**: Only TokenManager runs periodically for activation logic

4. **Token-Level Whitelist/Blacklist**: `bsv21:whitelist` and `bsv21:blacklist` contain tokenIds, managed by BSV21, not overlay

5. **Fee Address Derivation**: Deterministic from tokenId using BIP32

6. **Credits via OwnerSync**: Fee payments are regular P2PKH outputs, synced from JungleBus

7. **Debits via Output Count**: `ZCard` on topic sorted set × fee per output

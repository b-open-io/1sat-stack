# BSV21 & Overlay System Audit

**Date**: 2025-12-20
**Status**: In Progress

---

## Related Documentation

- **[BSV21_TOKEN_PIPELINE.md](./BSV21_TOKEN_PIPELINE.md)** - Canonical pipeline architecture documentation
- **[SYNC_PLAN.md](../pkg/bsv21/SYNC_PLAN.md)** - Original sync implementation plan
- **[CONFIG_CLEANUP_PLAN.md](./CONFIG_CLEANUP_PLAN.md)** - Planned configuration cleanup

---

## Table of Contents

1. [Context](#context)
2. [Architecture Overview](#architecture-overview)
3. [Data Flow Diagrams](#data-flow-diagrams)
4. [Module Summaries](#module-summaries)
5. [Issues Found](#issues-found)
6. [Action Items](#action-items)
7. [Questions to Investigate](#questions-to-investigate)

---

## Context

### What We're Trying to Accomplish

The BSV21 token pipeline should:
1. **Discover** all BSV21 token deployments from JungleBus
2. **Evaluate** which tokens should be actively indexed (whitelist OR paid fees)
3. **Process** token transactions through validated topic managers
4. **Serve** token data via HTTP API

### Current Problem

A whitelisted token (`ae59f3b898ec61acbdb6cc7a245fabeded0c094bf046f35206a3aec60ef88127_0`) was not creating a worker overnight, despite:
- Being added to `bsv21:whitelist`
- The lifecycle manager running every 5 minutes
- The token eventually being in `tm_bsv21` topic

After a server restart, the worker was created immediately. This suggests either:
1. The lifecycle manager was not running
2. The whitelist wasn't being loaded correctly
3. The token wasn't in the topic when the lifecycle ran
4. Some state was stuck/cached

### Key Design Principles (from BSV21_TOKEN_PIPELINE.md)

1. **Direct registration**: TokenManager registers/unregisters topics directly with overlay engine
2. **No intermediate state**: TokenManager calculates balance and acts immediately
3. **Whitelist/Blacklist**: BSV21-specific lists control token activation
4. **Fee calculation via OwnerSync**: Fee payments are synced from JungleBus

---

## Architecture Overview

The BSV21/Overlay system processes BSV21 fungible tokens through a multi-stage pipeline:

### Components

| Component | Purpose | Key Files |
|-----------|---------|-----------|
| **JBSync** | Subscribes to JungleBus, queues transactions | `pkg/jbsync/` |
| **Worker** | Generic queue processor framework | `pkg/worker/` |
| **BSV21 Sync** | Dispatcher + TokenManager + TokenWorkers | `pkg/bsv21/sync.go` |
| **Topic Managers** | Decide which outputs to admit | `pkg/topic/` |
| **Overlay Engine** | Coordinates topic managers, stores outputs | `pkg/overlay/` |
| **OutputStore** | Stores indexed outputs, implements engine.Storage | `pkg/txo/` |

### Storage Keys

| Key Pattern | Purpose |
|-------------|---------|
| `q:bsv21` | Main dispatcher queue (txids from JungleBus) |
| `q:tok:{tokenId}` | Per-token queue (txids to process) |
| `z:tp:tm_bsv21` | Discovery topic - all deploy operations |
| `z:tp:tm_{tokenId}` | Validated topic - token transfers |
| `z:tp:{topic}:tx` | Applied transactions per topic |
| `bsv21:whitelist` | Set of always-active token IDs |
| `bsv21:blacklist` | Set of never-active token IDs |
| `h:{outpoint}` | Output hash data (events, merkle state, tag data) |
| `h:sats` | Bulk satoshi lookup hash |
| `h:spnd` | Bulk spend lookup hash |

---

## Data Flow Diagrams

### Flow 1: Discovery (JungleBus → tm_bsv21)

```
JBSubscriber (subscription for BSV21 txs)
       │
       │ receives block with transactions
       ▼
Store.ZAdd(q:bsv21, {txid, score})
       │
       │ dispatcher polls queue
       ▼
BSV21 Batch Dispatcher (pkg/bsv21/sync.go:runDispatcher)
       │
       ├─→ Fetch batch (up to 1000 txs)
       │
       ├─→ Process all txs concurrently:
       │     │
       │     ├─→ BeefStorage.LoadTx(txid)
       │     ├─→ Decode BSV21 from outputs
       │     ├─→ Collect tokenIds for each tx
       │     └─→ Build BEEF for deploy operations
       │
       ├─→ If ANY tx fails: retry entire batch
       │
       └─→ If all succeed (commit phase):
             │
             ├─→ Store.ZAdd(q:tok:{tokenId}, {txid, score}) for all tokens
             │
             ├─→ For deploy operations:
             │         Overlay.Submit(TaggedBEEF{Topics: ["tm_bsv21"]})
             │               │
             │               ▼
       │         Bsv21DiscoveryTopicManager.IdentifyAdmissibleOutputs()
       │               │ admits deploy+mint and deploy+auth
       │               ▼
       │         OutputStore.InsertOutputs(topic="tm_bsv21", outputs)
       │               │
       │               ▼
       │         Store.ZAdd(z:tp:tm_bsv21, {outpoint, score})
       │
       └─→ Store.ZRem(q:bsv21, txid)  // remove from queue
```

### Flow 2: Lifecycle (tm_bsv21 → TokenWorkers)

```
TokenManager.Start()
       │
       ├─→ manageWorkerLifecycle() // initial call
       │
       └─→ Ticker (every 5 min) → manageWorkerLifecycle()


manageWorkerLifecycle():
       │
       ├─→ Store.ZRange(z:tp:tm_bsv21) // get all discovered tokens
       │
       ├─→ Store.SMembers(bsv21:whitelist)
       │
       ├─→ Store.SMembers(bsv21:blacklist)
       │
       └─→ For each token outpoint in topic:
             │
             ├─→ Parse outpoint → tokenId
             │
             ├─→ Skip if blacklisted
             │
             ├─→ Check if whitelisted
             │
             ├─→ GenerateFeeAddress(tokenId) → address
             │
             ├─→ owner.Sync(address) // sync fee payments
             │
             ├─→ calculateBalance(tokenId, address)
             │     │
             │     ├─→ credits = unspent sats at fee address
             │     └─→ debits = (admitted outputs) × feePerOutput
             │
             └─→ If whitelisted OR balance > 0:
                   │
                   ├─→ Register tm_{tokenId} topic with overlay engine
                   │
                   └─→ createWorker(tokenId, address)
                         │
                         └─→ TokenWorker.Run() in goroutine
```

### Flow 3: Token Processing (TokenWorker → validated topic)

```
TokenWorker.Run():
       │
       └─→ Loop:
             │
             ├─→ Store.ZRange(q:tok:{tokenId}) // get queued txids
             │
             └─→ For each txid:
                   │
                   ├─→ BeefStorage.BuildFullBeefTx(txid)
                   │
                   ├─→ Overlay.Submit(TaggedBEEF{Topics: ["tm_{tokenId}"]})
                   │         │
                   │         ▼
                   │   Bsv21ValidatedTopicManager.IdentifyAdmissibleOutputs()
                   │         │
                   │         ├─→ Decode BSV21 from outputs → tokensOut
                   │         │
                   │         ├─→ Decode BSV21 from inputs → tokensIn
                   │         │
                   │         └─→ If tokensIn >= tokensOut:
                   │               admit outputs
                   │         │
                   │         ▼
                   │   OutputStore.InsertOutputs(topic="tm_{tokenId}", outputs)
                   │
                   └─→ Store.ZRem(q:tok:{tokenId}, txid)
```

---

## Module Summaries

### pkg/overlay/ (3 files, ~420 lines)

**Status**: ✅ Clean

| File | Lines | Purpose |
|------|-------|---------|
| `config.go` | 83 | Configuration, initialization |
| `routes.go` | 168 | HTTP routes (wraps go-overlay-services) |
| `services.go` | 170 | Service container, topic/lookup management |

**Key Types**:
- `Services` - Wraps overlay engine, manages topic factories
- `TopicManagerFactory` - Lazy topic creation pattern

**Notes**:
- Uses factory pattern for topic registration
- `ActivateConfiguredTopics()` activates whitelisted topics
- Clean separation of concerns

---

### pkg/bsv21/ (5 files, ~1700 lines)

**Status**: ⚠️ Has Issues

| File | Lines | Purpose |
|------|-------|---------|
| `bsv21.go` | 286 | Core indexer, token parsing |
| `config.go` | 182 | Configuration, service init |
| `routes.go` | 552 | HTTP API endpoints |
| `sync.go` | 699 | JungleBus sync pipeline |
| `SYNC_PLAN.md` | 893 | Architecture documentation |

**Key Types**:
- `Indexer` - Implements `indexer.Indexer` for parsing
- `SyncServices` - Dispatcher worker management
- `TokenManager` - Per-token worker lifecycle
- `TokenWorker` - Processes single token's transactions

**Issues**: See [Issues Found](#issues-found)

---

### pkg/topic/ (3 files, ~370 lines)

**Status**: ⚠️ Minor Issues

| File | Lines | Purpose |
|------|-------|---------|
| `discovery.go` | 78 | Admits all deploy ops (tm_bsv21) |
| `bsv21.go` | 228 | Validates token transfers (tm_{tokenId}) |
| `onesat.go` | 66 | Admits all outputs (tm_1sat) |

**Key Types**:
- `Bsv21DiscoveryTopicManager` - For token discovery
- `Bsv21ValidatedTopicManager` - For token validation
- `OneSatTopicManager` - Catch-all for 1sat ordinals
- `MissingInputError` - Error for missing inputs

**Notes**:
- All implement `engine.TopicManager` interface
- `storage` field is unused in all managers

---

### pkg/txo/ (7 files, ~1900 lines)

**Status**: ⚠️ Has Issues

| File | Lines | Purpose |
|------|-------|---------|
| `config.go` | 113 | Configuration, service init |
| `output_store.go` | 824 | Core output storage |
| `engine_storage.go` | 381 | Implements engine.Storage interface |
| `output.go` | 83 | IndexedOutput type |
| `routes.go` | 374 | HTTP API endpoints |
| `search.go` | 107 | Search configuration |
| `output_store_old.go.bak` | 630 | **OBSOLETE** - old implementation |

**Key Types**:
- `OutputStore` - Core storage (Store + PubSub + BeefStore)
- `IndexedOutput` - Extended output with owners, events, data
- `OutputSearchCfg` - Fluent search builder

**Notes**:
- Implements `engine.Storage` for overlay integration
- Many search options defined but never used

---

### pkg/worker/ (2 files, ~290 lines)

**Status**: ⚠️ Has Dead Code

| File | Lines | Purpose |
|------|-------|---------|
| `config.go` | 29 | **DEAD CODE** - DefaultConfig unused |
| `worker.go` | 258 | Generic queue worker |

**Key Types**:
- `Worker` - Queue processor with concurrency
- `Config` - Worker configuration
- `Handler` - Processing callback type

**Notes**:
- `config.go` should be removed per CONFIG_CLEANUP_PLAN.md
- `ProcessOnce` method exists but unused

---

## Issues Found

### Bugs

| ID | Severity | Module | Description |
|----|----------|--------|-------------|
| BUG-1 | **HIGH** | bsv21/bsv21.go | `reasons` map (line 233) is created but never populated. The check at line 275 `if reason, ok := reasons[tokenId]` will never find anything. Token validation reasons are lost. |
| BUG-2 | Medium | bsv21/routes.go | `tokenId` path param captured but unused in `GetTransaction` (line 227) |
| BUG-3 | **HIGH** | bsv21/sync.go | TokenWorker removes from queue even on error (line 638). Failed transactions are lost instead of retried. |
| BUG-4 | **HIGH** | bsv21/sync.go | Dispatcher handler commits side effects (`ZAdd` to token queues at line 254) before the worker removes from main queue. Partial batch failures cause inconsistent state. |

### Dead Code

| ID | Module | Item | Notes |
|----|--------|------|-------|
| DEAD-1 | bsv21/bsv21.go | `IndexingFee` constant | sync.go uses cfg.FeePerOutput instead |
| DEAD-2 | bsv21/bsv21.go | `WhitelistFn`, `BlacklistFn` | Set but never called in parsing |
| DEAD-3 | bsv21/bsv21.go | `FundBalance` field | JSON:"-", never set or read |
| DEAD-4 | bsv21/config.go | `ModeRemote` | Returns "not implemented" |
| DEAD-5 | bsv21/routes.go | `TokenResponse` struct | Defined but never used |
| DEAD-6 | bsv21/sync.go | `jbClient` field | Stored but never accessed |
| DEAD-7 | bsv21/sync.go | `score` param in processTransaction | Passed but unused |
| DEAD-8 | topic/discovery.go | `storage` field | Never accessed |
| DEAD-9 | topic/bsv21.go | `storage`, `topic` fields | Never accessed |
| DEAD-10 | topic/bsv21.go | `tokenSummary.deploy` field | Never set or read |
| DEAD-11 | txo/config.go | `Services.sseManager` | Declared, never used |
| DEAD-12 | txo/config.go | `ModeRemote` | Returns "not implemented" |
| DEAD-13 | txo/search.go | Multiple fields | `ExcludeMined`, `ExcludeMempool`, `IncludeOutput`, `IncludeRawtx`, `IncludeMerkleProof`, `StoreSearchCfg()` |
| DEAD-14 | txo/ | `output_store_old.go.bak` | Entire file is obsolete |
| DEAD-15 | worker/config.go | Entire file | `DefaultConfig` never used |

### Visibility Gaps

| ID | Area | Issue | Impact |
|----|------|-------|--------|
| VIS-1 | Lifecycle | No log when `manageWorkerLifecycle` starts running | Can't tell if lifecycle is executing |
| VIS-2 | Lifecycle | No log of how many tokens found in topic | Can't tell if topic has data |
| VIS-3 | Topic search | `/api/txo/search/tp:tm_bsv21` returns nulls | Unclear if storage or loading issue |
| VIS-4 | Worker creation | No log when worker already exists | Can't tell if duplicate creation attempted |

### Configuration Issues

| ID | Issue | Notes |
|----|-------|-------|
| CFG-1 | `overlay.topic_whitelist` still exists | User mentioned it should be removed |
| CFG-2 | Multiple `ModeRemote` constants | Never implemented, should remove or implement |

### Architectural Gaps (vs bsv21-overlay-1sat-sync reference)

Comparison of `pkg/bsv21/sync.go` against reference implementation at `../bsv21-overlay-1sat-sync/cmd/jbsync/`.

#### Dispatcher (queue.go vs sync.go)

| Aspect | Reference | 1sat-stack | Gap |
|--------|-----------|------------|-----|
| Batch processing | Fetches batch, processes all, commits at end | `worker.Worker` commits per-item | **BUG-4** |
| Side effect timing | `ZAdd` to token queues after all succeed (line 186) | `ZAdd` inside handler before completion (line 254) | **BUG-4** |
| Error handling | Any error → return, retry whole batch | Per-item error logging, continues | Different strategy |

#### TokenWorker (workers.go vs sync.go)

| Aspect | Reference | 1sat-stack | Gap |
|--------|-----------|------------|-----|
| Sequential processing | ✅ limiter pattern (line 265) | ✅ Same (line 626) | None |
| ZRem on error | Only after success (line 360) | Always, even on error (line 638) | **BUG-3** |
| MissingInputError | Requeues dependency tx, restarts loop (lines 284-356) | Not implemented | Missing feature |
| Balance exhaustion | Self-deregisters when balance <= 0 (lines 236-261) | Not implemented | Missing feature |
| Merkle validation | Validates, fetches fresh if invalid (lines 548-621) | Not implemented | Missing feature |
| Worker recovery | Killed on error, recreated on lifecycle (line 276) | Logs error, continues (line 630) | Different strategy |

#### Key Architectural Decision Needed

The dispatcher's batch atomicity problem (BUG-4) has two potential solutions:

1. **Modify `worker.Worker`** to support batch-commit mode where `ZRem` happens after all handlers complete
2. **Replace dispatcher** with inline batch processing like the reference (no `worker.Worker` abstraction)

The complication: even with batch `ZRem`, the handler still does `ZAdd` inside itself. True atomicity requires the handler to return "what to write" rather than writing directly. This is a larger refactor.

---

## Action Items

### Phase 1: Visibility & Debugging (Do First)

#### Flow 1: Discovery Visibility

- [ ] **VIS-1**: Add `SearchOutpoints` to enable topic member visibility (see details below)
- [ ] **VIS-2**: Add API endpoint to check queue depths (`q:bsv21`, `q:tok:{tokenId}`)
- [ ] **VIS-3**: Add success log when `Overlay.Submit` succeeds for deploy ops

#### Flow 2: Lifecycle Visibility

- [ ] **VIS-4**: Add log at start of `manageWorkerLifecycle`: count of topic members, whitelist size
- [ ] **VIS-5**: Add log showing each token evaluated with whitelist/balance result
- [ ] **VIS-6**: Add log when worker already exists for tokenId

#### VIS-3: SearchOutpoints Implementation

**Problem**: `/api/txo/search/tp:tm_bsv21` returns nulls because `SearchOutputs` calls `loadOutputs`, which requires hash data at `h:{outpoint}`. Overlay's `InsertOutputs` only writes to sorted sets, not hash data.

**Solution**: Add `SearchOutpoints` function that returns outpoints + scores without loading full output data.

**Files to modify**: `pkg/txo/output_store.go`

**Changes**:

1. Add `OutpointResult` type:
```go
type OutpointResult struct {
    Outpoint *transaction.Outpoint
    Score    float64
}
```

2. Add `SearchOutpoints` function:
```go
func (s *OutputStore) SearchOutpoints(ctx context.Context, cfg *OutputSearchCfg) ([]*OutpointResult, error) {
    results, err := s.Search(ctx, cfg)
    if err != nil {
        return nil, err
    }

    out := make([]*OutpointResult, len(results))
    for i, r := range results {
        out[i] = &OutpointResult{
            Outpoint: transaction.NewOutpointFromBytes(r.Member),
            Score:    r.Score,
        }
    }
    return out, nil
}
```

3. Refactor `SearchOutputs` to use `SearchOutpoints`:
```go
func (s *OutputStore) SearchOutputs(ctx context.Context, cfg *OutputSearchCfg) ([]*IndexedOutput, error) {
    results, err := s.SearchOutpoints(ctx, cfg)
    if err != nil {
        return nil, err
    }

    ops := make([]*transaction.Outpoint, len(results))
    for i, r := range results {
        ops[i] = r.Outpoint
    }
    return s.loadOutputs(ctx, ops, cfg)
}
```

4. Add route or query param to expose `SearchOutpoints` via HTTP API

### Phase 2: Bug Fixes

- [ ] **BUG-1**: Fix `reasons` map in bsv21.go - populate it or use `token.reason` correctly
- [ ] **BUG-2**: Either use `tokenId` in GetTransaction or remove the path param

### Phase 3: Dead Code Removal

Priority order (safest first):

1. [ ] **DEAD-14**: Delete `pkg/txo/output_store_old.go.bak`
2. [ ] **DEAD-15**: Delete `pkg/worker/config.go`
3. [ ] **DEAD-8,9,10**: Remove unused fields from topic managers
4. [ ] **DEAD-13**: Remove unused search config fields
5. [ ] **DEAD-1,2,3,5,6,7**: Clean up bsv21 module dead code
6. [ ] **DEAD-4,11,12**: Remove `ModeRemote` constants or implement them

### Phase 4: Architecture Improvements

- [ ] Consider removing `overlay.topic_whitelist` if no longer needed
- [ ] Document the HD key derivation for fee addresses
- [ ] Add metrics/monitoring for worker status
- [ ] Consider adding health check for sync pipeline

---

## Questions to Investigate

### Q1: Why didn't the whitelisted token create a worker overnight?

**Hypothesis**: The token wasn't in `tm_bsv21` topic yet when whitelisted.

**To verify**:
1. Add logging to show when lifecycle runs and what it finds
2. Check if token is in topic: `Store.ZRange(z:tp:tm_bsv21)`
3. Check when token was discovered vs when it was whitelisted

### Q2: Why does topic search return nulls?

**Hypothesis**: Outputs are in the sorted set but not in the hash storage.

**To verify**:
1. Check if `z:tp:tm_bsv21` has members: `Store.ZCard()`
2. Check if individual outputs exist: `Store.HGetAll(h:{outpoint})`
3. Trace `InsertOutputs` to verify it writes to both locations

### Q3: Is the overlay.Submit actually working?

**To verify**:
1. Add logging in `Bsv21DiscoveryTopicManager.IdentifyAdmissibleOutputs`
2. Add logging in `OutputStore.InsertOutputs`
3. Verify BEEF is valid before submission

### Q4: What's the relationship between indexer flow and overlay flow?

**Current understanding**:
- Indexer flow: JBSync → Indexer.IngestCtx → OutputStore.SaveOutput
- Overlay flow: BSV21 Sync → Overlay.Submit → TopicManager → OutputStore.InsertOutputs

**Question**: Are these duplicating work? Should BSV21 use one or the other?

---

## Session Notes

### 2025-12-20

**What we did**:
1. Ran comprehensive audit of overlay, bsv21, topic, txo, worker modules
2. Mapped out the complete data flow
3. Identified bugs, dead code, and visibility gaps
4. Created this planning document

**Key finding**: The token worker IS working now after server restart. The whitelist was loaded, the token was in the topic, and the lifecycle check created the worker. The mystery is why it didn't work overnight.

**Next steps**: Add visibility logging to understand the lifecycle behavior.

---

## Appendix: Key File Locations

```
cmd/server/
├── main.go          # Entry point, server lifecycle
├── config.go        # All service initialization and wiring
└── config_test.go   # Configuration tests

pkg/overlay/
├── config.go        # Overlay configuration
├── routes.go        # HTTP routes (swagger stubs + mount)
└── services.go      # Services container, topic management

pkg/bsv21/
├── bsv21.go         # Indexer implementation
├── config.go        # BSV21 configuration
├── routes.go        # HTTP API
├── sync.go          # Dispatcher, TokenManager, TokenWorker
└── SYNC_PLAN.md     # Architecture docs

pkg/topic/
├── discovery.go     # Bsv21DiscoveryTopicManager (tm_bsv21)
├── bsv21.go         # Bsv21ValidatedTopicManager (tm_{tokenId})
└── onesat.go        # OneSatTopicManager (tm_1sat)

pkg/txo/
├── config.go        # TXO configuration
├── output_store.go  # Core storage implementation
├── engine_storage.go # engine.Storage interface impl
├── output.go        # IndexedOutput type
├── routes.go        # HTTP API
├── search.go        # Search configuration
└── output_store_old.go.bak # OBSOLETE

pkg/worker/
├── config.go        # DEAD CODE
└── worker.go        # Generic queue worker

pkg/jbsync/
├── config.go        # JungleBus subscriber config
├── keys.go          # Queue key builders
└── subscriber.go    # JungleBus subscription handler
```

---

## Completed Changes

### 2025-12-20: Dispatcher Batch Processing

**Problem**: The original dispatcher processed transactions one at a time using `worker.Worker`, committing each `ZAdd` to token queues immediately. If a tx failed after some commits, the queue would have partial data.

**Solution**: Replaced per-item worker with batch processor matching `bsv21-overlay-1sat-sync/cmd/jbsync/queue.go`:

1. Fetch batch of up to 1000 txs from `q:bsv21`
2. Process all txs concurrently (parse BSV21, collect tokenIds, build BEEF for deploys)
3. If ANY tx fails, log error and retry entire batch
4. Only after all succeed: commit to token queues, submit deploys to overlay, remove from main queue

**Files changed**:
- `pkg/bsv21/sync.go`:
  - Replaced `worker.Worker` dispatcher with `runDispatcher()` batch processor
  - Removed unused `dispatch()` function
  - Removed `dispatcher` field from `SyncServices` struct
  - Removed `worker` package import

**Error handling**:
- Infrastructure errors (DB down, etc): Return error, fatal to the goroutine
- Code bugs (parsing errors): Log error, retry batch after 1 second delay
- Both cases: Nothing is committed until entire batch succeeds

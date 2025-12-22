# BSV21 Sync Integration Plan

## Overview

This document outlines the plan to integrate the `bsv21-overlay-1sat-sync` pipeline into `1sat-stack`, leveraging existing infrastructure and the shared overlay engine.

## Source Analysis: bsv21-overlay-1sat-sync

### Architecture

The sync project implements a 3-stage pipeline:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                        bsv21-overlay-1sat-sync Flow                         │
└─────────────────────────────────────────────────────────────────────────────┘

Stage 1: JungleBus Subscription (junglebus.go)
  ┌─────────────────┐
  │   JungleBus     │───► Queue txid to "bsv21" sorted set (score = height.idx)
  │   Subscriber    │     Track progress in "progress" sorted set
  └─────────────────┘     Handle mempool transactions if enabled

Stage 2: Queue Processor (queue.go)
  ┌─────────────────┐
  │ Queue Processor │───► Load BEEF, parse BSV21, categorize by tokenId
  │  (8 workers)    │     Add to "tok:{tokenId}" queues
  └─────────────────┘     Discover new tokens → add to "bsv21:active"

Stage 3: Token Workers (workers.go)
  ┌─────────────────┐
  │ Token Channel   │───► Per-token workers with dedicated engines
  │   Manager       │     Process in-order, submit to overlay engine
  │  (16 workers)   │     Handle missing inputs, balance/fee tracking
  └─────────────────┘
```

### Key Components in bsv21-overlay-1sat-sync

#### Stage 1: JungleBus Subscriber (`cmd/jbsync/junglebus.go`)

- Subscribes to JungleBus topic for BSV21 transactions
- Batches transactions (1000 per batch) before writing to queue
- Tracks progress with reorg protection (6-block delay)
- Optionally handles mempool transactions
- Queue key: `"bsv21"` sorted set with score = `height + index/1e9`

#### Stage 2: Queue Processor (`cmd/jbsync/queue.go`)

- Pulls txids from main queue (`"bsv21"`)
- Loads BEEF, parses BSV21 token data from outputs
- Categorizes transactions by tokenId into per-token queues (`"tok:{tokenId}"`)
- Discovers new tokens → adds to `"bsv21:active"` registry
- Triggers worker creation via `TokenChannelManager.DiscoverToken()`

#### Stage 3: Token Workers (`cmd/jbsync/workers.go`)

- `TokenChannelManager`: Manages lifecycle of per-token workers
- `TokenWorker`: Processes transactions for a single token
- Each worker has dedicated `engine.Engine` instance (can be simplified)
- Handles missing input errors by requeuing dependencies
- Tracks balance/fees for paid indexing model

#### Fee Service (`pkg/fees/`)

- Generates deterministic addresses per tokenId from HD key
- Tracks credits/debits for paid indexing
- Syncs address UTXOs to calculate available balance

### Storage Keys Used

**Queue Keys** (prefixed with `q:`):

| Key | Type | Purpose |
|-----|------|---------|
| `q:{queueName}` | Sorted Set | Transaction queue (score = HeightScore) |
| `q:tok:{tokenId}` | Sorted Set | Per-token transaction queue (BSV21-specific) |

Note: Queue name is configured separately from subscription ID. Multiple subscriptions could feed the same queue.

**Sync Progress**:

| Key | Type | Purpose |
|-----|------|---------|
| `sync:progress` | Hash | Sync progress (field = subscriptionId or address, value = lastBlock) |

---

## Scoring Convention

All sorted sets use a **unified scoring convention** via `txo.HeightScore()`:

```go
// HeightScore calculates a score for ordering transactions
func HeightScore(height uint32, idx uint64) float64 {
    if height == 0 {
        // Mempool/unconfirmed: unix seconds + nanoseconds as decimal
        now := time.Now()
        return float64(now.Unix()) + float64(now.Nanosecond())/1e9
    }
    // Confirmed: block height + tx index as decimal
    return float64(height) + float64(idx)/1e9
}
```

**Format**: `integer_part.fractional_precision`

| Context | Example Score | Meaning |
|---------|---------------|---------|
| Confirmed tx | `800000.000001234` | Block 800000, tx index 1234 |
| Mempool tx | `1734567890.123456789` | Unix timestamp 1734567890 with nano precision |

**Why this works**:
- Current unix timestamps (~1.7e9) >> block heights (~800k), so mempool always sorts after confirmed
- float64 has ~15-16 significant digits, plenty of precision
- Precision loss only affects the fractional part (tx index or nanoseconds) - acceptable
- Human-readable when debugging

**TODO**: Consolidate duplicate `HeightScore` implementations:
- `pkg/txo/output.go:85` - returns 0 for height=0
- `pkg/indexer/context.go:18` - returns timestamp for height=0
- Keep single implementation in `pkg/txo/`, delete from `pkg/indexer/`

**Topic Management Keys**:

| Key | Type | Purpose |
|-----|------|---------|
| `topics:whitelist` | Set | Always-active topics |
| `topics:blacklist` | Set | Never-active topics |

Note: Active topics are computed on-demand by querying balances. No `topics:active` storage needed.

**Fee Service**: No persistent storage. Topics are registered at runtime (in-memory map of topicId → address). Balances queried via `pkg/own`.

---

## Existing 1sat-stack Infrastructure

### What We Already Have

| Component | Location | Status |
|-----------|----------|--------|
| Worker framework | `pkg/worker/worker.go` | ✓ Generic queue worker with concurrency |
| Store abstraction | `pkg/store/` | ✓ Sorted sets, search, hash maps |
| BSV21 TopicManager | `pkg/bsv21/topic.go` | ✓ Implements `engine.TopicManager` |
| BSV21 LookupService | `pkg/bsv21/lookup.go` | ✓ Implements `engine.LookupService` |
| BSV21 Indexer | `pkg/bsv21/bsv21.go` | ✓ Parses BSV21 from inscriptions |
| Overlay Services | `pkg/ovr/services.go` | ✓ Dynamic topic registration, sync |
| BEEF Storage | `pkg/beef/` | ✓ Filesystem + JungleBus fallback |
| Engine Storage | `pkg/txo/engine_storage.go` | ✓ Implements `engine.Storage` |
| JungleBus BEEF | `pkg/beef/junglebus.go` | ✓ Fetch BEEF from JungleBus |
| ChainTracker | External (go-chaintracks) | ✓ Already integrated |

### Worker Framework (`pkg/worker/worker.go`)

Already provides:
- Configurable concurrency
- Sorted set queue consumption
- Error handling with callbacks
- Graceful shutdown
- Status logging

```go
type Config struct {
    Store       store.Store
    Key         string        // Sorted set key to consume from
    Concurrency int
    Handler     Handler       // func(ctx, id, score) error
    OnError     ErrorHandler
    PageSize    uint32
    PollDelay   time.Duration
}
```

---

## Current Gaps

### 1. Overlay ↔ BSV21 Wiring (CRITICAL)

**Problem**: In `cmd/server/config.go`, BSV21 is initialized before Overlay, so registration fails:

```go
// Line 267-278: BSV21 checks overlay but overlay isn't created yet
if c.BSV21.Mode != bsv21.ModeDisabled && svc.TXO != nil {
    bsv21Svc, err := c.BSV21.Initialize(...)
    if svc.Overlay != nil {   // <-- ALWAYS NIL HERE
        svc.Overlay.RegisterLookupService("bsv21", svc.BSV21.Lookup)
    }
}

// Line 281-291: Overlay initialized AFTER BSV21
if c.Overlay.Mode != ovr.ModeDisabled && svc.TXO != nil {
    overlaySvc, err := c.Overlay.Initialize(...)
}
```

**Fix**: Reorder initialization - Overlay first, then BSV21, then wire together.

### 2. Missing `GetActiveTopics()` Implementation

The `ovr.Storage` interface requires:
```go
type Storage interface {
    GetActiveTopics(ctx context.Context) map[string]struct{}
}
```

No implementation exists. Need to query:
- `bsv21:whitelist` (always active)
- `bsv21:active` with score > 0 (has balance)
- Exclude `bsv21:blacklist`

### 3. Missing Engine Access Method

`ovr.Services` needs method to access engine for direct submission:
```go
func (s *Services) GetEngine() *engine.Engine
func (s *Services) Submit(ctx context.Context, beef overlay.TaggedBEEF, mode engine.SubmitMode) (overlay.Steak, error)
```

### 4. No JungleBus Subscription Pipeline

Need to port the 3-stage pipeline for historical sync.

---

## Integration Plan

### Phase 1: Fix Overlay Wiring

#### 1.1 Add Engine Access to ovr.Services

**File**: `pkg/ovr/services.go`

```go
// GetEngine returns the overlay engine for direct access
func (s *Services) GetEngine() *engine.Engine {
    return s.Engine
}

// Submit submits a tagged BEEF to the overlay engine
func (s *Services) Submit(ctx context.Context, beef overlay.TaggedBEEF, mode engine.SubmitMode) (overlay.Steak, error) {
    if s.Engine == nil {
        return nil, errors.New("overlay engine not initialized")
    }
    return s.Engine.Submit(ctx, beef, mode, nil)
}

// GetTopics returns list of active topic names
func (s *Services) GetTopics() []string {
    if s.Engine == nil {
        return nil
    }
    managers := s.Engine.ListTopicManagers()
    topics := make([]string, 0, len(managers))
    for name := range managers {
        topics = append(topics, name)
    }
    return topics
}
```

#### 1.2 Fix Initialization Order

**File**: `cmd/server/config.go`

```go
// Initialize creates all services from the configuration
func (c *Config) Initialize(ctx context.Context, logger *slog.Logger) (*Services, error) {
    // ... existing store, pubsub, beef, txo initialization ...
    
    // Initialize Fee Service FIRST (provides ovr.Storage interface)
    var feeService *fees.FeeService
    if c.Fees.Mode != fees.ModeDisabled {
        feeService, err = c.Fees.Initialize(ctx, logger, svc.Store.Store, svc.Chaintracks)
        if err != nil {
            return nil, fmt.Errorf("failed to initialize fees: %w", err)
        }
        svc.Fees = feeService
    }
    
    // Initialize overlay engine (uses fee service for active topics)
    if c.Overlay.Mode != ovr.ModeDisabled && svc.TXO != nil {
        overlaySvc, err := c.Overlay.Initialize(ctx, logger, &ovr.InitializeDeps{
            OutputStore:  svc.TXO.OutputStore,
            ChainTracker: svc.Chaintracks,
            Storage:      feeService, // Fee service implements ovr.Storage
        })
        if err != nil {
            return nil, fmt.Errorf("failed to initialize overlay: %w", err)
        }
        svc.Overlay = overlaySvc
    }
    
    // Initialize BSV21 AFTER overlay and fees
    if c.BSV21.Mode != bsv21.ModeDisabled && svc.TXO != nil {
        bsv21Svc, err := c.BSV21.Initialize(ctx, logger, &bsv21.InitializeDeps{
            OutputStore:  svc.TXO.OutputStore,
            ChainTracker: svc.Chaintracks,
            FeeService:   feeService, // For registering new tokens
        })
        if err != nil {
            return nil, fmt.Errorf("failed to initialize bsv21: %w", err)
        }
        svc.BSV21 = bsv21Svc
        
        // Wire BSV21 to overlay engine
        if svc.Overlay != nil {
            // Register topic factory for per-token topics (tm_{tokenId})
            svc.Overlay.RegisterTopic("bsv21", func(topicName string) (engine.TopicManager, error) {
                // Extract tokenId from topic name (e.g., "tm_abc123_0" -> "abc123_0")
                tokenId := strings.TrimPrefix(topicName, "tm_")
                return bsv21.NewTopicManager(topicName, svc.TXO.OutputStore, []string{tokenId}), nil
            })
            
            // Register global tm_bsv21 topic for token discovery
            svc.Overlay.Engine.RegisterTopicManager("tm_bsv21", bsv21.NewDiscoveryTopicManager(svc.TXO.OutputStore))
            
            // Register lookup service
            svc.Overlay.RegisterLookupService("bsv21", svc.BSV21.Lookup)
            
            logger.Info("BSV21 wired to overlay engine")
        }
    }
    
    // Start overlay sync (activates topics based on fee service)
    if svc.Overlay != nil {
        svc.Overlay.StartSync(ctx)
    }
    
    // ... rest of initialization ...
}
```

### Phase 2: JungleBus Sync Package

Create new package `pkg/jbsync/` with **only** the generic subscriber. Protocol-specific processing (categorization, queue processing) lives in the protocol package.

#### 2.1 Architecture

**Generic Pattern** (most protocols):
```
JungleBus Subscription
     │
     ▼
┌─────────────────────────────────────────────────────────────────┐
│  Subscriber (pkg/jbsync - generic)                              │
│  - Takes subscription ID as parameter                           │
│  - Writes txids to q:{subscriptionId} sorted set                │
│  - Score = height + index/1e9 (for ordering)                    │
│  - Tracks progress in q:progress (subscriptionId → block)       │
│  - Applies 6-block reorg protection before updating progress    │
│  - Batches writes (1000 txids) for efficiency                   │
└─────────────────────────────────────────────────────────────────┘
     │
     ▼
┌─────────────────────────────────────────────────────────────────┐
│  Processor (protocol-specific)                                  │
│  - Consumes from q:{subscriptionId} using pkg/worker            │
│  - Build BEEF, submit to overlay engine via ovr.Submit()        │
│  - Protocol-specific processing logic                           │
└─────────────────────────────────────────────────────────────────┘
```

**BSV21-Specific Pattern** (categorize first):
```
JungleBus Subscription (all BSV21 tokens)
     │
     ▼
┌─────────────────────────────────────────────────────────────────┐
│  Subscriber (pkg/jbsync - generic)                              │
│  - Writes to q:{subscriptionId}                                 │
└─────────────────────────────────────────────────────────────────┘
     │
     ▼
┌─────────────────────────────────────────────────────────────────┐
│  Categorizer (pkg/bsv21 - BSV21-specific)                       │
│  - Consumes from q:{subscriptionId}                             │
│  - Parses BSV21 outputs, extracts tokenIds                      │
│  - Routes to q:tok:{tokenId} per-token queues                   │
│  - Discovers new tokens → updates bsv21:active                  │
└─────────────────────────────────────────────────────────────────┘
     │
     ▼
┌─────────────────────────────────────────────────────────────────┐
│  Token Processors (pkg/bsv21 - BSV21-specific)                  │
│  - One worker per active token                                  │
│  - Consumes from q:tok:{tokenId}                                │
│  - Build BEEF, submit to shared overlay engine                  │
│  - Handle missing inputs, balance tracking                      │
└─────────────────────────────────────────────────────────────────┘
```

The BSV21 categorization step exists because:
- One JungleBus subscription covers ALL BSV21 tokens
- Per-token queues enable ordered processing per token
- Different tokens have different active states (whitelist/blacklist/paid)

#### 2.2 Package Structure

```
pkg/jbsync/
├── keys.go         # Storage key helpers (q: prefix, KeyProgress)
├── config.go       # SubscriberConfig, AddressSyncConfig
├── subscriber.go   # JungleBus topic subscription → queue
└── addrsync.go     # JungleBus address sync → ingest
```

**Shared Progress Tracking** (`sync:progress` hash):
- Topic subscriptions: `{subscriptionId}` → lastBlock
- Address monitoring: `{address}` → lastBlock

All protocol-specific logic (categorization, processing) lives in the protocol package.

#### 2.3 Storage Keys

**File**: `pkg/jbsync/keys.go`

```go
package jbsync

const (
    PfxQueue    = "q:"             // Queue prefix
    KeyProgress = "sync:progress"  // Sorted set: subscriptionId/address → lastBlock
)

// QueueKey returns the queue key for a subscription
func QueueKey(subscriptionId string) string {
    return PfxQueue + subscriptionId
}
```

#### 2.4 Configuration

**File**: `pkg/jbsync/config.go`

```go
package jbsync

// SubscriberConfig holds configuration for a JungleBus subscriber
type SubscriberConfig struct {
    SubscriptionID string // JungleBus subscription/topic ID (for progress tracking)
    QueueName      string // Queue to populate (e.g., "bsv21" → q:bsv21)
    FromBlock      uint64 // Minimum starting block height
    JungleBusURL   string // JungleBus service URL
    BatchSize      int    // Transactions per batch write (default: 1000)
    ReorgDepth     uint32 // Blocks to wait before confirming progress (default: 6)
    EnableMempool  bool   // Subscribe to mempool transactions
}
```

#### 2.5 Subscriber

**File**: `pkg/jbsync/subscriber.go`

Generic JungleBus subscriber:
- Takes `SubscriberConfig` with subscription ID and queue name
- Writes txids to `q:{queueName}` sorted set
- Uses `types.HeightScore(height, idx)` for scoring (see Scoring Convention above)
- Batches writes (configurable, default 1000)
- Progress tracking with reorg protection:
  - On block complete (status 200), calculate safe height
  - `safeHeight = min(blockHeight + 1, chainTip - reorgDepth)`
  - Store in `sync:progress` hash: `subscriptionId → safeHeight`
- If `EnableMempool` is true, mempool txs use `types.HeightScore(0, 0)` (timestamp-based)

```go
// Subscriber manages a JungleBus subscription
type Subscriber struct {
    config       *SubscriberConfig
    store        store.Store
    chainTracker ChainTracker // For getting current tip (reorg protection)
    logger       *slog.Logger
}

// NewSubscriber creates a new subscriber
func NewSubscriber(cfg *SubscriberConfig, store store.Store, ct ChainTracker) *Subscriber

// Start begins the subscription (blocking)
func (s *Subscriber) Start(ctx context.Context) error
```

#### 2.6 BSV21-Specific Implementation

All BSV21-specific logic lives in `pkg/bsv21/`:

**File**: `pkg/bsv21/sync.go`

```go
package bsv21

// SyncConfig holds BSV21 sync configuration  
type SyncConfig struct {
    SubscriptionID     string // JungleBus subscription for BSV21
    FromBlock          uint64 // Starting block (default: 811302)
    CategorizerWorkers int    // Concurrency for categorizer
    TokenWorkers       int    // Concurrency for token processing
}

// SyncServices manages BSV21 JungleBus sync
type SyncServices struct {
    subscriber  *jbsync.Subscriber
    categorizer *worker.Worker  // Categorizes main queue → token queues
    manager     *TokenManager   // Manages per-token processor workers
}

// Categorize parses a transaction and returns token queue names
func (s *SyncServices) Categorize(ctx context.Context, txid string, score float64) error {
    // Load BEEF
    hash, _ := chainhash.NewHashFromHex(txid)
    tx, err := s.beefStore.LoadTx(ctx, hash)
    if err != nil {
        return err
    }
    
    // Extract token IDs from outputs
    seen := make(map[string]struct{})
    for vout, output := range tx.Outputs {
        b := bsv21.Decode(output.LockingScript)
        if b == nil {
            continue
        }
        tokenId := b.Id
        if b.Op == string(bsv21.OpMint) {
            tokenId = (&transaction.Outpoint{Txid: *hash, Index: uint32(vout)}).OrdinalString()
        }
        if _, ok := seen[tokenId]; !ok {
            seen[tokenId] = struct{}{}
            // Add to token queue: q:tok:{tokenId}
            s.store.ZAdd(ctx, jbsync.PfxQueue+"tok:"+tokenId, store.ScoredMember{
                Member: []byte(txid),
                Score:  score,
            })
            // Discover new token
            s.manager.DiscoverToken(ctx, tokenId)
        }
    }
    
    // Remove from main queue
    return s.store.ZRem(ctx, jbsync.QueueKey(s.config.SubscriptionID), []byte(txid))
}

// TokenManager manages per-token processor workers
type TokenManager struct {
    workers  sync.Map // tokenId → *worker.Worker
    overlay  *ovr.Services
    store    store.Store
    // ...
}

// DiscoverToken creates a worker for a newly discovered token if eligible
func (m *TokenManager) DiscoverToken(ctx context.Context, tokenId string) error

// ProcessToken handles a single transaction for a token
func (m *TokenManager) ProcessToken(ctx context.Context, tokenId, txid string, score float64) error {
    // Build BEEF with inputs
    // Submit to shared overlay engine: m.overlay.Submit(ctx, taggedBeef, mode)
    // Handle missing input errors by requeuing dependencies
}
```

### Phase 3: Address Sync & Fee Service

Two separate concerns:
1. **Address Sync** (`pkg/jbsync/addrsync.go`) - Generic address monitoring
2. **Fee Service** (`pkg/fees/`) - Topic fee management using address balances

#### 3.1 Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│  tm_bsv21 TopicManager (global discovery)                       │
│  - Admits ALL deploy+mint operations                            │
│  - On new mint discovered:                                      │
│    fees.Register("tm_{tokenId}", tokenId)                       │
└─────────────────────────────────────────────────────────────────┘
     │
     ▼
┌─────────────────────────────────────────────────────────────────┐
│  Fee Service (pkg/fees - thin layer)                            │
│  - Register(topicId, derivationSeed) → generates address        │
│  - Adds address to jbsync.AddressSync for monitoring            │
│  - Queries balances via pkg/own (owner balance APIs)            │
│  - Manages whitelist/blacklist                                  │
│  - Implements ovr.Storage.GetActiveTopics()                     │
└─────────────────────────────────────────────────────────────────┘
     │
     ▼
┌─────────────────────────────────────────────────────────────────┐
│  Address Sync (pkg/jbsync/addrsync.go - generic)                │
│  - Monitors registered addresses via JungleBus                  │
│  - Ingests transactions into storage (BEEF, outputs)            │
│  - Tracks progress in q:progress (same as topic subscriptions)  │
│  - Doesn't know about fees or topics                            │
└─────────────────────────────────────────────────────────────────┘
     │
     ▼
┌─────────────────────────────────────────────────────────────────┐
│  ovr.syncTopics()                                               │
│  - Calls fees.GetActiveTopics() for active topic list           │
│  - Registers factories for active topics only                   │
└─────────────────────────────────────────────────────────────────┘
```

#### 3.2 Address Sync (`pkg/jbsync/addrsync.go`)

Generic address monitoring - lives in jbsync since it uses the same infrastructure:

```go
// AddressSync monitors addresses and ingests their transactions
type AddressSync struct {
    store        store.Store
    beefStore    beef.Storage
    junglebusURL string
    ingestCtx    *indexer.IngestCtx // For ingesting transactions
    logger       *slog.Logger
}

// AddAddress registers an address for monitoring
func (a *AddressSync) AddAddress(ctx context.Context, address string) error

// RemoveAddress stops monitoring an address
func (a *AddressSync) RemoveAddress(ctx context.Context, address string) error

// SyncAll syncs all registered addresses
func (a *AddressSync) SyncAll(ctx context.Context) error

// SyncAddress syncs a single address from last progress
func (a *AddressSync) SyncAddress(ctx context.Context, address string) error
```

**Progress tracking**: Uses `sync:progress` hash (shared with topic subscriptions)
- Key: address
- Score: last synced block height

#### 3.3 Fee Service (`pkg/fees/`)

Thin layer for topic fee management:

```
pkg/fees/
├── config.go       # Configuration, HD key setup
├── derivation.go   # Address derivation (BIP32)
├── service.go      # Register, GetActiveTopics, whitelist/blacklist
└── types.go        # Constants (output fee, etc.)
```

```go
// FeeService manages paid indexing for topics
type FeeService struct {
    store       store.Store
    addressSync *jbsync.AddressSync
    hdKey       *bip32.ExtendedKey
    outputFee   int64 // Cost per output (default: 1000 sats)
    
    mu     sync.RWMutex
    topics map[string]string // topicId → address (in-memory)
}

// Register registers a topic for fee tracking (called by protocol services)
// Generates address from seed, adds to address sync
func (fs *FeeService) Register(ctx context.Context, topicId, derivationSeed string) (string, error) {
    address := fs.generateAddress(derivationSeed)
    
    fs.mu.Lock()
    fs.topics[topicId] = address
    fs.mu.Unlock()
    
    fs.addressSync.AddAddress(ctx, address)
    return address, nil
}

// GetActiveTopics returns topics with positive balance or whitelisted
// Implements ovr.Storage interface
func (fs *FeeService) GetActiveTopics(ctx context.Context) map[string]struct{} {
    result := make(map[string]struct{})
    blacklist := fs.getBlacklist(ctx)
    
    // Add whitelisted (not blacklisted)
    for _, topic := range fs.getWhitelist(ctx) {
        if _, blocked := blacklist[topic]; !blocked {
            result[topic] = struct{}{}
        }
    }
    
    // Check registered topics for positive balance
    fs.mu.RLock()
    for topicId, address := range fs.topics {
        if _, blocked := blacklist[topicId]; blocked {
            continue
        }
        if _, ok := result[topicId]; ok {
            continue // already whitelisted
        }
        if fs.queryBalance(ctx, address) > 0 {
            result[topicId] = struct{}{}
        }
    }
    fs.mu.RUnlock()
    
    return result
}

// Whitelist/Blacklist management
func (fs *FeeService) AddToWhitelist(ctx context.Context, topicId string) error
func (fs *FeeService) RemoveFromWhitelist(ctx context.Context, topicId string) error
func (fs *FeeService) AddToBlacklist(ctx context.Context, topicId string) error
func (fs *FeeService) RemoveFromBlacklist(ctx context.Context, topicId string) error
```

#### 3.4 Address Derivation (Compatible with bsv21-overlay-1sat-sync)

Must use same HD key and derivation path for backward compatibility:

```go
// HD public key (same as bsv21-overlay-1sat-sync and 1sat-indexer)
const hdKeyString = "xpub661MyMwAqRbcF221R74MPqdipLsgUevAAX4hZP2rywyEeShpbe3v2r9ciAvSGT6FB22TEmFLdUyeEDJL4ekG8s9H5WXbzDQPr6eW1zEYYy9"

// generateAddress generates a payment address for a topic
// derivationSeed is hashed to create BIP32 path components
func (fs *FeeService) generateAddress(derivationSeed string) string {
    // Hash the seed (for BSV21, seed is the tokenId outpoint string)
    hash := sha256.Sum256([]byte(derivationSeed))
    
    // Generate BIP32 path: 21/{first_8_bytes>>1}/{bytes_24_to_28>>1}
    path := fmt.Sprintf("21/%d/%d",
        binary.BigEndian.Uint32(hash[:8])>>1,
        binary.BigEndian.Uint32(hash[24:])>>1)
    
    // Derive key and generate P2PKH address
    ek, _ := fs.hdKey.DeriveChildFromPath(path)
    pubKey, _ := ek.ECPubKey()
    pkHash := hash.Hash160(pubKey.Compressed())
    address, _ := script.NewAddressFromPublicKeyHash(pkHash, true)
    
    return address.AddressString
}
```

#### 3.5 Storage Keys

| Key | Type | Purpose |
|-----|------|---------|
| `topics:whitelist` | Set | Always-active topics |
| `topics:blacklist` | Set | Never-active topics |

**No other persistent storage needed**:
- Topic registrations are in-memory (registered by services at runtime)
- Address balances queried via `pkg/own`
- Active topics computed on-demand in `GetActiveTopics()`

#### 3.6 BSV21 Integration

BSV21 discovers tokens and registers them with the fee service:

```go
// In pkg/bsv21/lookup.go

// When a new mint is discovered via tm_bsv21:
func (l *Lookup) OutputAdmittedByTopic(ctx context.Context, payload *engine.OutputAdmittedByTopic) error {
    // ... existing logic ...
    
    if b.Op == string(bsv21.OpMint) {
        tokenId := outpoint.OrdinalString()
        topicId := "tm_" + tokenId
        
        // Register with fee service (BSV21-specific: uses tokenId as derivation seed)
        if l.feeService != nil {
            l.feeService.Register(ctx, topicId, tokenId)
        }
    }
    
    // ... rest of logic ...
}
```

---

## Simplifications from Original

### 1. Single Shared Engine

**Original**: Each token worker creates its own `engine.Engine` instance.

**Simplified**: All workers submit to the shared engine via `ovr.Services.Submit()`. The engine has dynamically registered topic managers.

### 2. Use Existing Worker Framework

**Original**: Custom worker loop in `tokenWorker()`.

**Simplified**: Use `pkg/worker.Worker` with custom handler.

### 3. Leverage Existing BEEF Storage

**Original**: Separate BEEF storage setup.

**Simplified**: Use `pkg/beef.Services` already configured with JungleBus fallback.

### 4. Unified Store

**Original**: Separate Redis clients for queue vs events.

**Simplified**: Use `pkg/store.Store` for everything.

---

## Migration Checklist

### Phase 0: Scoring Consolidation
- [ ] Create `pkg/types/score.go` with unified `HeightScore()` function
- [ ] Update `pkg/txo/output.go` to use `types.HeightScore()`
- [ ] Update `pkg/indexer/context.go` to use `types.HeightScore()`
- [ ] Remove duplicate implementations

### Phase 1: Overlay Wiring
- [ ] Add `GetEngine()`, `Submit()`, `GetTopics()` to `pkg/ovr/services.go`
- [ ] Fix initialization order in `cmd/server/config.go`
- [ ] Register BSV21 topic factory and lookup service
- [ ] Wire FeeService as `ovr.Storage` (provides `GetActiveTopics()`)
- [ ] Call `ovr.StartSync()` to begin topic synchronization

### Phase 2: Generic JungleBus Sync (`pkg/jbsync/`)
- [ ] Create `pkg/jbsync/keys.go` - queue key helpers (`q:` prefix), progress key (`sync:progress`)
- [ ] Create `pkg/jbsync/config.go` - SubscriberConfig, AddressSyncConfig
- [ ] Create `pkg/jbsync/subscriber.go` - generic JungleBus topic → queue
  - [ ] Batched writes to `q:{queueName}` (queue name from config)
  - [ ] Progress tracking in `sync:progress` hash (by subscription ID)
  - [ ] Mempool support (same queue, timestamp score)
- [ ] Create `pkg/jbsync/addrsync.go` - generic address monitoring
  - [ ] AddAddress/RemoveAddress for registration
  - [ ] SyncAddress fetches from JungleBus, ingests transactions
  - [ ] Progress tracking in `sync:progress` (shared with subscriptions)

### Phase 3: Fee Service (`pkg/fees/`)
- [ ] Create `pkg/fees/config.go` - HD key setup, configuration
- [ ] Create `pkg/fees/derivation.go` - BIP32 address generation (compatible with bsv21-overlay-1sat-sync)
- [ ] Create `pkg/fees/service.go`:
  - [ ] In-memory topic → address registration
  - [ ] Register(topicId, seed) → generate address, add to AddressSync
  - [ ] GetActiveTopics() → query balances via pkg/own, apply whitelist/blacklist
  - [ ] Whitelist/blacklist management (topics:whitelist, topics:blacklist)
- [ ] Implement `ovr.Storage` interface in FeeService
- [ ] Wire FeeService + AddressSync into server initialization

### Phase 4: BSV21-Specific Sync (`pkg/bsv21/`)
- [ ] Create global `tm_bsv21` TopicManager for token discovery
- [ ] Update `Lookup.OutputAdmittedByTopic()` to register new tokens with FeeService
- [ ] Create `pkg/bsv21/sync.go` with:
  - [ ] `Categorize()` - extract tokenIds from BSV21 outputs
  - [ ] `TokenManager` - manage per-token processor workers
  - [ ] `ProcessToken()` - process token queue, submit to overlay
- [ ] Wire BSV21 sync into server initialization
- [ ] Add BSV21 sync configuration to main config

---

## Resolved Design Decisions

1. **Topic Naming**: Two-tier approach
   - `tm_bsv21` - Global topic for ALL deploy+mint operations (token discovery)
   - `tm_{tokenId}` - Per-token topics for transfers/burns (only for active tokens)

2. **Fee Model**: Generic fee service (`pkg/fees/`)
   - Fee service is protocol-agnostic
   - BSV21 registers tokens with fee service on discovery
   - Fee service implements `ovr.Storage.GetActiveTopics()`

3. **Mempool Processing**: Same queue, different score
   - `EnableMempool` config flag (opt-in)
   - Mempool txs go to same queue with timestamp-based score
   - Historical sync completes before mempool (timestamps >> block heights)

4. **Scoring**: Unified `txo.HeightScore()` with decimal format
   - Confirmed: `height + idx/1e9`
   - Mempool: `unixSeconds + nano/1e9`

---

## Resolved Questions

**Historical Sync**: 
- `FromBlock` config specifies the protocol's activation block (minimum starting height)
- If progress exists and is ahead of `FromBlock`, resume from progress
- Otherwise start from `FromBlock`
- Example: BSV21 uses 811302, other protocols use their activation block

**Fee Service Sync**:
- No sync until `Register(topicId, derivationSeed)` is called
- Registration adds address to sync list
- Periodic background sync for all registered addresses
- Active workers can trigger more frequent syncs for their own addresses

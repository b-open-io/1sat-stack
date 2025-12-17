# Overlay Engine Integration Plan

## Overview

Wire the overlay engine from `go-overlay-services` into `1sat-stack`. This provides:
- Standard overlay protocol routes (submit, lookup, GASP sync)
- Topic-based transaction filtering and admission
- Peer-to-peer synchronization via GASP

**Note:** Topic Manager and Lookup Service implementations are documented separately in `TOPIC_MANAGERS.md`.

## Key Concepts

### Indexers vs Topic Managers

**Indexers** (existing `pkg/indexer/parse/*`):
- Parse transaction data
- Extract structured information
- Filter by presence of data (return nil if data not found)
- Shared parsing logic reusable by topic managers

**Topic Managers** (overlay pattern):
- USE parsers to understand the data
- Apply topic-specific admission logic
- Decide if transaction fits the confines of a specific topic
- More specific filtering than just "data present"

Example: BSV21 Topic Manager parses the BSV21 data (using shared parsing), then applies admission logic like "is this a valid token transfer for tokens I'm tracking?"

## The Overlay Flow

```
HTTP POST /submit (x-topics: ["bsv21", "mytoken"])
    │
    ▼
Engine.Submit(TaggedBEEF)
    │
    ▼
┌─────────────────────────────────────────────────────┐
│ For each topic:                                      │
│  1. TopicManager.IdentifyAdmissibleOutputs(beef)    │
│     └─ Parse data, apply admission logic            │
│     └─ Returns: OutputsToAdmit, AncillaryTxids      │
│  2. Storage.MarkUTXOsAsSpent()                      │
│  3. LookupService.OutputSpent()                     │
│  4. Storage.InsertOutputs()                         │
│  5. LookupService.OutputAdmittedByTopic()           │
│     └─ Extract data, create events, save indexes    │
└─────────────────────────────────────────────────────┘
    │
    ▼
Return STEAK (per-topic admission results)
```

## Implementation Plan

### Phase 1: Initialize Overlay Engine

Use `engine.NewEngine()` directly from `go-overlay-services`.

**File to modify:** `cmd/server/config.go`

```go
import "github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"

// In Initialize():
if c.Overlay.Mode != "disabled" {
    svc.OverlayEngine = engine.NewEngine(engine.Engine{
        Managers:       buildTopicManagers(...),
        LookupServices: buildLookupServices(...),
        Storage:        svc.TXO.OutputStore,  // Already implements engine.Storage
        ChainTracker:   svc.Chaintracks,
    })
}
```

### Phase 2: Register Overlay Routes

Add overlay routes from go-overlay-services to the server.

**File to modify:** `cmd/server/config.go`

```go
import overlayserver "github.com/bsv-blockchain/go-overlay-services/pkg/server"

// In Services struct:
type Services struct {
    // ... existing fields
    OverlayEngine *engine.Engine
}

// In Initialize():
if c.Overlay.Mode != "disabled" {
    svc.OverlayEngine = overlay.NewEngine(overlay.EngineConfig{
        Storage:      svc.TXO.OutputStore,
        ChainTracker: svc.Chaintracks,
        Managers:     buildTopicManagers(...),
        Lookups:      buildLookupServices(...),
    })
}

// In RegisterRoutes():
if svc.OverlayEngine != nil {
    overlayserver.RegisterRoutes(app, &overlayserver.RegisterRoutesConfig{
        Engine: svc.OverlayEngine,
        // ... other config
    })
}
```

### Phase 3: Update Storage for HeightScore

Modify `InsertOutputs` to use HeightScore from MerklePath instead of timestamp.

**File to modify:** `pkg/txo/engine_storage.go`

```go
func (s *OutputStore) InsertOutputs(ctx context.Context, topic string, txid *chainhash.Hash, outputVouts []uint32, outpointsConsumed []*transaction.Outpoint, beefData []byte, ancillaryTxids []*chainhash.Hash) error {
    // Parse BEEF to get MerklePath
    _, tx, _, err := transaction.ParseBeef(beefData)
    if err != nil {
        return err
    }

    // Calculate score from MerklePath if available, otherwise use timestamp
    var score float64
    if tx.MerklePath != nil {
        height := tx.MerklePath.BlockHeight
        var idx uint64
        for _, leaf := range tx.MerklePath.Path[0] {
            if leaf.Hash != nil && leaf.Hash.Equal(*txid) {
                idx = leaf.Offset
                break
            }
        }
        score = HeightScore(height, idx)
    } else {
        score = float64(time.Now().UnixNano())
    }

    // ... rest of implementation using score
}
```

### Phase 4: Configuration

Add overlay configuration options.

**File to modify:** `cmd/server/config.go`

```go
type OverlayConfig struct {
    Mode   string `mapstructure:"mode"` // disabled, embedded
    Routes struct {
        Enabled bool   `mapstructure:"enabled"`
        Prefix  string `mapstructure:"prefix"` // default: /overlay
    } `mapstructure:"routes"`
}
```

## Existing Infrastructure

The following already exists and will be reused:

**Storage Interface** (`pkg/txo/engine_storage.go`):
- `OutputStore` implements `engine.Storage`
- `InsertOutputs`, `FindOutputs`, `MarkUTXOsAsSpent`, etc.

**Example Topic Manager** (`pkg/bsv21/topic.go`):
- `TopicManager` for BSV21 tokens
- Shows pattern for admission logic

**Example Lookup Service** (`pkg/bsv21/lookup.go`):
- `Lookup` for BSV21 tokens
- Shows pattern for data extraction and event creation

## Files Summary

**Modified files:**
- `cmd/server/config.go` - Add overlay engine init, config, and routes
- `pkg/txo/engine_storage.go` - HeightScore from MerklePath

## Design Decisions

1. **Scoring** - Use HeightScore from the start for confirmed transactions. Extract height from BEEF's MerklePath in InsertOutputs.

2. **Parsers are shared** - Topic Managers and Lookup Services reuse parsing logic from `pkg/indexer/parse/*`. No duplication of parsing code.

3. **Arcade unchanged** - Remains external. Handles broadcast to network, consumed by but not the entry point for overlay flow.

4. **GASP sync** - Comes free with overlay engine. Handles peer-to-peer synchronization.

## Next Steps

After this infrastructure is in place:
1. Define specific Topic Managers (see `TOPIC_MANAGERS.md`)
2. Define specific Lookup Services
3. Wire them into the engine configuration

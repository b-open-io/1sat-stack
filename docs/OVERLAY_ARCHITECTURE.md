# Overlay Architecture

This document describes the overlay system architecture in 1sat-stack.

## Overview

The overlay system provides topic-based transaction filtering and indexing using the BSV Overlay Services protocol. Transactions are submitted with topic tags, validated by topic managers, and indexed by lookup services.

## Components

### Overlay Engine (`go-overlay-services`)

The external `go-overlay-services` package provides:
- `engine.Engine` - Coordinates topic managers and lookup services
- `engine.Storage` - Interface for output storage (implemented by `OutputStore`)
- `engine.TopicManager` - Interface for admission logic
- `engine.LookupService` - Interface for indexing admitted outputs

### OutputStore (`pkg/txo/`)

Implements `engine.Storage` interface. Provides:
- Topic membership tracking (`z:tp:{topic}`)
- Applied transaction tracking (`z:tp:{topic}:tx`)
- Event indexing (`z:{event}`)
- Spend tracking (`h:spnd`)
- Output data storage (`h:{outpoint}`)

### Topic Managers (`pkg/topic/`)

Decide which outputs to admit to a topic:

| Topic | Manager | Admits |
|-------|---------|--------|
| `tm_bsv21` | `Bsv21DiscoveryTopicManager` | All BSV21 deploy operations |
| `tm_{tokenId}` | `Bsv21ValidatedTopicManager` | Valid transfers for specific token |
| `tm_1sat` | `OneSatTopicManager` | All outputs (catch-all) |

### Lookup Services (`pkg/lookup/`)

Index admitted outputs for querying:

| Service | Events Indexed |
|---------|----------------|
| `BSV21Lookup` | `id:`, `sym:`, `p2pkh:`, `cos:`, `ltm:`, `pow20:`, `list:` |
| `OneSatLookup` | `own:`, `txid:`, tag-specific events |

---

## Data Flow

### Submit Flow

```
Client/Worker
     │
     │ Overlay.Submit(TaggedBEEF{Topics: ["tm_xxx"]})
     ▼
Engine.Submit()
     │
     ├─→ TopicManager.IdentifyAdmissibleOutputs()
     │         │
     │         └─→ Returns OutputsToAdmit, CoinsToRetain
     │
     ├─→ Storage.InsertOutputs()
     │         │
     │         ├─→ z:tp:{topic}  ← outpoint (ZAdd)
     │         ├─→ h:{outpoint}  ← deps, inputs (HSet)
     │         └─→ BEEF storage  ← transaction data
     │
     ├─→ Storage.InsertAppliedTransaction()
     │         │
     │         └─→ z:tp:{topic}:tx  ← txid (ZAdd)
     │
     └─→ LookupService.OutputAdmittedByTopic()
               │
               └─→ OutputStore.SaveEvents()
                         │
                         ├─→ h:{outpoint}  ← ev, dt:{tag} (HSet)
                         └─→ z:{event}     ← outpoint (ZAdd)
```

### Query Flow

```
Client
     │
     │ GET /api/lookup/{service}?query=...
     ▼
LookupService.Lookup()
     │
     └─→ OutputStore.SearchOutputs()
               │
               ├─→ z:{event}      ← Search by event keys
               ├─→ h:spnd         ← Filter spent (optional)
               └─→ h:{outpoint}   ← Load output data
```

---

## Storage Keys

All keys are documented in `pkg/store/KEYS.md`. Summary:

### Written by Engine (via OutputStore)

| Key | Operation | When |
|-----|-----------|------|
| `z:tp:{topic}` | ZAdd outpoint | InsertOutputs |
| `z:tp:{topic}:tx` | ZAdd txid | InsertAppliedTransaction |
| `h:{outpoint}` `dp:{topic}` | HSet | InsertOutputs (if deps) |
| `h:{outpoint}` `in:{topic}` | HSet | InsertOutputs (if inputs consumed) |
| `h:spnd` | HSet | MarkUTXOsAsSpent |

### Written by Lookup Services (via OutputStore.SaveEvents)

| Key | Operation | When |
|-----|-----------|------|
| `h:{outpoint}` `ev` | HSet | OutputAdmittedByTopic |
| `h:{outpoint}` `dt:{tag}` | HSet | OutputAdmittedByTopic |
| `z:{event}` | ZAdd outpoint | OutputAdmittedByTopic |

---

## Topic Registration

Topics are registered dynamically with the overlay engine:

```go
// Register topic manager factory
overlay.RegisterTopicManagerFactory("bsv21", func(topic string) engine.TopicManager {
    return topic.NewBsv21ValidatedTopicManager(topic, storage, logger)
})

// Activate a topic (creates manager instance)
overlay.ActivateTopic("tm_abc123...i0")
```

The Token Manager (`pkg/bsv21/manager.go`) registers topics for active tokens during its lifecycle check.

---

## Score Consistency

All scores use `types.HeightScore()`:
- **Confirmed**: `blockHeight + blockIdx/1e9`
- **Unconfirmed**: `time.Now().UnixNano() / 1e9`

Helper functions:
- `types.HeightScore(height, idx)` - Build from block position
- `types.ScoreFromTx(tx, txid)` - Extract from parsed transaction
- `types.ScoreFromBeef(beef)` - Extract from BEEF bytes

---

## Related Documentation

- `pkg/store/KEYS.md` - Complete key reference
- `docs/BSV21_PIPELINE.md` - BSV21-specific processing pipeline

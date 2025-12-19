# Overlay Engine Integration Plan

## Overview

Wire the overlay engine from `go-overlay-services` into `1sat-stack`. This provides:
- Standard overlay protocol routes (submit, lookup, GASP sync)
- Topic-based transaction filtering and admission
- Peer-to-peer synchronization via GASP

## Current State (Completed)

The following infrastructure is in place:

- **Overlay Engine** initialized in `pkg/ovr/config.go`
- **Storage Interface** (`pkg/txo/engine_storage.go`) implements `engine.Storage`
- **Routes** registered in `cmd/server/config.go`
- **Dynamic Topic Registration** (`pkg/ovr/services.go`)

## Package Structure (Refactored)

```
pkg/
├── parse/           # Output-level parsers using go-templates
│   ├── parse.go     # ParseContext, ParseResult, GetData helper
│   ├── p2pkh.go     # P2PKH parser
│   ├── lock.go      # Lock contract parser
│   ├── inscription.go # Inscription parser
│   ├── bsv21.go     # BSV21 token parser
│   ├── ordlock.go   # OrdLock listing parser
│   ├── cosign.go    # Cosign parser
│   ├── shrug.go     # Shrug token parser
│   └── bitcom.go    # Bitcom + B, MAP, AIP, BAP, Sigma parsers
├── topic/           # Topic managers (admission logic)
│   ├── registry.go  # Auto-registration registry
│   ├── onesat.go    # 1Sat topic manager (catch-all)
│   └── bsv21.go     # BSV21 topic manager
└── lookup/          # Lookup services (indexing/querying)
    ├── registry.go  # Auto-registration registry
    ├── onesat.go    # 1Sat lookup service (runs all parsers)
    └── bsv21.go     # BSV21 lookup service
```

## Parsers (`pkg/parse/`)

Output-level parsers use `go-templates` decoders directly.

### Parser Architecture

Each parser:
- Takes a `*ParseContext` containing output metadata and accumulated results
- Returns a `*ParseResult` with tag, data, events, and owners
- Uses `go-templates` `Decode` functions directly (no reimplementation)

```go
// ParseContext holds accumulated parse results and output metadata.
type ParseContext struct {
    Outpoint      *transaction.Outpoint
    LockingScript []byte
    Satoshis      uint64
    Results       map[string]*ParseResult  // Accumulated results keyed by tag
}

// ParseResult holds the output of parsing a single transaction output.
type ParseResult struct {
    Tag    string          // Identifier (e.g., "p2pkh", "insc")
    Data   any             // Parsed data structure from go-templates
    Events []string        // Events to index
    Owners []*types.PKHash // Owners associated with this output
}
```

### Implemented Parsers

| Parser | Tag | Template | Dependencies |
|--------|-----|----------|--------------|
| `ParseP2PKH` | `p2pkh` | `p2pkh.Decode` | none |
| `ParseLock` | `lock` | `lockup.Decode` | none |
| `ParseInscription` | `insc` | `inscription.Decode` | satoshis == 1 |
| `ParseBSV21` | `bsv21` | `bsv21.Decode` | outpoint for deploy |
| `ParseOrdLock` | `ordlock` | `ordlock.Decode` | satoshis == 1 |
| `ParseCosign` | `cosign` | `cosign.Decode` | none |
| `ParseShrug` | `shrug` | `shrug.Decode` | outpoint for deploy |

### Bitcom Parsers (Cascading)

Bitcom parsing is split into a base parser and sub-parsers that read from the base:

| Parser | Tag | Template | Dependencies |
|--------|-----|----------|--------------|
| `ParseBitcom` | `bitcom` | `bitcom.Decode` | none (run first) |
| `ParseB` | `b` | `bitcom.DecodeB` | requires `bitcom` |
| `ParseMAP` | `map` | `bitcom.DecodeMap` | requires `bitcom` |
| `ParseAIP` | `aip` | `bitcom.DecodeAIP` | requires `bitcom` |
| `ParseBAP` | `bap` | `bitcom.DecodeBAP` | requires `bitcom` |
| `ParseSigma` | `sigma` | `bitcom.DecodeSIGMA` | requires `bitcom` |

Sub-parsers access the base bitcom data via:
```go
bc := GetData[bitcom.Bitcom](ctx, TagBitcom)
```

## Topic Managers (`pkg/topic/`)

### Auto-Registration

Topic managers self-register via `init()`:

```go
// pkg/topic/registry.go
var Registry = map[string]Factory{}

func init() {
    Register("1sat", func(topicName string, storage engine.Storage, logger *slog.Logger) (engine.TopicManager, error) {
        return NewOneSatTopicManager(), nil
    })
    Register("bsv21", func(topicName string, storage engine.Storage, logger *slog.Logger) (engine.TopicManager, error) {
        return NewBSV21TopicManager(topicName, storage, nil), nil
    })
}
```

### OneSatTopicManager

Admits ALL outputs (catch-all behavior):

```go
func (tm *OneSatTopicManager) IdentifyAdmissibleOutputs(ctx context.Context, beefBytes []byte, previousCoins []uint32) (overlay.AdmittanceInstructions, error) {
    _, tx, _, err := transaction.ParseBeef(beefBytes)
    // ... error handling ...
    
    // Admit all outputs
    admit.OutputsToAdmit = make([]uint32, len(tx.Outputs))
    for i := range tx.Outputs {
        admit.OutputsToAdmit[i] = uint32(i)
    }
    return admit, nil
}
```

### BSV21TopicManager

Validates token transfers and admits only valid BSV21 outputs.

## Lookup Services (`pkg/lookup/`)

### Auto-Registration

Lookup services self-register via `init()`:

```go
// pkg/lookup/registry.go
var Registry = map[string]Factory{}

func init() {
    Register("1sat", func(storage *txo.OutputStore, logger *slog.Logger) (engine.LookupService, error) {
        return NewOneSatLookup(storage, logger), nil
    })
    Register("bsv21", func(storage *txo.OutputStore, logger *slog.Logger) (engine.LookupService, error) {
        return NewBSV21Lookup(storage), nil
    })
}
```

### OneSatLookup

Runs all parsers when an output is admitted:

```go
func (l *OneSatLookup) OutputAdmittedByTopic(ctx context.Context, payload *engine.OutputAdmittedByTopic) error {
    // Parse BEEF, create ParseContext
    parseCtx := parse.NewParseContext(outpoint, output.LockingScript.Bytes(), output.Satoshis)

    // Run all parsers
    for _, parser := range l.parsers {
        if result := parser(parseCtx); result != nil {
            parseCtx.AddResult(result)
        }
    }

    // Collect events with tag prefixes, store to database
    // ...
}
```

### BSV21Lookup

Indexes BSV21 token data with balance tracking and mint caching.

## Server Configuration

The overlay engine and lookup services are wired in `cmd/server/config.go`:

```go
// Initialize overlay engine
overlaySvc, err := c.Overlay.Initialize(ctx, logger, &ovr.InitializeDeps{
    OutputStore:  svc.TXO.OutputStore,
    ChainTracker: svc.Chaintracks,
})
svc.Overlay = overlaySvc

// Register 1Sat lookup service
onesatLookup := lookup.NewOneSatLookup(svc.TXO.OutputStore, logger)
svc.Overlay.RegisterLookupService("1sat", onesatLookup)

// Register BSV21 lookup service (after BSV21 init)
svc.Overlay.RegisterLookupService("bsv21", svc.BSV21.Lookup)
```

## Design Decisions

1. **Parsers use go-templates directly** - No reimplementation of parsing logic.

2. **ParseContext enables cascading** - Parsers can read results from previous parsers via `GetData[T](ctx, tag)`.

3. **Data types from go-templates** - ParseResult.Data contains actual types from go-templates.

4. **Events are tag-relative** - Events returned by parsers don't include the tag prefix. The lookup service adds the prefix when saving.

5. **1Sat admits everything** - The 1sat topic is a catch-all. Specialized topics (BSV21, BAP, BSocial) can coexist with their own admission logic.

6. **Separated packages** - `parse/`, `topic/`, `lookup/` provide clear separation of concerns.

## Future Work

### Additional Topic Managers

From `bsocial-overlay/`:
- **BAP Topic Manager** - Bitcoin Attestation Protocol identity management
- **BSocial Topic Manager** - Social posts, likes, follows, etc.

These will have their own admission logic and lookup services, but can coexist with the 1sat catch-all topic.

## Related Documentation

- `TOPIC_MANAGERS.md` - Specific topic manager implementations
- `../go-templates/` - Template decoders used by parsers
- `../bsocial-overlay/` - BAP and BSocial topic manager reference implementations

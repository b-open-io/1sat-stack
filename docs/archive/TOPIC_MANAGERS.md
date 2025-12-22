# Topic Managers and Lookup Services

This document defines the specific Topic Manager and Lookup Service implementations for 1sat-stack overlays.

**Prerequisites:** The overlay engine infrastructure must be wired in first (see `OVERLAY_REFACTOR_PLAN.md`).

## Relationship to Indexers

Existing indexers in `pkg/indexer/parse/*` provide **parsing logic**:
- Extract structured data from transaction outputs
- Determine if data of a certain type is present

Topic Managers **use** this parsing logic but add **admission logic**:
- Is this transaction relevant to THIS specific topic?
- What are the dependencies (AncillaryTxids)?
- Which inputs should be retained vs removed?

## Interfaces

### TopicManager Interface

```go
type TopicManager interface {
    // IdentifyAdmissibleOutputs - Parse and apply admission logic
    // previousCoins: indexes of inputs that exist in this topic's storage
    IdentifyAdmissibleOutputs(ctx context.Context, beef []byte, previousCoins []uint32) (overlay.AdmittanceInstructions, error)

    // IdentifyNeededInputs - Which inputs are required for validation
    IdentifyNeededInputs(ctx context.Context, beef []byte) ([]*transaction.Outpoint, error)

    GetDocumentation() string
    GetMetaData() *overlay.MetaData
}
```

### LookupService Interface

```go
type LookupService interface {
    OutputAdmittedByTopic(ctx context.Context, payload *OutputAdmittedByTopic) error
    OutputSpent(ctx context.Context, payload *OutputSpent) error
    OutputNoLongerRetainedInHistory(ctx context.Context, outpoint *transaction.Outpoint, topic string) error
    OutputEvicted(ctx context.Context, outpoint *transaction.Outpoint, topic string) error
    OutputBlockHeightUpdated(ctx context.Context, txid *chainhash.Hash, blockHeight uint32, blockIdx uint64) error
    Lookup(ctx context.Context, question *lookup.LookupQuestion) (*lookup.LookupAnswer, error)
    GetDocumentation() string
    GetMetaData() *overlay.MetaData
}
```

## Existing Implementation: BSV21

Reference implementation in `pkg/bsv21/`:

**Topic Manager** (`topic.go`):
- Parses BSV21 data from outputs
- Validates token balances (tokens in >= tokens out)
- Tracks AncillaryTxids for dependency resolution
- Filters by token whitelist if configured

**Lookup Service** (`lookup.go`):
- Extracts BSV21 data on admission
- Creates events: `id:{tokenId}`, `sym:{symbol}`, `p2pkh:{addr}:{id}`
- Saves to storage via `SaveEvents()`

---

## Topic Definitions

*To be refined as we discuss each topic in detail*

### BSV21 (existing)
- **Location:** `pkg/bsv21/topic.go`, `pkg/bsv21/lookup.go`
- **Parser:** `pkg/indexer/parse/onesat/bsv21.go`
- **Admission Logic:** Token balance validation, whitelist filtering
- **Status:** Already implemented

---

### [Next topic to discuss]

*Details to be added after discussion*


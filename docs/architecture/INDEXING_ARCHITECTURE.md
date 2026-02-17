# Indexing Architecture Plan

## Current State: Three Output Save Operations

### 1. SaveOutput (main indexer flow)
**File:** `pkg/txo/output_store.go:53-133`
**Called from:** `indexer/indexer.go:178` via `IndexContext.Save()`

**Data saved:**
| Key | Field | Data |
|-----|-------|------|
| `h:{outpoint}` | `ev` | Events JSON array (custom events, own:xxx) |
| `h:{outpoint}` | `ms` | Merkle state (block height + block idx) |
| `h:{outpoint}` | `dt:{tag}` | Tag-specific data |
| `h:sats` | {outpoint} | Satoshis (uint64) |
| `z:ev:{event}` | {outpoint} | Sorted set for each event |

### 2. SaveEvents (lookup service flow)
**File:** `pkg/txo/output_store.go:135-184`
**Called from:** `lookup/bsv21.go:136` via `OutputAdmittedByTopic()`

**Data saved:**
| Key | Field | Data |
|-----|-------|------|
| `h:{outpoint}` | `ev` | Events JSON array |
| `h:{outpoint}` | `dt:{tag}` | Tag-specific data |
| `z:ev:{event}` | {outpoint} | Sorted set for each event |

**NOT saved:**
- `h:sats` - satoshis
- `h:{outpoint}` `ms` - merkle state

### 3. InsertOutputs (overlay/engine flow)
**File:** `pkg/txo/engine_storage.go:19-65`
**Called from:** overlay engine via `engine.Storage` interface

**Data saved:**
| Key | Field | Data |
|-----|-------|------|
| BEEF storage | - | Transaction BEEF |
| `h:{outpoint}` | `in:{topic}` | Inputs consumed for topic |
| `h:{outpoint}` | `dp:{topic}` | Deps (ancillary txids) for topic |
| `z:tp:{topic}` | {outpoint} | Topic membership sorted set |

**NOT saved:**
- Events, Merkle state, Tag data, Satoshis, Event sorted sets

---

## Current State: Spend Operations

### SaveSpend (main indexer flow)
**File:** `pkg/txo/output_store.go:218-226`

- `h:spnd` -> {outpoint}: Spend txid
- `z:ev.spnt:{event}` -> {outpoint}: Spent event sorted sets

### MarkUTXOsAsSpent (overlay/engine flow)
**File:** `pkg/txo/engine_storage.go:263-277`

- `h:spnd` -> {outpoint}: Spend txid only
- Does NOT update spent event sorted sets

---

## Current Flow Sequences

### Main Indexing Flow
```
JungleBus (subscription_ids from indexer.sync config) -> q:ingest -> IngestSync -> IngestTxid -> ParseTx -> Save()
                                                                                                        |-- SaveOutput (for each output)
                                                                                                        +-- SaveSpend (for each input)
```

Ingest subscription IDs are set via `ONESAT_INDEXER_SYNC_SUBSCRIPTION_IDS` env var (comma-separated).
Multiple subscriptions all feed the same `q:ingest` queue.

### BSV21/Overlay Flow
```
JungleBus (subscription_id from bsv21.sync config) -> q:bsv21 -> dispatcher -> overlay.Submit()
                                                                                |-- InsertOutputs (topic data only)
                                                                                +-- OutputAdmittedByTopic -> SaveEvents (events + tag data)
```

### BAP/BSocial/OPNS Overlay Flow
```
JungleBus (subscription_id from {module}.sync config) -> q:{module} -> OverlaySync -> BEEF -> overlay.Submit()
```

Each overlay module (BAP, BSocial, OPNS) has its own subscription_id set via env var
(e.g., `ONESAT_BAP_SYNC_SUBSCRIPTION_ID`). The generic `OverlaySync` worker drains the
queue, builds BEEF, and submits through the overlay engine.

---

## Engine.Submit Hook Points

Hooks called from `go-overlay-services/pkg/core/engine/engine.go:Submit()`:

**Storage interface (implemented by OutputStore):**
| Order | Method | Purpose |
|-------|--------|---------|
| 1 | `DoesAppliedTransactionExist` | Skip if duplicate |
| 2 | `FindOutputs` | Get inputs being spent |
| 3 | `MarkUTXOsAsSpent` | Mark inputs spent |
| 4 | `InsertOutputs` | Insert outputs for topic |
| 5 | `UpdateConsumedBy` | Track consumed-by |
| 6 | `InsertAppliedTransaction` | Record tx applied |

**LookupService interface (implemented by BSV21Lookup):**
| Order | Method | Purpose |
|-------|--------|---------|
| A | `OutputSpent` | Notify about spent outputs |
| B | `OutputAdmittedByTopic` | Notify about admitted outputs |

**Flow:**
```
Submit()
  -> [1] DoesAppliedTransactionExist
  -> [2] FindOutputs
  -> TopicManager.IdentifyAdmissibleOutputs
  -> [3] MarkUTXOsAsSpent
  -> [A] LookupService.OutputSpent (per spent input)
  -> [4] InsertOutputs (has full BEEF)
  -> [B] LookupService.OutputAdmittedByTopic (per admitted output)
  -> [5] UpdateConsumedBy
  -> [6] InsertAppliedTransaction
```

---

## Proposed Solution

**Principle:** All base indexing goes through main ingestion flow. Overlay flow handles only topic-specific operations and validated token balance tracking.

### 1. Trigger main indexing from `InsertOutputs`

`InsertOutputs` receives full BEEF -> call `IngestTx` to run main indexing:
- Saves satoshis, events, owners, merkle state via `SaveOutput`
- Saves spends via `SaveSpend`

### 2. Rewrite BSV21 Lookup Service

The BSV21 lookup service handles **topic-scoped, validated lookups only**:

| Lookup | Source |
|--------|--------|
| Token details (id, op, amt, sym, dec, icon) | Main indexer tag data (NOT topic-scoped) |
| Block data (`/:tokenId/blk/:height`) | Topic membership ZSet score (`z:tp:tm_{tokenId}`) |
| Balance/History/Unspent by address | **BSV21 Lookup Service** (topic-scoped, validated only) |

**BSV21 Lookup Service Scope:**
- Only handles `{topicId}:{lockType}:{address}` lookups (topicId = `tm_{tokenId}`)
- Only validated outputs (admitted by overlay) get indexed
- Authoritative UTXO set (removes spent, not just marks)

**New behavior:**

`OutputAdmittedByTopic`:
- Add outpoint to ZSet `{topicId}:{lockType}:{address}` with score
- Store amount for efficient balance summing

`OutputSpent`:
- Remove from ZSet
- Remove amount

**Key format:** `{topicId}:{lockType}:{address}` standardizes for any overlay topic (e.g., `tm_abc123_0:p2pkh:1xyz...`)

**Optimization:** Overlay processes in spending order -> ZSet is authoritative UTXO set. Balance/Unspent queries need no spent filtering.

### 3. Keep topic-specific operations in `InsertOutputs`

- Topic membership sorted set (`z:tp:{topic}`)
- Deps tracking (`dp:{topic}`)
- Inputs consumed (`in:{topic}`)

### 4. Cleanup

- Remove `txid:` event from SaveOutput (currently at line 61)

---

## Files to Modify

| File | Change |
|------|--------|
| `pkg/txo/output_store.go` | Remove `txid:` event |
| `pkg/txo/engine_storage.go` | Call `IngestTx` in `InsertOutputs` to trigger main indexing |
| `pkg/parse/p2pkh.go` | Add `p2pkh` event (exact 25-byte), add `fund` event (sats > 1) |
| `pkg/parse/lock.go` | Add `lock` event |
| `pkg/parse/inscription.go` | Add `insc` event, split type into two levels |
| `pkg/parse/bsv21.go` | Remove ALL events; keep only tag data storage |
| `pkg/parse/ordlock.go` | Rename `list` -> `ordlock:list` |
| `pkg/parse/bitcom.go` | Namespace MAP events (`map:type:`, `map:app:`), BAP events (`bap:type:`, `bap:id:`) |
| `pkg/parse/parse.go` | Remove `shrug` from DefaultTags |
| `pkg/lookup/bsv21.go` | Rewrite to use dedicated ZSets for `{lockType}:{addr}:{tokenId}` |

---

## Parser Review

### 1. P2PKH (`pkg/parse/p2pkh.go`)

**Current:** Sets owner only, no events

**Proposed:**
| Condition | Events | Owners |
|-----------|--------|--------|
| Exactly 25 bytes, valid P2PKH | `p2pkh` | address |
| Exactly 25 bytes, valid P2PKH, sats > 1 | `p2pkh`, `fund` | address |
| P2PKH prefix + trailing script | (none) | address |

**Rationale:**
- `p2pkh` event: Find pure P2PKH outputs
- `fund` event: Find fundable UTXOs (non-dust)
- Composite scripts (inscriptions, etc.) still tracked by owner but not as `p2pkh`

### 2. Lock (`pkg/parse/lock.go`)

**Current:** Sets owner only, no events

**Proposed:**
| Events | Owners |
|--------|--------|
| `lock` | address |

**Change:** Add `lock` event

### 3. Inscription (`pkg/parse/inscription.go`)

**Current:** `type:{full}`, `parent:{outpoint}`, owner from suffix

**Proposed:**
| Events | Owners |
|--------|--------|
| `insc` | P2PKH from suffix |
| `type:{base}` (e.g., `type:image`) | |
| `type:{full}` (e.g., `type:image/jpeg`) | |
| `parent:{outpoint}` (if exists) | |

**Changes:**
- Add `insc` event
- Split type into two levels: base (`image`) and full (`image/jpeg`)

### 4. BSV21 (`pkg/parse/bsv21.go`)

**Current:** `deploy`, `id:{tokenId}`, `sym:{symbol}`, `own:{address}` (redundant)

**Proposed:**
| Events | Owners | Tag Data |
|--------|--------|----------|
| (none) | (none) | id, op, amt, sym, dec, icon |

**Changes:**
- Remove ALL events (`deploy`, `id:`, `sym:`, `own:`)
- Remove owner extraction (owners come from suffix parsers: P2PKH, Inscription, Cosign)
- Keep only tag data storage for BSV21 metadata

**Note:**
- `{lockType}:{addr}:{tokenId}` handled by BSV21 lookup service in dedicated ZSets
- Owners are determined by the suffix parser (P2PKH, Inscription, or Cosign), not BSV21 parser

### 5. OrdLock (`pkg/parse/ordlock.go`)

**Current:** `list` event, seller as owner

**Proposed:**
| Events | Owners |
|--------|--------|
| `ordlock:list` | Seller address |

**Changes:**
- Rename `list` -> `ordlock:list`
- TODO: Add `ordlock:buy` and `ordlock:cancel` events in summarize method

### 6. Cosign (`pkg/parse/cosign.go`)

**Current:** No events, owner from address

**Proposed:** No changes - keep as-is

### 7. Shrug (`pkg/parse/shrug.go`)

**Proposed:** Remove from DefaultTags - should live in an overlay like BSV21

### 8. Bitcom Family (`pkg/parse/bitcom.go`)

#### Bitcom (base parser)
**Current:** No events, stores parsed `bitcom.Bitcom` structure
**Proposed:** No changes - keep as-is

#### B (file data)
**Current:** `type:{mediaType}` event
**Proposed:** No changes - this is the canonical content type event

#### MAP
**Current:** `type:{type}`, `app:{app}` events

**Proposed:**
| Events | Data |
|--------|------|
| `map:type:{type}` | MAP data |
| `map:app:{app}` | |

**Changes:**
- Namespace `type:` -> `map:type:` to avoid conflict with content types

#### AIP
**Current:** `signer:{address}` for valid signatures
**Proposed:** No changes - keep as-is

#### BAP
**Current:** `type:{bap.Type}`, `id:{idKey}` events

**Proposed:**
| Events | Data |
|--------|------|
| `bap:type:{type}` | BAP data |
| `bap:id:{idKey}` | |

**Changes:**
- Namespace `type:` -> `bap:type:` to avoid conflict with content types
- Namespace `id:` -> `bap:id:` to avoid conflict with BSV21 `id:{tokenId}`

#### Sigma
**Current:** `signer:{address}` for valid signatures
**Proposed:** No changes - keep as-is

---

## Event Namespace Summary

| Parser | Current Events | New Events |
|--------|---------------|------------|
| P2PKH | (none) | `p2pkh`, `fund` |
| Lock | (none) | `lock` |
| Inscription | `type:{full}`, `parent:{op}` | `insc`, `type:{base}`, `type:{full}`, `parent:{op}` |
| BSV21 | `deploy`, `id:`, `sym:`, `own:` | (none) - tag data only |
| OrdLock | `list` | `ordlock:list` |
| Cosign | (none) | (none) |
| Shrug | - | REMOVED from DefaultTags |
| Bitcom | (none) | (none) |
| B | `type:{mediaType}` | `type:{mediaType}` (unchanged) |
| MAP | `type:`, `app:` | `map:type:`, `map:app:` |
| AIP | `signer:` | `signer:` (unchanged) |
| BAP | `type:`, `id:` | `bap:type:`, `bap:id:` |
| Sigma | `signer:` | `signer:` (unchanged) |

---

## Implementation Order

1. **Parser changes** (no dependencies, can be done independently):
   - `pkg/parse/p2pkh.go` - Add `p2pkh` and `fund` events
   - `pkg/parse/lock.go` - Add `lock` event
   - `pkg/parse/inscription.go` - Add `insc` event, split type levels
   - `pkg/parse/bsv21.go` - Remove all events; keep only tag data
   - `pkg/parse/ordlock.go` - Rename `list` -> `ordlock:list`
   - `pkg/parse/bitcom.go` - Namespace MAP and BAP events
   - `pkg/parse/parse.go` - Remove shrug from DefaultTags

2. **Storage changes**:
   - `pkg/txo/output_store.go` - Remove `txid:` event

3. **Integration changes** (depends on parsers being complete):
   - `pkg/txo/engine_storage.go` - Call `IngestTx` from `InsertOutputs`
   - `pkg/lookup/bsv21.go` - Rewrite to use dedicated ZSets

---

## Notes

- All changes are backwards compatible for reads (old data still works)
- Re-indexing will be required to populate new events
- BSV21 balance queries will use new dedicated ZSets, not events

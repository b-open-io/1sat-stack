# Origin Indexer: Tag 1-Sat Transfers with Origin Metadata

Status: **In Progress**

## Context

When an inscription is transferred, the receiving output is plain P2PKH — no inscription data in the script. The `insc` parser only fires when the script contains an actual inscription. This means transferred ordinals (the majority of OPNS names on a user's address) have no `type:`, `origin:`, or `name:` events, making them invisible to tag-based search.

Goal: Add an origin parser to the parse chain so that all 1-sat non-inscription outputs get `origin:`, `type:`, and `name:` events at index time. No data objects stored — events only.

## Approach

The origin parser is another parser in the chain, same as all the others. It fires on 1-sat outputs that are NOT inscriptions, calls ORDFS `Load()` to resolve the origin, and emits events. ORDFS handles all ordinal position math and backward crawl internally — no need to replicate that logic.

### 1. Add `context.Context` and services to `ParseContext`

**File: `pkg/parse/parse.go`**

Add optional `Ctx context.Context` and `Ordfs *ordfs.Ordfs` fields to `ParseContext`. Update `Parse()` to accept and thread these through.

### 2. Create origin parser

**File: `pkg/parse/origin.go`**

New parser function `ParseOrigin` that:
- Skips if output is NOT 1-sat (no `1sat` result in context)
- Skips if output IS an inscription (has `insc` result in context)
- Calls `ordfs.Load(ctx, &Request{Outpoint: &op, Content: false, Map: true})`
- `ErrNotFound` → return nil (not an inscription transfer)
- Other error → log warning, return nil (don't fail the whole tx)
- On success, emits events:
  - `origin:{origin_outpoint}`
  - `type:{category}` (e.g., `type:application`)
  - `type:{full}` (e.g., `type:application/op-ns`)
  - `name:{name}` if map data contains a `name` key

### 3. Wire ORDFS into IngestCtx

**File: `pkg/indexer/ingest.go`** — Add `Ordfs *ordfs.Ordfs` field + `WithOrdfs()` setter.

**File: `pkg/indexer/indexer.go`** — Pass `Ctx` and `Ordfs` to `parse.Parse()`.

**File: `pkg/indexer/config.go`** — Add `Ordfs` to `InitializeDeps`.

**File: `cmd/server/config.go`** — Pass `svc.ORDFS.Ordfs` to indexer deps.

### 4. Add `origin` tag to config

Add `origin` to the active tags list in config.yaml on rack.

### 5. Update admin UI discover flow

**File: `admin/ui/src/pages/OpNSPage.tsx`**

Search simplifies to:
```
GET /txo/search?key=own:{address}&key=type:application/op-ns&join=intersect&unspent=true
```
No bulk metadata call needed. Use `origin:` from output events to fetch name content.

## Files Modified

| File | Change |
|------|--------|
| `pkg/parse/parse.go` | Add `Ctx` and `Ordfs` to ParseContext, update Parse signature |
| `pkg/parse/origin.go` | New origin parser |
| `pkg/indexer/ingest.go` | Add `Ordfs` field + `WithOrdfs()` |
| `pkg/indexer/indexer.go` | Pass ctx/ordfs to parse.Parse() |
| `pkg/indexer/config.go` | Add `Ordfs` to `InitializeDeps` |
| `cmd/server/config.go` | Pass ORDFS to indexer deps |
| `admin/ui/src/pages/OpNSPage.tsx` | Use indexed type events instead of bulk metadata |

## Verification

1. `go vet ./pkg/indexer/ ./pkg/ordfs/ ./pkg/parse/`
2. `cd admin/ui && bun run build`
3. Deploy to rack, trigger owner sync for address with transferred OPNS names
4. Verify outputs have `origin:` and `type:application/op-ns` events
5. Verify discover flow finds OPNS names via tag intersection

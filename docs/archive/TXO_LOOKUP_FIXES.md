# TXO Lookup & Search API Fixes

Status: **In Progress**

## Background

The event storage and retrieval audit revealed several issues with how TXO lookups and searches work. The core storage works correctly — events are stored in sorted sets with `ev:` prefix, hash data is stored under outpoint keys with `h:` prefix. But several retrieval paths are broken or inconsistent.

## Issues

### 1. `LoadOutputsByTxid` scans wrong key space

**File**: `pkg/txo/output_store.go` L635-664
**Route**: `GET /txo/tx/:txid`

`LoadOutputsByTxid` calls `Store.Scan()` with `KeyTxidPrefix(txid)` (32-byte txid). `Scan` uses `kvKey()` which prefixes with `k:`, scanning `k:{txid}*`. But all output data is stored via `HSet` under `h:{outpoint}:{field}` — nothing is ever written to the `k:` key space for outpoints.

**Fix**: Use the same search mechanism as everything else. Every output has a `txid:{txid}` event stored in sorted set `ev:txid:{txid}`. Replace the Scan with:
```go
results, err := s.Search(ctx, &OutputSearchCfg{
    SearchCfg: store.SearchCfg{
        Keys: [][]byte{[]byte("txid:" + txid.String())},
    },
})
```
Then load outputs from results.

### 2. Direct outpoint lookup (`GET /txo/:outpoint`) returns empty

**File**: `pkg/txo/routes.go` L55-77, `pkg/txo/output_store.go` L623-632

`LoadOutput` calls `HGetAll(ctx, KeyOutHash(op))` where `KeyOutHash` returns `op.Bytes()` (36 bytes). At the Badger level, this scans prefix `h:{36-byte-outpoint}:` for all fields.

The byte-level operations are consistent between save and load. If this returns empty, it means the data was never saved for that particular outpoint. This may have been a transient issue during the store clear/resync, or there may be a subtle issue with how `OutpointFromString` parses the separator (`.` vs `_`).

**Action**: Verify with fresh data after the origin indexer changes. If still failing, add debug logging to `LoadOutput` to compare requested key bytes vs what exists.

### 3. Admin UI OPNS discover used bulkMetadata instead of indexed events

**File**: `admin/ui/src/pages/OpNSPage.tsx`
**Status**: Fixed (commit fe64cfb)

The old flow searched `own:{address}` with `tags=insc`, then called bulkMetadata on every result, then filtered by content type client-side. This was slow and only found inscription outputs (not transfers).

**Fix**: Replaced with intersect search `key=own:{address}&key=type:application/op-ns&join=intersect` and extract names from indexed `name:` events. No ORDFS calls needed.

## Event Storage Reference

All events go through `SaveOutput` which builds:
1. `txid:{txid}` — always
2. Parser events (e.g., `1sat`, `insc`, `type:application/op-ns`, `origin:{outpoint}`, `name:{name}`, `p2pkh:{addr}`, etc.)
3. `own:{address}` — for each owner from parsers

All stored in sorted sets via `KeyEvent(event)` → Badger key `zs:ev:{event}`.

### Search paths

| Route | Keys used | Goes through prefixKey? |
|-------|-----------|------------------------|
| `GET /txo/search` | Raw query params | Yes — `prefixKey()` adds `ev:` if missing |
| `GET /owner/:owner/txos` | `own:{address}` | Yes — via `Search()` |
| `GET /owner/:owner/balance` | `own:{address}` | Yes — via `Search()` |
| `GET /txo/tx/:txid` | `KeyTxidPrefix(txid)` via Scan | **NO** — bypasses search, scans `k:` space (BUG) |
| `GET /txo/:outpoint` | Direct hash lookup | N/A — uses `HGetAll`, no sorted set |

# Parser Events Audit

This document summarizes what each parser returns for Events and Owners fields.

## How Events Are Saved

In `pkg/lookup/onesat.go`, events are processed as:

1. **Prefixed events:** `tag + ":" + event` for each event in `result.Events`
2. **Owner events:** `"own:" + address` for each owner in `result.Owners`

Tags themselves are NOT saved as events.

---

## Parser Summary

### P2PKH (`pkg/parse/p2pkh.go`)

| Field | Value |
|-------|-------|
| Tag | `p2pkh` |
| Events | *(none)* |
| Owners | `[pkHash]` |
| Data | `*p2pkh.P2PKH` |

**Saved events:** `own:ADDRESS`

---

### Lock (`pkg/parse/lock.go`)

| Field | Value |
|-------|-------|
| Tag | `lock` |
| Events | `["owner:ADDRESS"]` |
| Owners | `[pkHash]` |
| Data | `*lockup.Lockup` |

**Saved events:** `lock:owner:ADDRESS`, `own:ADDRESS`

**Note:** Uses `"owner:"` (with 'er') not `"own:"` in Events field.

---

### Inscription (`pkg/parse/inscription.go`)

| Field | Value |
|-------|-------|
| Tag | `insc` |
| Events | `["type:MIMETYPE"]`, `["parent:OUTPOINT"]` (if present), `["own:ADDRESS"]` (if suffix has P2PKH) |
| Owners | `[pkHash]` (from script suffix if P2PKH) |
| Data | `*inscription.Inscription` |

**Saved events:** `insc:type:MIMETYPE`, `insc:parent:OUTPOINT`, `insc:own:ADDRESS`, `own:ADDRESS`

**Note:** Only parses outputs with exactly 1 satoshi.

---

### BSV21 (`pkg/parse/bsv21.go`)

| Field | Value |
|-------|-------|
| Tag | `bsv21` |
| Events | `["deploy"]` (for deploy ops), `["id:TOKEN_ID"]`, `["sym:SYMBOL"]` (if present), `["own:ADDRESS"]` (if suffix has P2PKH) |
| Owners | `[pkHash]` (from inscription suffix if P2PKH) |
| Data | `*BSV21` |

**Saved events:** `bsv21:deploy`, `bsv21:id:TOKEN_ID`, `bsv21:sym:SYMBOL`, `bsv21:own:ADDRESS`, `own:ADDRESS`

---

### OrdLock (`pkg/parse/ordlock.go`)

*TODO: Document*

---

### Cosign (`pkg/parse/cosign.go`)

| Field | Value |
|-------|-------|
| Tag | `cosign` |
| Events | `["own:ADDRESS"]` for each cosigner |
| Owners | *(none set directly)* |
| Data | `*ordlock.Cosign` |

**Saved events:** `cosign:own:ADDRESS` (for each cosigner)

**Note:** Sets Events but not Owners - cosigners won't appear in `own:ADDRESS` lookups!

---

### Shrug (`pkg/parse/shrug.go`)

*TODO: Document after reading*

---

### Bitcom (`pkg/parse/bitcom.go`)

*TODO: Document*

---

## Issues Identified

1. **Redundant `own:` events:** Several parsers include `"own:ADDRESS"` in Events AND set Owners, resulting in both `tag:own:ADDRESS` and `own:ADDRESS` being saved.

2. **Inconsistent owner handling:** Cosign sets `"own:ADDRESS"` in Events but doesn't set Owners, so cosigners won't appear in owner lookups.

3. **Typo in Lock:** Uses `"owner:"` instead of `"own:"` - may be intentional for lock-specific lookups.

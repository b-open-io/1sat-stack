# Error Handling Analysis Report: 1sat-stack

**Date:** 2025-12-15
**Scope:** All Go source files in the 1sat-stack repository

---

## Executive Summary

This report identifies **16 locations** where errors are potentially being swallowed or improperly handled. The issues range from critical (data integrity risks) to low (minor cleanup issues). The most concerning patterns involve:

- BEEF storage operations ignoring errors (5 locations)
- Database rollback operations not checking for failures
- JSON parsing failures being silently ignored
- Save operations returning success despite persistence failures

---

## Critical Issues

These issues could lead to data loss, corruption, or inconsistent state.

### 1. BEEF Load Errors Ignored

**Files affected:**
- `pkg/txo/engine_storage.go` (lines 92, 135, 171, 219)
- `pkg/bsv21/routes.go` (line 292)

**Pattern:**
```go
output.Beef, _ = s.BeefStore.LoadBeef(ctx, &outpoint.Txid)
```

**Risk:** BEEF data is critical for transaction verification. When loading fails:
- API consumers receive incomplete data without any indication
- Network/storage issues are completely hidden
- No way to distinguish between "no BEEF exists" and "failed to load BEEF"

**Recommendation:** Return or log the error. Consider adding a boolean flag to indicate BEEF availability.

---

### 2. saveBeefInternal Errors Ignored

**File:** `pkg/beef/beef.go` (lines 303, 308)

**Code:**
```go
if ct != nil {
    _, tx, _, parseErr := transaction.ParseBeef(updatedBeef)
    if parseErr == nil && tx != nil && tx.MerklePath != nil {
        valid, verifyErr := tx.MerklePath.Verify(ctx, txid, ct)
        if verifyErr == nil && valid {
            s.saveBeefInternal(ctx, txid, updatedBeef)  // ERROR IGNORED
            return updatedBeef, nil
        }
    }
} else {
    s.saveBeefInternal(ctx, txid, updatedBeef)  // ERROR IGNORED
    return updatedBeef, nil
}
```

**Risk:** This is the most serious issue:
- Function returns success even when persistence fails
- Caller receives updated BEEF but it may not be stored
- Subsequent loads will return stale data
- Creates silent data inconsistency

**Recommendation:** Propagate the error from `saveBeefInternal`.

---

## High Severity Issues

These issues could lead to incomplete operations or inconsistent indices.

### 3. Rollback GetEvents Error Ignored

**File:** `pkg/txo/output_store.go` (line 714)

**Code:**
```go
events, _ := s.GetEvents(ctx, op)
```

**Risk:** If `GetEvents` fails during rollback:
- Event indices won't be cleaned up
- Orphaned entries remain in sorted sets
- Function reports success despite incomplete cleanup

---

### 4. Rollback HGetAll Error Ignored

**File:** `pkg/txo/output_store.go` (line 723)

**Code:**
```go
fields, _ := s.Store.HGetAll(ctx, hashKey)
```

**Risk:** Hash fields won't be retrieved, leaving orphaned data.

---

### 5. Multiple Store Operations in Rollback

**File:** `pkg/txo/output_store.go` (lines 716-730)

**Code:**
```go
for _, event := range events {
    s.Store.ZRem(ctx, keyEvent(event), opBytes)
    s.Store.ZRem(ctx, keyEventSpnd(event), opBytes)
}

for field := range fields {
    s.Store.HDel(ctx, hashKey, []byte(field))
}

s.Store.HDel(ctx, hashSpnd, opBytes)
s.Store.HDel(ctx, hashSats, opBytes)
```

**Risk:** All `ZRem` and `HDel` operations ignore errors:
- Partial cleanup leaves indices inconsistent
- No way to know rollback partially failed

**Recommendation:** Collect errors and log them. Consider returning an aggregate error.

---

### 6. SaveSpend JSON Unmarshal

**File:** `pkg/txo/output_store.go` (line 299)

**Code:**
```go
if len(eventsBytes) > 0 {
    var events []string
    if err := json.Unmarshal(eventsBytes, &events); err == nil {
        for _, event := range events {
            s.Store.ZAdd(ctx, keyEventSpnd(event), store.ScoredMember{
                Member: opBytes,
                Score:  score,
            })
        }
    }
}
```

**Risk:** If JSON unmarshal fails:
- Spend event indices won't be updated
- Output marked as spent but not indexed for events
- Creates inconsistent state between output status and event indices

---

## Medium Severity Issues

These issues may cause data to be silently dropped or incomplete responses.

### 7. Events JSON Unmarshal

**File:** `pkg/txo/output_store.go` (line 572)

**Code:**
```go
if ev, ok := fields[fldEvent]; ok {
    json.Unmarshal(ev, &output.Events)
}
```

**Risk:** Corrupted event data results in empty events array with no indication of failure.

---

### 8. Tag Data JSON Parsing

**File:** `pkg/txo/output_store.go` (lines 580-593)

**Code:**
```go
if err := json.Unmarshal(data, &tagData); err == nil {
    output.Data[tag] = tagData
}
```

**Risk:** Tag data silently dropped on parse failure. No way to distinguish "tag doesn't exist" from "tag data corrupted."

---

### 9. OrdFS JSON Parsing (Multiple)

**File:** `pkg/ordfs/ordfs.go` (lines 163, 170, 175-177)

**Affected fields:**
- `subTypeData` (line 163)
- `royalties` (line 170)
- `mapJSON` serialization (line 175-177)

**Risk:** Nested data structures silently lost on parse/serialize failure.

---

### 10. Close() Error Message

**File:** `pkg/beef/beef.go` (lines 324-334)

**Code:**
```go
var errs []error
for _, storage := range s.storages {
    if err := storage.Close(); err != nil {
        errs = append(errs, err)
    }
}

if len(errs) > 0 {
    return errors.New("errors closing storages")
}
```

**Risk:** Individual storage errors are collected but then discarded. Caller only sees generic message.

**Recommendation:** Use `errors.Join()` or `fmt.Errorf` to include all errors.

---

### 11. PubSub Publish Errors

**File:** `pkg/txo/output_store.go` (lines 192, 243)

**Code:**
```go
if s.PubSub != nil {
    opStr := op.String()
    for _, event := range events {
        s.PubSub.Publish(ctx, event, opStr)
    }
}
```

**Risk:** Subscribers silently miss updates. No visibility into pub/sub failures.

---

## Low Severity Issues

Minor issues that could cause cleanup problems.

### 12. Temp File Removal

**File:** `pkg/beef/filesystem.go` (line 59)

**Code:**
```go
if err := os.Rename(tempFile, filePath); err != nil {
    os.Remove(tempFile)  // ERROR IGNORED
    return fmt.Errorf("failed to rename file: %w", err)
}
```

**Risk:** Failed temp file cleanup can accumulate disk usage over time.

---

## Summary Table

| # | Severity | File | Line(s) | Issue |
|---|----------|------|---------|-------|
| 1 | CRITICAL | engine_storage.go | 92, 135, 171, 219 | BEEF load errors ignored |
| 2 | CRITICAL | routes.go | 292 | BEEF load error ignored |
| 3 | CRITICAL | beef.go | 303, 308 | saveBeefInternal errors ignored |
| 4 | HIGH | output_store.go | 714 | GetEvents error in Rollback |
| 5 | HIGH | output_store.go | 723 | HGetAll error in Rollback |
| 6 | HIGH | output_store.go | 716-730 | Store delete errors in Rollback |
| 7 | HIGH | output_store.go | 299 | JSON unmarshal in SaveSpend |
| 8 | MEDIUM | output_store.go | 572 | Events JSON unmarshal |
| 9 | MEDIUM | output_store.go | 580-593 | Tag data JSON parsing |
| 10 | MEDIUM | ordfs.go | 163, 170, 175 | OrdFS JSON parsing |
| 11 | MEDIUM | beef.go | 324-334 | Vague Close() error |
| 12 | MEDIUM | output_store.go | 192, 243 | PubSub errors ignored |
| 13 | LOW | filesystem.go | 59 | Temp file removal |

---

## Recommendations

### Immediate Actions (Critical)

1. **Fix saveBeefInternal error handling** in `beef.go:303,308`
   - Return the error from saveBeefInternal
   - This prevents silent data loss

2. **Address BEEF load errors** in `engine_storage.go` and `routes.go`
   - At minimum: log the error
   - Better: include error state in response or return error

### Short-term Actions (High)

3. **Add error logging to Rollback operations**
   - Log individual errors during cleanup
   - Consider collecting errors and returning aggregate

4. **Fix SaveSpend JSON handling**
   - Log warning when event parsing fails
   - Consider failing the operation on parse error

### Medium-term Actions

5. **Add structured logging for all JSON parse failures**
   - Create a helper that logs on failure
   - Helps diagnose data corruption issues

6. **Improve Close() error message**
   - Use `errors.Join()` to preserve individual errors

7. **Add PubSub error logging**
   - Log failures for debugging
   - Consider adding metrics for publish failures

---

## Notes

Some ignored errors may be intentional design choices:
- Rollback operations often use "best effort" cleanup
- PubSub failures may be acceptable if primary storage succeeded

However, even intentional error suppression should include logging for debugging and monitoring purposes.

# Zig Parser Implementation Plan

Status: **In Progress**
Linear: OPL-1512

## Structure

All parsers live in `zig/src/parse/` as separate files, imported by `main.zig`.
Parser libraries from bsvz are used where available (script, transaction, crypto types).

## Parser Inventory

### Done
- `1sat` — satoshis == 1 check
- `p2pkh` — P2PKH address extraction

### Tier A — Simple pattern match
- `lock.zig` — CLTV lock detection, extract address + until height
- `cosign.zig` — Cosign pattern detection
- `opns.zig` — OPNS mine output detection

### Tier B — Script parsing
- `inscription.zig` — OP_FALSE OP_IF envelope parsing (content type, data, parent)
- `bsv21.zig` — Token op extraction from inscription data (depends on inscription)
- `ordlock.zig` — OrdLock marketplace listing pattern
- `shrug.zig` — Shrug protocol pattern

### Tier C — Bitcom protocol family
All in `bitcom.zig`, with sub-parsers:
- Base bitcom: split OP_RETURN by `|` separator into protocol chunks
- `b` protocol: media type, encoding, data, filename
- `map` protocol: cmd + key-value data
- `aip` protocol: AIP signature extraction
- `bap` protocol: BAP identity attestation
- `sigma` protocol: SIGMA signature extraction

These chain: bitcom base runs first, then B/MAP/AIP/BAP/SIGMA read from its results.

### Tier D — External data
- `origin.zig` — Origin tracking for transferred ordinals. Needs access to spent output data from the ParsedBeef (source transactions are included in the beef).

## Approach

Each parser file exports a function matching the pattern:
```zig
pub fn parse(locking_script: []const u8, satoshis: u64, ctx: *ParseContext) !void
```

`ParseContext` holds accumulated results so later parsers can read earlier ones.
`main.zig` calls them in order matching the Go `DefaultTags` sequence.

## Parallel Work Packages

1. **Tier A bundle**: lock + cosign + opns (simple, independent)
2. **Inscription + BSV21**: inscription first, bsv21 reads from it
3. **OrdLock + Shrug**: independent script patterns
4. **Bitcom family**: base parser + all sub-protocols
5. **Origin**: depends on ParsedBeef input being wired up

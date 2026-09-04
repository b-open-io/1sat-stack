# ecosystemalias

Contract-only foundation for the generic BRC-169 ecosystem-alias overlay module.

This package freezes parser, query, and cursor interfaces plus conformance vectors.
It does **not** wire a live module, HTTP routes, topic manager, store, lookup
implementation, discovery, or sync. OPL-4445 implements those.

## Frozen names

| Token | Value |
| --- | --- |
| Topic | `tm_ecosystemalias` |
| Lookup service | `ls_ecosystemalias` |
| Protocol | `ecosystem-alias` |
| Version | `1` |
| HTTP (planned only) | `POST /ecosystemalias/overlay/lookup` |

Do not invent alias or domain REST routes.

## Token

A claim is a [BRC-48](https://github.com/bitcoin-sv/BRCs/blob/master/scripts/0048.md)
PushDrop output with exactly six fields, in order:

1. protocol — ASCII `ecosystem-alias`
2. version — ASCII `1`
3. normalized alias
4. normalized RFC 1123 FQDN
5. compressed 33-byte certifier key
6. DER ECDSA signature

The digest is SHA-256 of the raw concatenation of fields 1–5, with no separators
or length prefixes. The certifier key in field 5 signs that digest.

Claims require a positive satoshi value. Conflicts remain queryable; this overlay
never imposes uniqueness on alias or domain.

Token values must already be normalized because they are signed.
`ValidateTokenFields` therefore does not case-fold. Query normalization may
ASCII-lowercase, but rejects leading/trailing whitespace, non-ASCII/Unicode
input, and empty values.

### Alias grammar

Lowercase ASCII letters and digits with internal single hyphens. 1–32 bytes.
No leading or trailing hyphen. No consecutive hyphens.

### Domain grammar

Lowercase ASCII RFC 1123 FQDN. At least two labels. No trailing dot. Each label
1–63 bytes. Total at most 253 bytes. Valid `xn--` punycode labels are accepted as
ASCII. Unicode input is rejected; clients must pass punycode.

## Query

`DecodeQuery` accepts a BRC-24 `query` object with exactly one mode:

- `alias`
- `domain`
- `findAll: true`

It rejects unknown fields, JSON `null`, malformed JSON, duplicate fields, invalid
combinations, `findAll: false`, zero or oversized limits, malformed cursors, and
a cursor bound to another query.

Default page size is 100. Maximum is 500.

Deterministic malformed-query codes are guaranteed at this Go interface
(`Error.Code`, independent of `Error.Message`). The existing shared HTTP adapter
may surface them as BRC-24 `provider-failure`. Changing transport errors is
outside this contract.

## Ordering

Alias/domain results: confirmed first, then earliest block height, earliest
block index, lexical txid, output index. Mempool entries follow confirmed
entries and use lexical txid and output index only.

Enumeration (`findAll`) results: lexical txid then output index.

Do not use wall-clock scores. Confirmed vs mempool is an explicit flag, not
`height == 0`.

## Cursor

Cursors are opaque, URL-safe, and versioned (`ea1.` + unpadded base64url).
They are client-derived from the last returned outpoint plus a fingerprint of
the normalized query mode and value. On receipt the service resolves that
outpoint's stored sort key. No server secret is required. Validation is
structural and binding, not authorization.

The current overlay engine discards lookup `Result` metadata when hydrating an
`output-list`, so **no cursor can currently be returned as BRC-24 lookup
metadata**. Clients must derive the next cursor from the last hydrated outpoint.

## Fixtures

`testdata/brc169-aliases.json` is versioned. It covers all six token fields,
signature digest/vector material, normalization, strict query decoding, ordering
keys, and cursors.

`canonicalSha256` is SHA-256 of the document after omitting that field and
serializing with RFC 8785-style canonical JSON (sorted object keys, no
insignificant whitespace, JSON numbers as digit literals). Current value:

`d163add78c6533f8a01af597da12cc9511019c3a8223e0b541883ea43b078a1f`

A TypeScript copy must fail on drift.

This fixture does **not** include or claim to reproduce a confirmed Sigma
transaction. No raw Sigma transaction bytes are present in this repository.
Signature vectors use a documented RFC6979 test key (secp256k1 generator).

## OPL-4445 remaining work

The current storage lifecycle has incomplete rollback propagation. Full
lifecycle and reorg support is required of OPL-4445, including:

- A strict local BRC-48 decoder shared by parser and topic manager
- SQLite and PostgreSQL storage
- Standard BRC-24 lookup (`ls_ecosystemalias`) returning `output-list`
- Configuration, routes, discovery, sync, and docs
- Complete spend, eviction, block-update, restart, and reorg behavior

Parser and topic manager must never fetch manifests. Bidirectional manifest
consent (`metanet.handles.aliases`) remains resolver-side.

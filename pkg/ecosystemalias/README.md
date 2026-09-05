# ecosystemalias

Contract-only foundation for the generic BRC-169 ecosystem-alias overlay module.

This package freezes parser and query interfaces plus conformance vectors.
It does **not** wire a live module, HTTP routes, topic manager, or lookup.
Lookup indexes overlay events. OPL-4445 implements decoding, topic, and routes.

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
combinations, `findAll: false`, zero or oversized limits, and negative skips.

Default page size is 100. Maximum is 500. Optional `skip` is a non-negative
offset (default 0).

Deterministic malformed-query codes are guaranteed at this Go interface
(`Error.Code`, independent of `Error.Message`). The existing shared HTTP adapter
may surface them as BRC-24 `provider-failure`. Changing transport errors is
outside this contract.

## Ordering

Lookup order is overlay `HeightScore` on the event, then output index.

Confirmed: `height + txIndex/1e9`. Unconfirmed: ingest unix time (sorts after
any block height). Same transaction (same score) uses `vout`.

`outputs.score` stays ingest time for GASP. Do not page with opaque cursors.

## Fixtures

`testdata/brc169-aliases.json` is versioned. It covers all six token fields,
signature digest/vector material, normalization, strict query decoding, and
HeightScore ordering.

`canonicalSha256` is SHA-256 of the document after omitting that field and
serializing with RFC 8785-style canonical JSON (sorted object keys, no
insignificant whitespace, JSON numbers as digit literals). Current value:

`c8a9d7fbd555ba98ad547daa590f30fd480f05841ba93be52bbf756b1e920bb8`

A TypeScript copy must fail on drift.

This fixture does **not** include or claim to reproduce a confirmed Sigma
transaction. No raw Sigma transaction bytes are present in this repository.
Signature vectors use a documented RFC6979 test key (secp256k1 generator).

## OPL-4445 remaining work

Remaining work:

- A strict local BRC-48 decoder shared by parser and topic manager
- Lookup via overlay events (`alias:` / `domain:`), spends on `outputs`
- HeightScore restamp on `OutputBlockHeightUpdated`
- Standard BRC-24 lookup returning `output-list`
- Configuration, routes, and docs

Parser and topic manager must never fetch manifests. Bidirectional manifest
consent (`metanet.handles.aliases`) remains resolver-side.

### Enumeration and ordering

Admission writes an `ecosystemalias:all` event alongside `alias:` and `domain:`.
All query modes filter spent outputs and sort by event score, then numeric output
index, before applying skip/limit. Confirmation and reorg callbacks restamp all
three events. Output ingestion scores stay unchanged because GASP uses them as
sync watermarks. This replaces `FindUTXOs` enumeration, whose ingestion order
cannot represent confirmation order. Before activating a node built from an
earlier review branch, re-index its alias events so enumeration includes every
retained claim.

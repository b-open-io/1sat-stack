# Collection overlay

Status: **in progress** (decisions locked 2026-07-16)

### Progress

- [x] MAP parse events (`map:subType`, `map:collectionId`, `_N` normalize)
- [x] `pkg/collection` topic managers + lookup + routes (library; not wired into monolith `cmd/server`) — [1sat-stack#9](https://github.com/b-open-io/1sat-stack/pull/9)
- [x] `collection-overlay` stand-alone server scaffold — https://github.com/b-open-io/collection-overlay

Linear:

- **This work:** [OPL-2993](https://linear.app/openprotocollabs/issue/OPL-2993/collection-overlay-1sat-stack-library-stand-alone-collection-overlay) — *Backlog*, assignee david.case. Library (`1sat-stack`) + stand-alone `collection-overlay`. Related to OPL-2970.
- **PR-flurry coordination (Luke):** [OPL-2970](https://linear.app/openprotocollabs/issue/OPL-2970/ordfs-collections-pr-flurry-coordination-plan-sigma-authorship-over) — *In Progress*. SIGMA over AIP, ord-fs/json `.` entry, BSV-21 token members, multi-repo PRs (sdk, stack#8, mintflow, skills). Adjacent mint/docs work, not the overlay pipeline design.
- Earlier framing: [OPL-2969](https://linear.app/openprotocollabs/issue/OPL-2969), [OPL-2968](https://linear.app/openprotocollabs/issue/OPL-2968).

## Goal

Index 1Sat ordinal **collections** and **collection items** as overlay topics:

- Tools live in **1sat-stack** (library).
- Product server is a stand-alone **collection-overlay** (same pattern as [opns-overlay](https://github.com/b-open-io/opns-overlay)): own config, composes stack packages.

## Explicit non-goals / rejected approaches

- **Not** [1sat-stack#8](https://github.com/b-open-io/1sat-stack/pull/8) as the design: general-index membership listing + bsv21 collection hooks is not the approved path.
- **No collection APIs or resolvers on BSV21.** Dependency direction is **collection → may reference a BSV-21 `tokenId`**, never bsv21 → collection.
- **AIP is not valid membership proof for inscriptions.** AIP `[-1]` is replayable onto a counterfeit inscription. **SIGMA only** for collection roots and items (including BSV-21 deploys that carry `collectionItem` MAP). AIP remains fine for 0-sat bitcom `B` / pure OP_RETURN.
- **No transfer tracking in v1** (mint-only), similar spirit to OpNS (mint tree, not ownership transfers).
- **No whitelist/fee lifecycle required in v1 library.** Stand-alone `collection-overlay` owns product config; fee model later if needed.
- **Do not require item-signer == root controller (or BAP identity) at ingest.** BAP key rotation can race the BAP overlay. Admit on MAP shape + valid SIGMA **presence**; authority matching is a later query concern.

Related open work (context only; do not treat as approved implementation):

| Repo | PR | Notes |
|------|-----|--------|
| 1sat-ordinals | [#20](https://github.com/BitcoinSchema/1sat-ordinals/pull/20) | Docs: ord-fs refs, BSV-21 members, SIGMA — still says legacy AIP valid (needs fix) |
| 1sat-sdk | [#19](https://github.com/b-open-io/1sat-sdk/pull/19), [#21](https://github.com/b-open-io/1sat-sdk/pull/21) | Mint SIGMA + ref items / BSV-21 as collectionItem |
| 1sat-stack | [#8](https://github.com/b-open-io/1sat-stack/pull/8) | Not approved approach |

## On-chain model (spec)

| Role | Shape |
|------|--------|
| Collection root | 1-sat ordinal, MAP `type=ord`, `subType=collection`, SIGMA |
| Member (NFT) | 1-sat, `subType=collectionItem`, `subTypeData.collectionId` = root outpoint, SIGMA |
| Member (fungible) | BSV-21 deploy with same `collectionItem` MAP + SIGMA; membership in MAP only (`bsv-20` JSON unchanged). Member identity is the deploy outpoint (`tokenId`) |

## Topics

| Topic | Admits |
|-------|--------|
| `tm_1sat_collection` | Collection **root mints** only (MAP `subType=collection` + valid SIGMA present) |
| `tm_col_{collectionId}` | **Member mints** only for that root (MAP `subType=collectionItem`, normalized `collectionId` matches topic, valid SIGMA present) |

- `collectionId` is an outpoint (same kind of topic key as BSV-21 `tokenId`).
- Prefix `tm_col_` is module isolation only; BSV-21 naming (`tm_{tokenId}`) re-evaluated later.
- Discovery uses `tm_1sat_collection` because “collection” alone is too generic.

## Events and routing

Generic MAP events (no special `1sat_collection:*` family):

| Event | Use |
|-------|-----|
| `map:subType:collection` | → discovery queue / `tm_1sat_collection` |
| `map:subType:collectionItem` | optional signal |
| `map:collectionId:{id}` | → `q:tm_col_{id}` (normalize same-tx `_N` to absolute outpoint when emitting) |

EventBridge pattern-matches these; collection meaning lives in topic managers / lookup.

## Admission (v1)

- **Discovery:** MAP `subType=collection` + valid SIGMA present (signing address available for later mapping).
- **Per-collection:** MAP `subType=collectionItem` + normalized `collectionId` matches + valid SIGMA present.
- **No GASP / previousCoins for v1** — mint outputs are self-contained for admission.
- **No signer↔root / BAP match at ingest.**

## Lookup storage (v1, thin)

Co-locate topic + lookup in the module (not a shared `pkg/lookup` home for new work).

- **Discovery topic DB:** root outpoint, useful MAP fields, SIGMA signer address, score/height.
- **Per-collection topic DB:** member outpoint, collectionId, mintNumber/rank if present, SIGMA signer, content-type / tokenId hint if useful, score.

No current-owner / transfer tables in v1.

## HTTP API (product surface)

Prefer under `/collection` (or collection-overlay’s mount). Suggested:

| Route | Purpose |
|-------|---------|
| `GET …/collection` or `…/roots` | List discovery roots |
| `GET …/collection/:collectionId` | Root metadata + SIGMA signer |
| `GET …/collection/:collectionId/members` | Members from `tm_col_{id}` |
| `GET …/collection/:collectionId/member/:outpoint` | Single member (optional) |

No `GET /bsv21/…/collection`. Token balances stay on BSV21; collection may include outpoint/`tokenId` on member payloads.

Routes may live only on **collection-overlay** at first; stack can expose the same later if embedded.

## Architecture split

### 1sat-stack (library)

1. **Parse** — emit `map:subType:*` and `map:collectionId:*` (with `_N` normalization).
2. **`pkg/collection`** — discovery + per-id topic managers, lookup, constructors usable without monolith `cmd/server` wiring.
3. Reuse overlay storage / engine helpers (as opns-overlay does).
4. **Optional cleanup:** move `pkg/lookup/bsv21.go` → `pkg/bsv21`, `pkg/lookup/shrug.go` → `pkg/shrug` so topic+lookup co-locate.

Not required in stack for first cut: whitelist manager, fee lifecycle, collection JungleBus pipeline, full monolith embedding.

### collection-overlay (stand-alone)

- New repo/module, patterned on opns-overlay.
- Own config, `cmd/server`, mounts collection topics/routes, peers/backfill as needed.
- Imports 1sat-stack as library (`replace` during dev).

## Implementation order

1. 1sat-stack: MAP parse events (+ tests).
2. 1sat-stack: `pkg/collection` topic managers + lookup (mint-only, SIGMA presence).
3. Stand up **collection-overlay** wiring (submit/index/query for roots + members).
4. Later: authority matching at query time (BAP-aware), fee/whitelist product config, transfer tracking if ever needed, BSV21 topic rename consistency.

## Related docs (spec / mint)

- `1sat-ordinals/adding-metadata/collections.md`, `collectionitem-subtype.md`, `signing.md`
- SDK actions: `mintCollection` / `mintCollectionItem` (SIGMA work in open PRs)

## Open follow-ups (not blocking v1 library)

- Fix ordinals docs PR language: SIGMA required for inscription membership; drop “legacy AIP remains valid.”
- Confirm OPL-2970 title/scope in Linear matches this plan; split issues if stack library vs collection-overlay should track separately.
- Query-time “authoritative member” once BAP resolution is reliable.
- Fee-gated activation (phase 3 product).

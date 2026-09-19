# gib

## Purpose

`gib` is the overlay module for gib, on-chain git on BSV. It indexes **commit
heads**: 1-satoshi PushDrop coins that name a repository's branch tip. A coin's
spend chain is the branch's push history, so the module records every head it
sees, links each head to the one it spent, and serves repository, branch, and
publisher views for gibhub.net and other clients. Content (directory manifests,
files, patches) is not indexed here; ORDFS resolves it from the root outpoint a
head names.

## Concepts

| Term | Meaning |
| --- | --- |
| Origin | Outpoint of the repository's genesis directory inscription. The repo id. |
| Root | Outpoint of the root directory manifest a branch currently points at. |
| Commit head | 1-sat PushDrop coin: fields `["gib", origin, branch, root, identity]`, git commit object inscribed on the same output. |
| Identity | The publisher's BRC-100 identity key (compressed hex). |
| Push | Spending a head and creating the next one for the same origin and branch. |
| Delete | Spending a head with no successor (burn). |
| Owner | The identity that minted the earliest head for an origin. Anyone may mint heads for any origin; the API groups by identity. |
| `.gib` | Optional JSON file at the tree root (`name`, `description`, `defaultBranch`). Read through the gateway at admission and stored on the head; repositories report the latest head's values. Labels, not identifiers. |

Decoding lives in `pkg/template/gib` (`Decode`, `ParseCommit`, `Fields`,
`LockingScript`). Outpoint fields may be 36 raw bytes or `txid_vout` strings;
the identity may be 33 raw bytes or hex. The lock may precede or follow the
fields and the inscription may precede or follow the lock.

| Token | Value |
| --- | --- |
| Topic manager | `tm_gib` |
| Lookup service | `ls_gib` |
| Parser tag / event | `gib`, plus `gib:{origin}` per repository |
| Queue | `q:gib` |
| REST prefix | `/1sat/gib` |
| Overlay routes | `/1sat/gib/overlay` |

### Ingestion

Broadcasts through the stack are parsed by `pkg/parse/gib.go`, which emits
`gib` and `gib:{origin}` events. The event bridge routes `gib` and `spend:gib`
into `q:gib`; a single OverlaySync worker submits them to the engine in arrival
order. Admission stores the head and records the spend of every gib input in
the same transaction, so a push's predecessor is linked even if the engine
never admitted it. `SpendSync` records deletions (burns) straight from the
indexer's spend events. An optional JungleBus subscription can feed the same
queue for historical sync.

### Storage

Per-topic table `gib_heads` (SQLite or Postgres via the overlay storage
factory): one row per head with decoded fields, the parsed commit (sha, tree,
parents, author, committer, message), `prev_outpoint`, and spend info
(`spend_txid`, `next_outpoint`, `spend_score`), plus `gib_commit_parents`
(head outpoint → parent sha) so the commit DAG is walkable across
repositories: a fork republishes its forked commit verbatim, and its parents
resolve through this index to whichever origin's heads hold them. Scores
follow `types.HeightScore`; block-height updates restamp rows.

## Configuration

Disabled by default.

```yaml
gib:
  mode: embedded          # disabled | embedded
  log_level: info
  routes:
    enabled: true
    prefix: /gib
  sync:
    enabled: false
    subscription_id: ""   # optional JungleBus subscription
    queue_name: gib
    concurrency: 1        # keep 1: heads must apply before the push that spends them
    batch_size: 1000
```

Admin runtime keys: `overlay.gib.enabled`, `overlay.gib.sub_id`,
`overlay.gib.concurrency`, `overlay.gib.batch_size`, `overlay.gib.log_level`.

## Examples

```bash
# Recently active repositories
curl https://api.1sat.app/1sat/gib/repos?limit=20

# Repositories an identity has pushed to
curl https://api.1sat.app/1sat/gib/identity/<pubkey-hex>/repos

# One repository with its current branch heads
curl https://api.1sat.app/1sat/gib/repo/<origin>

# A branch's current head and push history (branch names may contain slashes)
curl https://api.1sat.app/1sat/gib/repo/<origin>/branch/feature/x

# One head
curl https://api.1sat.app/1sat/gib/head/<outpoint>

# A git commit as a DAG node: every head publishing it (any repo, any fork)
# and every head whose commit names it as a parent
curl https://api.1sat.app/1sat/gib/commit/<git-sha>

# BRC-24 lookup: current heads for an origin, hydrated to output-list BEEF
curl -X POST https://api.1sat.app/1sat/gib/overlay/lookup \
  -H 'content-type: application/json' \
  -d '{"service":"ls_gib","query":{"origin":"<origin>"}}'
```

Head JSON:

```json
{
  "outpoint": "txid_0",
  "txid": "…", "vout": 0,
  "origin": "…_0", "branch": "main", "root": "…_3",
  "identity": "02…",
  "commit": { "sha": "…", "tree": "…", "parents": ["…"],
              "author": {"name": "…", "email": "…", "time": 1700000000, "tz": "+0000"},
              "committer": {…}, "message": "…" },
  "prev": "txid_0",
  "spend": { "txid": "…", "next": "txid_0", "score": 900001.000000012 },
  "score": 900000.000000007, "height": 900000
}
```

Resolve content with ORDFS: `/content/{root}/path/to/file` for any head's
tree, and `/content/{outpoint}:-1` to follow a head coin to the branch's
current tip.

## See Also

- `docs/architecture/OVERLAY_SYNC_ROUTING.md`
- `pkg/template/gib` — decoder and reference encoder
- `pkg/ecosystemalias`, `pkg/ordlock` — sibling PushDrop / custom-table overlays
- gib design docs (gib-cli repo, `docs/plans/`)

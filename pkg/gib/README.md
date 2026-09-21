# gib

## Purpose

`gib` is the overlay module for gib, on-chain git on BSV. It indexes **commit
heads**: bare 1-satoshi PushDrop coins that name a repository's branch tip. A
coin's spend chain is the branch's push history, so the module records every
head it sees, links each head to the one it spent, and serves repository,
branch, and publisher views for gibhub.net and other clients. Content
(directory manifests, files, patches) is not served here; ORDFS resolves it
from the root outpoint a head names.

Nothing is inscribed on a head. The commits a push publishes live in the
`.git` object store inside the root it points at, and the module reads them
out of the submission at admission — which is also what a head must carry to
be admitted at all. See **Admission**.

> **A clean break.** The five-field head with the commit inscribed on the
> same output is a different format and no longer decodes. The heads
> published before this fall out of the topic; there is no migration.

## Concepts

| Term | Meaning |
| --- | --- |
| Repository origin | Outpoint of the repository's genesis `ordfs/dir` root. The repo id; the `origin` field everywhere in this API. |
| Root | Outpoint of the root directory manifest a branch currently points at: git's tree for the tip commit, plus `.git`. |
| Commit head | Bare 1-sat PushDrop coin, nothing inscribed: fields `["gib", repository origin, branch, root, identity, branched-from]`. |
| `.git` | The object store gib adds to a published root: every commit object reachable from the tip, named by its sha, plus a `.` entry pointing at the tip commit. gib strips `.git` before hashing, so the tree still verifies against what git computed. git itself refuses a `.git` entry in a tree, so the name can never collide with a real file. |
| Branched-from | The head this one branched from or merged in — the second parent. Empty on an ordinary push, whose only parent is the head it spends; set on a branch's first head, and on a merge *alongside* the spend. A head's parents mirror its commit's parents. |
| Identity | The publisher's BRC-100 identity key (compressed hex). |
| Push | Spending a head and creating the next one for the same origin and branch. |
| Delete | Spending a head with no successor (burn). A wallet operation, not a protocol event: it stops the publisher extending that branch and reclaims the satoshi. It retracts nothing — see **A published branch is permanent**. |
| Owner | The identity that minted the earliest head for an origin. Anyone may mint heads for any origin; the API groups by identity. |
| `.gib` | Optional JSON file in the genesis tree (`name`, `description`, `defaultBranch`). Read from the origin outpoint through the gateway at admission and stored on the head; fixed for the repository's life (rename = new origin). Labels, not identifiers. |

Decoding lives in `pkg/template/gib` (`Decode`, `ParseCommit`, `Fields`,
`LockingScript`). Outpoint fields may be 36 raw bytes or `txid_vout` strings;
the identity may be 33 raw bytes or hex. The lock may precede or follow the
fields. An absent branched-from field is an empty push, which PushDrop
encodes as `OP_FALSE` — the same opcode a single zero byte encodes to, so
both read back as absent. Reading a push out of a submission lives in
`push.go` (`ReadPush`).

| Token | Value |
| --- | --- |
| Topic manager | `tm_gib` |
| Lookup service | `ls_gib` |
| Parser tag / event | `gib`, plus `gib:{origin}` per repository (a reader's feed; see **Ingestion**) |
| Queue | none — submission only |
| REST prefix | `/1sat/gib` |
| Overlay routes | `/1sat/gib/overlay` |

### Admission

A head is admitted only with the push it publishes, judged **from the
submitted BEEF alone** with no network fetch:

- the transaction holding the head's `root` is in the submission, and its
  output at that index decodes as an `ordfs/dir`;
- that directory has a `.git` entry, readable here, and every commit object
  in the store that the submission carries hashes to the sha it is filed
  under — the store is keyed by sha, so a name is proof of content. gib
  hashes with SHA-1, so an entry under a 64-character name is one this
  reader cannot check: it is left alone rather than passed off as verified;
- when the branched-from field is set, the transaction holding that head is
  in the submission too, and that output decodes as a head.

So a client submits its content transactions alongside the head, in one
BEEF. An atomic BEEF would prune everything the head does not spend, so the
submission is a BEEF V2 with the head transaction last.

Every transaction the reading relied on comes back as an **ancillary txid**,
which the engine records against the admitted output: the content a push
published is retained with the head instead of discarded once the submission
is over.

What admission deliberately does **not** require is that the overlay hold the
whole history. Because `.git` is keyed by sha, an object already on chain is
cited at the outpoint holding it rather than republished — which is what lets
a branch fork from another publisher's head without copying anything. A
`.git` entry citing a commit object in an earlier transaction is a reference
the overlay records (sha and outpoint, `held: false`) and may not hold: one
hop verified, every hop named. There is no completeness rule.

### Ingestion: submission only

A head enters `tm_gib` one way — a client submits it, through the overlay's
BRC-22 route at `/1sat/gib/overlay/submit`, with the content transactions
that prove the push in the same BEEF. There is **no queue, no event bridge,
no sync worker and no JungleBus subscription** for this module, and that is
deliberate.

gib is a far more explicit push than anything else in this stack. The client
holds the repository and knows exactly what it is publishing, so the overlay
never has to discover a head or reconstruct a repository from the chain: it
accepts what it is handed, checks it against itself, and keeps it.
Repositories travel between overlays peer to peer, not down a feed. There
will be no full-repository syncing from a chain feed.

It could not have worked the other way round in any case. `OverlaySync`
builds its submission with `beefStorage.BuildFullBeef(headTxid)`, which
carries the head transaction and its ancestry — the previous head and a
funding output. A push's content transactions are not ancestors of the head,
so a head ingested from the feed would arrive without the push it publishes
and be refused by admission, which is fetch-free by design.

**Nothing listens to the chain feed.** `pkg/parse/gib.go` still parses every
commit head the indexer sees and still emits `gib` and `gib:{origin}`, but
those are a reader's feed — `gib:{origin}` is the per-repository SSE
subscription gibhub.net uses for live updates — and nothing in this module
subscribes to them or to `spend:gib`. The only spend it records is the one
the engine observes when it is handed the transaction that made it: a push
taking the branch's previous head as an input, which is what orders a
branch's history.

### A published branch is permanent

A publisher can stop extending a branch — spend its own head, and no further
push can continue that chain — but it cannot retract one. The heads are on
chain, the trees and commit objects they name are on chain, and the overlay
goes on serving every one of them. Anyone can still branch from a head whose
publisher has moved on: nothing about the history or the content changes
when the tip coin is spent.

So deleting is not a protocol event here. It is a wallet operation: you
spend your own token to stop tracking it and reclaim the satoshi, and nobody
else needs to hear about it. The overlay hears about it only if someone
hands it the spending transaction, and all that does is take the head off
the list of *current* ones — the branch's history is served exactly as
before. This is the blockchain; there is no deleting.

> **A consequence for the client, not for this module.** git expects
> `git push gib :branch` to stick. The client burns its token and git
> reports the branch gone, but the overlay has not forgotten anything, and a
> later fetch will advertise that branch again from the heads it holds. That
> is a client-side concern — how gib's remote helper presents a branch its
> publisher has abandoned — and a property of publishing to a chain, not a
> bug in this module. Branch accumulation is a commercialisation question,
> not a design one.

### BRC-24 lookups (`ls_gib`)

Three queries, each naming its `type`. Together they are the whole of what a
client that knows only the overlay endpoint a domain declares in its BRC-180
manifest needs to clone a repository and keep it up to date, without the
host's REST root. They answer in the order a clone needs them:

| `type` | Asks for | Answer |
| --- | --- | --- |
| `branches` | A repository's branches, with each one's tip and the branch to clone from | Freeform: a plain list |
| `headsSince` | One branch's heads from a point forward, oldest first | Output list, one entry per head |
| `txs` | Whole transactions by txid | Freeform: one merged BEEF |

**A query must name its type.** There was a fourth, the typeless `heads`
query, which answered by outpoint / origin / branch / identity / sha with
BRC-24 *formulas*. It is gone. `engine.hydrateOneFormula` loads each formula
with `Storage.FindOutput(ctx, outpoint, nil, nil, true)` — a nil topic — and
this stack's `EngineAdapter.FindOutput` rejects a nil topic with `topic is
required`, so a formula answer never reached a client however the question
was asked. Its one real use was enumerating a repository, which `branches`
now does properly; the same filters are served over REST by
`/1sat/gib/heads`, which works. `TestFormulaHydrationStillRejectsNilTopic`
pins the defect so nobody builds a formula answer here again. Every query
above builds its own answer inside the lookup service.

Neither `branches` nor `txs` is an output list, because neither asks for
outputs. `txs` asks for transactions; an output list would have to give each
entry a meaningless output index. `branches` asks for branches — a branch is
a (repository origin, branch, identity) triple, and what a client wants from
it is a name to sync and a head to sync up to, which it then hands to
`headsSince`, the query whose job is delivering heads with their BEEF.

Conditions a client must act on are reported in the answer's `result`, not
as lookup errors: the overlay HTTP layer collapses every lookup error to an
opaque 500, which a client cannot tell from any other failure. A lookup error
is reserved for malformed input. `result` arrives as a JSON-encoded string,
per the BRC-24 response shape.

**`branches`** — every branch of one repository, ordered by branch name then
publisher. A branch is per (repository origin, branch, identity), so the same
name published by two identities is two entries; that is also why each entry
carries the identity a client must pass back to `headsSince`.

`defaultBranch` and `owner` are the branch and publisher of the **genesis
push** — the earliest head this overlay holds for the repository — which is
the branch a clone starts from. They are on every page, whichever page the
entry itself falls on, so a client never pages looking for the default and
never has to guess it from the `.gib` metadata file. (`.gib`'s
`defaultBranch` is a label the publisher wrote; this is what the chain
shows.)

`tip` is the newest head this overlay holds for that branch: a client that
already has the branch compares before asking for anything, and one that
does not passes the branch and identity to `headsSince`, whose last head is
that tip. `spent` marks a branch whose publisher has stopped extending it —
still listed, still served, still forkable; see **A published branch is
permanent**.

At most 100 branches per page (`limit`, default 20). `more` says the page
stopped short, and `next` is the cursor to echo back as `since`; ordering is
by name rather than by activity, so a push landing between pages cannot move
an entry from one page to another. The answer is built from the index alone
and never touches the BEEF store, so enumerating a repository cannot fail or
truncate for want of a transaction — unlike `headsSince`, which must hand
over BEEF and says so when it cannot.

```jsonc
// query
{"type":"branches","origin":"<repository origin>","limit":100,
 "since":{"branch":"main","identity":"02…"}}
// result (freeform)
{"query":"branches","origin":"…_0","defaultBranch":"main","owner":"02…",
 "branches":[
   {"branch":"feature/x","identity":"02…","tip":"txid_0","sha":"…",
    "root":"…_3","score":900002.000000001},
   {"branch":"main","identity":"03…","tip":"txid_0","sha":"…","root":"…_7",
    "spent":true,"score":900001.000000004}],
 "more":true,"next":{"branch":"main","identity":"03…"}}
```

A repository this overlay holds nothing for is an empty `branches` list with
no `defaultBranch`, not an error: "nothing here" and "I cannot answer" stay
distinguishable.

**`headsSince`** — `since` is exclusive, so a client resumes by passing the
last outpoint it received; empty means from the branch's first head.
`identity` is optional, and when it is set the heads returned are a
subsequence of the spend chain rather than a contiguous run. At most 100
heads per page (`limit`, default 20); `more` says the page stopped short of
the tip.

```jsonc
// query
{"type":"headsSince","origin":"<repository origin>","branch":"main",
 "identity":"02…","since":"txid_0","limit":100}
// result, beside outputs[] — outpoints is index-aligned with outputs
{"query":"headsSince","origin":"…_0","branch":"main","identity":"02…",
 "since":"txid_0","outpoints":["txid_0","txid_0"],"more":false}
```

`result.code` is empty on success. `unknown-since` means `since` names no
head this overlay holds on that branch (a client must not read an empty
answer as "nothing new" without checking it); `missing-beef` means the page
stopped at an indexed head whose transaction is no longer in the BEEF store,
so the client repairs with `gib recover` rather than paging the same gap
forever.

**`txs`** — one merged BEEF holding every requested transaction the overlay
holds, with their proofs. Merging carries shared ancestry once instead of
repeating it per transaction. Requests are deduplicated at the txid, because
that is the unit of exchange: a push writes many outputs in one transaction.
At most **50** txids per request — the answer carries whole transactions, far
heavier than outpoints — and a request over the cap is rejected rather than
truncated, so a short answer never looks like a complete one.

```jsonc
// query
{"type":"txs","txids":["<txid>","<txid>"]}
// result (freeform); beef is BEEF V2, base64 in JSON
{"query":"txs","beef":"<base64 BEEF V2>"}
```

There is no list of what came back and what did not: the client parses the
BEEF and sees for itself which txids are in it. Holding none of them is an
empty BEEF V2 — six bytes, zero transactions — which parses normally. A
failure is an HTTP error with no `result` at all, never an empty BEEF, so the
two are never confused.

The REST routes below are unchanged. `/1sat/beef/{txid}` stays the repair
path for `gib recover`, which legitimately pulls raw transactions from an
upstream.

### Storage

Per-topic tables (SQLite or Postgres via the overlay storage factory).

`gib_heads`: one row per head with its decoded fields, `branched_from`,
`prev_outpoint`, spend info (`spend_txid`, `next_outpoint`, `spend_score`) —
and the head's **tip commit**. The spend columns are written only where the
engine sees a spend: the next push takes the head as an input, or someone
submits the transaction that spent it. They say which head a branch's tip is
now (`spend_txid IS NULL`) and which head succeeded which; they never mean a
head was withdrawn. The commit is no longer on the token: it is
the `.` entry of the root's `.git` store, read out of the submission at
admission. When a push cited that object instead of republishing it (a fork
of a commit already on chain) only its sha is known, which is still the
answer to "what does this head publish". `gib_commit_parents` (head outpoint
→ parent sha of that tip commit) keeps the commit DAG walkable across
repositories: a fork republishes its forked commit verbatim, and its parents
resolve through this index to whichever origin's heads hold them.

`gib_commits`: one row per commit any push's `.git` store names, keyed by
sha alone because a commit is content-addressed — the head that first
published or named it keeps the credit, however many later pushes cite it.
`ref_outpoint` says where the object lives and `held` whether the overlay
read it: a cited hop is recorded without a body, and a later push that
carries the bytes fills it in. Keying by sha is also what keeps the index
linear: every push's store names the whole reachable history, so a row per
push and commit would grow with the square of it.

Scores follow `types.HeightScore`; block-height updates restamp rows.
Eviction of a head (reorg) takes the commits it published with it: a
transaction the chain unmade published nothing.

## Configuration

Disabled by default.

```yaml
gib:
  mode: embedded          # disabled | embedded
  log_level: info
  routes:
    enabled: true
    prefix: /gib
```

There is no `sync` section: no queue, no subscription, no worker to tune.

Admin runtime keys: `overlay.gib.enabled`, `overlay.gib.log_level`.

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

# A git commit as a DAG node: the commit object itself, every head whose tip
# it is (any repo, any fork), and every head whose tip names it as a parent
curl https://api.1sat.app/1sat/gib/commit/<git-sha>

# BRC-24 sync: a repository's branches, and the one to clone from — where a
# client with nothing but the repository origin starts
curl -X POST https://api.1sat.app/1sat/gib/overlay/lookup \
  -H 'content-type: application/json' \
  -d '{"service":"ls_gib","query":{"type":"branches","origin":"<origin>"}}'

# BRC-24 sync: a branch's heads from a point forward, oldest first,
# each carrying its own BEEF
curl -X POST https://api.1sat.app/1sat/gib/overlay/lookup \
  -H 'content-type: application/json' \
  -d '{"service":"ls_gib","query":{"type":"headsSince","origin":"<origin>",
       "branch":"main","since":"<outpoint>"}}'

# BRC-24 sync: whole transactions by txid, returned as one merged BEEF
# (at most 50 per request)
curl -X POST https://api.1sat.app/1sat/gib/overlay/lookup \
  -H 'content-type: application/json' \
  -d '{"service":"ls_gib","query":{"type":"txs","txids":["<txid>"]}}'
```

Head JSON:

```json
{
  "outpoint": "txid_0",
  "txid": "…", "vout": 0,
  "origin": "…_0", "branch": "main", "root": "…_3",
  "identity": "02…",
  "branchedFrom": "txid_0",
  "commit": { "sha": "…", "tree": "…", "parents": ["…"],
              "author": {"name": "…", "email": "…", "time": 1700000000, "tz": "+0000"},
              "committer": {…}, "message": "…" },
  "prev": "txid_0",
  "spend": { "txid": "…", "next": "txid_0", "score": 900001.000000012 },
  "score": 900000.000000007, "height": 900000
}
```

`commit` is the head's tip commit, read from the root's `.git` store, and
`branchedFrom` is present only when the head names a second parent.

Resolve content with ORDFS: `/content/{root}/path/to/file` for any head's
tree, and `/content/{outpoint}:-1` to follow a head coin to the branch's
current tip. A published root also carries `.git`, so
`/content/{root}/.git/{sha}` is the commit object itself.

## See Also

- `docs/architecture/OVERLAY_SYNC_ROUTING.md`
- `pkg/template/gib` — decoder and reference encoder
- `pkg/ecosystemalias`, `pkg/ordlock` — sibling PushDrop / custom-table overlays
- gib design docs (gib-cli repo, `docs/plans/`)

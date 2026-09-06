# BRC-169 lifecycle gate

Status: **In Progress**

Linear: OPL-4473. Targets master after ecosystem-alias wiring PR #30 merged.

## Implemented checks

`pkg/ecosystemalias/http_lifecycle_test.go` exercises real Fiber routes, the
module engine, SQLite membership/events, and filesystem BEEF. It checks default
and custom route prefixes, service discovery, current submission through the
configured broadcaster, historical engine import without broadcasting, alias /
domain / empty-object queries, conflict order, pagination, invalid-query
rejection, non-alias exclusion, spend eviction, and persistence after reopening.
Synthetic Merkle roots and a recording broadcaster replace external chain
infrastructure. This does not prove mainnet funding, wallet custody, arcade
callbacks, or authoritative chain/reorg events.

The HTTP gate exposed two defects that provider-only tests missed:

- Upstream rejects explicit `{}` queries before provider dispatch. Fix:
  https://github.com/bsv-blockchain/go-overlay-services/pull/365
- Formula hydration asks storage for an output without a topic. Single-topic
  module engines now bind that operation to their own topic; unbound adapters
  still reject ambiguous requests. An isolation regression covers this scope.

## Validation and dependency gate

Upstream #365 has merged. Stack also requires the unmerged GASP isolation
changes in upstream #361; switching directly to master would remove those fixes.
Upstream #366 carries the exact #365 patch onto that existing integration branch:
https://github.com/bsv-blockchain/go-overlay-services/pull/366
The fork commit could not be resolved under the official Go module path. Wait
for the compatible revision to become available upstream before updating the pin.

Run `go test ./pkg/ecosystemalias ./pkg/overlay/storage` after the upstream fix
is included in the pinned overlay-services dependency. Until then, the new HTTP
test deliberately fails on the empty query. Do not merge this gate with an
unresolved dependency, disable the test, or silently rewrite `{}` on the client.

For review before an upstream release, copy go.mod/go.sum to a temporary module
file and replace overlay-services there with a checkout of the pinned version
plus PR #365's two-file patch; then run `go test -modfile=<temporary.mod> ./...`.
Do not commit a local-path replacement or change production dependencies as part
of the test rehearsal.

## TypeScript client interoperability

The optional `TestHTTPLifecycleTypeScriptClient` starts the real Fiber listener
on loopback and imports two synthetic confirmed conflicting claims using the
historical engine path. Bun runs the actual SDK client source from PR #41 over
TCP and verifies alias/domain/empty queries, pagination, empty results, output
indices, and locally derived transaction IDs from returned Atomic BEEF.

```sh
BRC169_SDK_ROOT=/absolute/path/to/1sat-sdk go test -modfile=/tmp/brc169-lifecycle.mod ./pkg/ecosystemalias -run TestHTTPLifecycle -count=1 -v
```

The SDK checkout must include #41 and have its client dependencies installed.
The temporary module override is the same review-only dependency gate described
above. Without BRC169_SDK_ROOT this cross-repository check explicitly skips;
the Go HTTP lifecycle gate still runs normally. This proves transport and BEEF
interoperability, not public SHIP/SLAP discovery or real-chain custody.

## Deployed endpoint observation (2026-09-06)

Read-only probes and the actual #41 SDK client against
`https://api.1sat.app/1sat/ecosystemalias/overlay` confirmed both capability
listings. Alias `sigma`, domain `sigmaidentity.com`, and skip=0/limit=1 return
HTTP 200 with an empty output list. Explicit `{}` returns HTTP 400 before
provider dispatch. No claim was imported, submitted, spent, or broadcast.
Empty results cannot exercise populated-output hydration; that fix is supported
by the local populated HTTP gate, not a demonstrated live HTTP 500.

## Still required before rollout

Real staged appliance/BAP compatibility, public discovery and populated real-chain SDK lookup,
PostgreSQL topic-isolation evidence for the complete engine flow, interrupted
writes and duplicate-admission reconciliation, authoritative reorg/rollback and
restart recovery, and finality retention. OPL-4473 remains In Progress.

# BRC-169 lifecycle gate

Status: **In Progress**

Linear: OPL-4473. Based on the ecosystem-alias wiring PR #30.

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

Run `go test ./pkg/ecosystemalias ./pkg/overlay/storage` after the upstream fix
is included in the pinned overlay-services dependency. Until then, the new HTTP
test deliberately fails on the empty query. Do not merge this gate with an
unresolved dependency, disable the test, or silently rewrite `{}` on the client.

For review before an upstream release, copy go.mod/go.sum to a temporary module
file and replace overlay-services there with a checkout of the pinned version
plus PR #365's two-file patch; then run `go test -modfile=<temporary.mod> ./...`.
Do not commit a local-path replacement or change production dependencies as part
of the test rehearsal.

## Still required before rollout

Real staged appliance/BAP compatibility, third-party SDK over live HTTP,
PostgreSQL topic-isolation evidence for the complete engine flow, interrupted
writes and duplicate-admission reconciliation, authoritative reorg/rollback and
restart recovery, and finality retention. OPL-4473 remains In Progress.

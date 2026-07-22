# Microservice Split — Overnight Execution Log

Branch: `microservice-split` (from `master` @ `ab77548`). Running the plan in
`microservice-split-implementation.md` milestone by milestone: implement → review → fix-loop →
next. This log is the morning-review summary; newest status at the bottom of each milestone.

## Decisions I made autonomously (review these first)

1. **M1 parity correction (commit `d1d6111`).** The plan's Task 1.3 specified a call-time
   `IngestTx` closure for the overlay engine. On verifying against master, that closure would
   **newly enable** overlay-submitted transactions to trigger general txo indexing — a path
   master left dormant because its init ordering captured `OutputStore.IngestTx` as nil before
   the indexer set it (master `cmd/server/config.go`: overlay init at :988, IngestTx assigned at
   :1226; adapter skips on nil at `pkg/overlay/storage/adapter.go:58`). That is a runtime
   behavior change (with double-ingest risk) inside a milestone whose contract is byte-for-byte
   parity with master, and it is exactly the overlay→index feedback loop **Milestone 3** is meant
   to wire deliberately. **I reverted to strict parity**: overlay adapter `IngestTx` stays nil in
   `mode: all`, deferred to M3. The capabilities parity diff can't catch this (route surface is
   unchanged) — it was caught by reading the wiring. **If you'd rather ship the enable now, that's
   an M3 conversation; I chose the conservative path for an unattended run.**

## Milestone status

### M1 — pkg/node + mode selection
- Implemented (commits `cd8ad77`, `ae709f2`, `7f59561`) + parity fix (`d1d6111`).
- Gate: `go test ./...` green; `mode: all` capabilities diff vs master baseline **empty**
  (re-verified after the parity fix on port 18080); `mode: opns` boots with only its surface.
- Plan deviations noted by implementer: docs endpoint is `/1sat/api-spec/swagger.json` (not the
  plan's path); paymail uses `Mode` not `Enabled`; `convertSpendsChain` moved alongside
  `convertBeefChain`. All benign.
- Status: **under review** (M1 diff vs master). Proceeding to M2 only after review clears.

### M2 — Queue interface — not started
### M3 — Event-driven lifecycle — not started
### M4 — Remote providers — not started
### M5 — Gateway + per-service admin — not started

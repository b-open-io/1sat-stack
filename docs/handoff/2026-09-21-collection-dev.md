# Collection dev handoff — 2026-09-21

Work sat uncommitted on `feat/gib-head-v2`. It is committed on `feat/collection-dev`.
The server on this machine is **stopped**. Do not start it until you have read
the worker warning below.

## What this is

A local collection overlay: one JungleBus feed, discovery always on, item
workers only when a collection is whitelisted, paid, or `index_all` is set.
BEEF bytes live in Kvrocks. Overlay topic rows are meant to share one Postgres
database instead of a SQLite file per collection.

This box was a stress test of standing up every discovered collection, and a
way to see what data we actually have. The test was stopped because one worker
per collection does not hold.

## Runtime on this machine

| Thing | State |
| --- | --- |
| Server container `1sat-dev-stack-1` | Stopped |
| Kvrocks `1sat-dev-kvrocks-1` | Up, healthy, `127.0.0.1:6666` |
| Postgres | Compose service written, **not started** |
| Volumes | `~/.1sat/data`, `~/.1sat/kvrocks`, `~/.1sat/postgres` (postgres dir may not exist yet) |
| API | `http://127.0.0.1:18080` and `http://mss1.tail8041e2.ts.net:18080`, base path `/1sat` |
| Listen | Host network, port **18080**. Do not publish `18080:8080` through Docker NAT. UFW already allows tcp 18080 on `tailscale0`. |
| Container cap | 16 GB. Production gives this process 32 GB. |
| Image | `deploy/Dockerfile` (server only). The repo-root `Dockerfile` fails: admin UI `better-sqlite3` vs current Bun/node. |

Start, when you mean to:

```sh
cd deploy
ONESAT_ROOT=/home/shruggr/.1sat HOST_UID=1000 HOST_GID=1000 docker compose up -d --build
```

`deploy/config.yaml` is mounted read-only. It is gitignored by the generic
`config.yaml` rule except for the `!deploy/config.yaml` exception.

dgemma user units (`dgemma-vllm`, `dgemma-structured`, `dgemma-v6-proxy`) are
enabled. After a reboot they pin GPU memory. Stop them with
`systemctl --user stop`, not `docker kill` (`Restart=on-failure`).

`1sat-stack/store/` (about 159 MB of Badger) is leftover from an old run. The
container does not use it. It is **not** in this commit. Incomplete data there
does not matter.

## Do not start `index_all` as it stands

`deploy/config.yaml` has `collection.sync.index_all: true`. That flag starts a
worker for every discovered collection, blacklist excepted. Funding is ignored.
The default in code is `false`.

Last run, before the stop:

- 434 discovered collections
- 416 workers started, all reported active
- 420 overlay SQLite files
- 578 open fds, about 1.6 GB RSS
- item-worker cap is 2, so only 2 can process an item at once

The shared limiter caps work in flight. It does not cap how many workers
exist. Each worker is its own goroutine, its own topic manager, and (on
SQLite) its own database. Empty queues are still polled every second against
the Badger store.

The thing to change: start a worker only while its queue has items, and close
the database when it goes idle. Do not leave one open for every collection
forever.

If you bring the stack up with the current config, you get that explosion
again, only the topic rows go to Postgres instead of SQLite files. The
Postgres pool is capped at 32 connections (`pkg/overlay/storage/postgres_factory.go`)
so a startup burst should not trip `max_connections`. That does not fix the
goroutines.

## Postgres is not live yet

Compose adds `postgres:16-alpine` next to Kvrocks:

- user/password/db: `onesat` / `onesat` / `onesat`
- published only on `127.0.0.1:5432`
- volume `~/.1sat/postgres`
- overlay URL in `deploy/config.yaml`:
  `postgres://onesat:onesat@127.0.0.1:5432/onesat?sslmode=disable`

The running binary does not have this yet. Config is mounted, but the image
has to be rebuilt.

Collection lookup SQL is dialect-aware (`pkg/collection/lookup.go`): SQLite
keeps one DB per topic; Postgres uses one `collection_entries` table scoped by
`topic_id`. `go test ./pkg/collection/` passes. Overlay storage Postgres tests
pass. There is no collection-specific testcontainers test yet.

Switching backends does **not** copy the SQLite overlay files. JungleBus
progress is `progress:45c2c785401daf359a3810ab439db604a3253a830654e6d2cd43623b4421baf5`
in `~/.1sat/data/config.db`. If that key is left in place, ingest resumes and
Postgres stays empty of history. Unset it (server down) before a replay:

```sh
go run ./cmd/stack --data-dir /home/shruggr/.1sat/data \
  config unset progress:45c2c785401daf359a3810ab439db604a3253a830654e6d2cd43623b4421baf5
```

Old SQLite files under `~/.1sat/data/overlay` are then unused. Leave them.

## What already lives in Postgres, and what does not

`overlay.storage_backend: postgres` is the switch for every overlay module's
engine tables. Custom lookup tables follow only if they branch on
`TopicID() > 0`.

| Piece | Where it should live | Ready? |
| --- | --- | --- |
| Overlay engine tables | Postgres | Yes |
| Collection `collection_entries` | Postgres | SQL written, not run against the new container |
| gib, bap, ordlock custom tables | Postgres, via `topicID` | Already dialect-aware. Not enabled in this deploy. |
| BSV-21 / shrug `token_outputs` | Would hit the same DB if those modules are on | **No.** `pkg/lookup/bsv21.go` and `pkg/lookup/shrug.go` are still SQLite (`?`, `BLOB`, `INSERT OR REPLACE`, no `topic_id`). Do not enable them on this backend until that is fixed. |
| BEEF bytes | Kvrocks | Yes. Do not move blobs into Postgres. |
| Queues (`q:collection`, `q:tm_col_{id}`) | Badger today. Redis protocol, so Kvrocks if they move. | No Postgres store provider, and there should not be one. |
| Config store, logs | SQLite (`config.db`, log db) | No Postgres implementation. |
| Wallet DB | Archive schema mentions `wallet.db.engine: postgres` | Not implemented in `pkg/wallet`. This server does not embed that wallet. |
| Chaintracks | Files under the data dir | Unchanged. |

The reason Postgres is in the compose is so the relational overlay can share
one database. Queues and BEEF stay on Kvrocks.

## Collection rules already decided

- Topics: `tm_1sat_collection` for roots, `tm_col_{collectionId}` for items.
  `col_` avoids token-id collisions.
- JungleBus test filter `^subtype=collection` matches both `subtype=collection`
  and `subtype=collectionitem`.
- Subscription `45c2c785401daf359a3810ab439db604a3253a830654e6d2cd43623b4421baf5`,
  `from_block` 783968, 4 dispatch workers, 2 item workers.
- Discovery queue is `q:collection`. Roots are submitted directly. Items are
  queued on `q:tm_col_{id}` before a worker exists.
- Fee is 1000 sats/output. `fee_per_output: 0` is treated as unset and replaced
  with 1000. A real zero does not start a worker.
- Active means whitelisted, funded (`balance > 0`), or `index_all`, unless
  blacklisted. Blacklist wins.
- No whitelist-all switch. Keys are `collection.whitelist:{id}`. Admin HTTP
  whitelist is BSV-21 only (`bsv21.whitelist:`).
- Payment addresses are BRC-42 against the anyone counterparty. Invoice is
  `txid_vout`. Identity pubkey
  `02c96292e1fc788be7de3f7bf6bfcbb7b61dc6bda215573c9d3b38b4d229867761`,
  direct address `1Sat5rCg1TwWeC9hVG1tMMfNFza59rDmq`.
- SIGMA message hash matches 1sat-indexer: single SHA256 of
  `inputHash||dataHash`. go-sigma SHA256d is wrong. Fix is in
  `pkg/template/bitcom/sigma.go`.
- JungleBus BEEF storage must not parse a download just to validate it. Proof
  refresh uses the proof route, not `/transaction/beef`
  (`pkg/beef/junglebus.go`).
- Chaintracks bootstrap `https://mainnet.gorillanode.io/api/v1`, mode `api`.
  Missing headers were a missing bootstrap URL, not an old upstream.
- No GASP on collection item sync.

## Other work in this commit

Not only the collection pipeline. Also in the diff, because it was uncommitted
on the same tree:

- Collections browser (`collections/`) and a link from the landing page.
- BSV-21 browser routes and `pkg/bsv21/ui`.
- Admin route/config tweaks and `pkg/config/apply.go` overlay storage wiring.
- `config.example.yaml` notes for the collection overlay.

## Next

1. Change item-worker lifecycle so a collection is open only while its queue
   has work. That is the blocker. `index_all` stays a test switch, default off.
2. Start Postgres, rebuild the server image, and only then turn the overlay
   backend on. Unset the JungleBus progress key if the postgres database should
   be filled from `from_block` 783968.
3. Before any other overlay module is enabled on this URL, make its custom SQL
   dialect-aware the same way collection, gib, bap, and ordlock already are.
   BSV-21 lookup is the known gap.
4. Leave BEEF on Kvrocks. Leave queues on a Redis-protocol store if they leave
   Badger. Do not put either in Postgres.

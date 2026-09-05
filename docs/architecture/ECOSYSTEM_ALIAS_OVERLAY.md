# Ecosystem Alias Overlay

## Scope

The ecosystem-alias module is 1sat-stack's generic BRC-169 implementation. It
provides one topic manager, one lookup service, durable lifecycle state, and the
standard overlay HTTP surface:

```text
tm_ecosystemalias
        │ admission and spend lifecycle
        ▼
topic-scoped overlay storage (events + outputs)
        │
        ▼
ls_ecosystemalias ──► BRC-24 output-list (Atomic BEEF + output index)
```

It does not own a public hostname and has no Sigma-specific policy.

## Responsibility boundaries

The module is responsible for:

- decoding the exact six-field, lock-after BRC-48 claim;
- validating normalization, positive satoshis, and the certifier signature;
- retaining all valid conflicting alias/domain claims;
- tracking admission, spend, block placement, eviction, rollback, and reorg
  lifecycle state;
- querying by normalized alias or normalized domain;
- hydrating results as standard BRC-24 Atomic BEEF output-list entries.

The module is not responsible for:

- fetching a domain manifest during admission;
- deciding which conflicting claim an application should prefer;
- proving bidirectional consent between the claim and a domain manifest;
- provisioning hosts or DNS;
- creating or broadcasting SHIP/SLAP advertisements.

The module does not fetch manifests. Resolvers and applications fetch them and
apply consent/conflict policy.
This prevents network availability or mutable web content from changing the
deterministic result of on-chain topic admission.

## Runtime composition

`ecosystemalias.mode: embedded` runs the module inside the 1sat-stack process.
It depends on shared overlay infrastructure:

- the per-topic SQLite factory or PostgreSQL factory;
- the shared transaction-to-topic index;
- BEEF storage for Atomic BEEF lookup responses;
- the overlay route configuration;
- the main store when the optional queue worker or JungleBus subscriber runs.

The module's `embedded` mode is independent of the database backend. A
single-node appliance can use SQLite; an appliance with an external database
can use PostgreSQL.

## HTTP contract

With the default server base path and module prefix, clients send:

```http
POST /1sat/ecosystemalias/overlay/lookup
Content-Type: application/json

{
  "service": "ls_ecosystemalias",
  "query": {
    "alias": "sigma",
    "limit": 100
  }
}
```

The query object has exactly one mode:

- `alias: string`
- `domain: string`

It may also include `limit` (1–500) and `skip` (default 0). Full topic membership
is GASP (`FindUTXOs` / ingest scores), not a lookup mode. Responses use `type: "output-list"`, with an `outputs` array
containing base64 Atomic BEEF and the claim's `outputIndex`.

```json
{
  "type": "output-list",
  "outputs": [
    { "beef": "<base64 Atomic BEEF>", "outputIndex": 0 }
  ],
  "result": ""
}
```

The same standard route group also exposes topic-manager and lookup-provider
documentation. A custom module prefix changes the group as a unit. It never
creates separate `/alias` or `/domain` REST resources.

## Configuration layers

Static YAML and `ONESAT_` environment settings use the module configuration
tree:

```yaml
overlay:
  mode: embedded
  storage_backend: sqlite
  storage_path: overlay

ecosystemalias:
  mode: embedded
  routes:
    enabled: true
    prefix: /ecosystemalias
  sync:
    enabled: false
    subscription_id: ""
    queue_name: ecosystemalias
    concurrency: 8
    batch_size: 1000
```

Runtime settings managed in the admin UI use the
`overlay.ecosystemalias.*` keys documented in the package README. All exposed
module settings require a process restart; the UI presents them as
"Save & Restart".

The sync worker and JungleBus subscription are separate concepts. Enabling the
worker drains the `ecosystemalias` queue. Supplying a subscription ID adds a
JungleBus source for historical ingestion. Another route may fill the queue
without a JungleBus subscription.

## Storage

SQLite creates `tm_ecosystemalias.db` under `overlay.storage_path` plus the
shared `tx_topics.db`. PostgreSQL keeps the engine and claim tables in the
shared database and scopes them by topic ID. The claim store uses bytewise
ordering for deterministic cross-backend alias/domain results.

The lookup returns only unspent claims that still belong to
`tm_ecosystemalias`. It loads the exact topic output and all required ancestry,
then serializes Atomic BEEF rooted at that transaction.

## Proposed Sigma appliance

The proposed Sigma host is a dedicated deployment of the generic 1sat-stack
binary with only the required shared services and selected identity modules
enabled. A Sigma-owned name such as `overlay.sigmaidentity.com` keeps this
identity infrastructure separate from the `api.1sat.app` catch-all while
reusing the same deployable stack.

This repository does not establish that host. Deployment manifests, DNS,
monitoring, and SHIP/SLAP publication remain explicit rollout steps. Until
those steps complete, local configuration examples are not evidence that a
production overlay is live or advertised.

## Readiness checks

Before a host is advertised:

1. verify disabled mode registers no route or capability;
2. verify the chosen prefix owns the standard route group;
3. verify `tm_ecosystemalias` and `ls_ecosystemalias` documentation discovery;
   4. verify alias, domain, pagination, and conflict results on both
   configured storage backends;
5. verify spend, eviction, confirmation, rollback, restart, and reorg behavior;
6. add host-level health checks, backups, logs, and alerting;
7. only then publish the intended discovery advertisements.

## Related documentation

- [`pkg/ecosystemalias/README.md`](../../pkg/ecosystemalias/README.md)
- [`OVERLAY_ARCHITECTURE.md`](OVERLAY_ARCHITECTURE.md)
- [`OVERLAY_SYNC_ROUTING.md`](OVERLAY_SYNC_ROUTING.md)

# Ecosystem Alias

## Purpose

`ecosystemalias` is the generic BRC-169 ecosystem-alias overlay module. It
validates on-chain alias claims, retains their lifecycle in topic-scoped
storage, and answers standard BRC-24 lookups by alias, domain, or enumeration.

The module has no Sigma-specific behavior. A Sigma appliance can enable it,
but the same module can be enabled by any independently operated 1sat-stack.

## Contract

| Token | Value |
| --- | --- |
| Topic manager | `tm_ecosystemalias` |
| Lookup service | `ls_ecosystemalias` |
| Protocol | `ecosystem-alias` |
| Version | `1` |
| Default lookup path | `POST /1sat/ecosystemalias/overlay/lookup` |

The topic manager accepts a positive-satoshi BRC-48 PushDrop output with
exactly six fields:

1. ASCII protocol `ecosystem-alias`
2. ASCII version `1`
3. normalized alias
4. normalized RFC 1123 FQDN
5. compressed 33-byte certifier key
6. DER ECDSA signature

The signature covers one SHA-256 digest of the raw concatenation of fields
1–5. The certifier key in field 5 verifies the signature. Alias and domain
values must already be normalized because they are signed.

The overlay deliberately keeps conflicts. Multiple unspent claims for the
same alias or domain are returned in deterministic order so the resolver can
apply policy with full information.

Admission validates only the on-chain BRC-169 claim. It does not fetch the
claimed domain's manifest. Manifest discovery and bidirectional consent (for
example, checking `metanet.handles.aliases`) belong to the resolver or
application using the lookup result.

## Configuration

The module is disabled by default. Only `disabled` and `embedded` modes are
supported. `embedded` means the module runs in the current process; the shared
overlay storage may still use SQLite or PostgreSQL.

### Default / disabled

```yaml
ecosystemalias:
  mode: disabled
```

### Embedded with SQLite

```yaml
overlay:
  mode: embedded
  storage_backend: sqlite
  storage_path: overlay

ecosystemalias:
  mode: embedded
  log_level: info
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

SQLite creates `tm_ecosystemalias.db` below `overlay.storage_path`. The claim
table lives in that topic database.

### Embedded with PostgreSQL

```yaml
overlay:
  mode: embedded
  storage_backend: postgres
  storage_url: postgres://user:password@postgres:5432/onesat?sslmode=require

ecosystemalias:
  mode: embedded
  routes:
    enabled: true
    prefix: /ecosystemalias
```

PostgreSQL uses the shared overlay database and isolates engine and claim rows
by topic ID.

### Custom route prefix

```yaml
ecosystemalias:
  mode: embedded
  routes:
    enabled: true
    prefix: /identity
```

With the default server base path, the BRC-24 lookup then moves to
`POST /1sat/identity/overlay/lookup`. No alias- or domain-specific REST routes
are registered.

### Admin UI settings

The admin UI writes these runtime settings to `{data_dir}/config.db` and
restarts the server when they change:

| Runtime key | Meaning | Default |
| --- | --- | --- |
| `overlay.ecosystemalias.enabled` | Select embedded mode | `false` |
| `overlay.ecosystemalias.routes_enabled` | Register standard overlay routes | `true` |
| `overlay.ecosystemalias.route_prefix` | Module mount prefix | `/ecosystemalias` |
| `overlay.ecosystemalias.sync_enabled` | Run the queue worker | `false` |
| `overlay.ecosystemalias.sub_id` | Optional JungleBus subscription | empty |
| `overlay.ecosystemalias.concurrency` | Queue-worker concurrency | `8` |
| `overlay.ecosystemalias.batch_size` | JungleBus page batch size | `1000` |
| `overlay.ecosystemalias.log_level` | Component log level | `info` in the UI |

The subscription is optional. Enable the worker without a subscription when
another ingestion path fills the `ecosystemalias` queue.

## BRC-24 lookup

Send a JSON lookup question to the module's standard overlay endpoint:

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

Exactly one query mode is allowed:

```json
{ "domain": "sigmaidentity.com", "limit": 100 }
```

```json
{ "findAll": true, "limit": 100 }
```

An opaque `cursor` may accompany the same mode and normalized value. The
default page size is 100 and the maximum is 500.

The response is the standard BRC-24 `output-list` envelope. Each `beef` value
is base64-encoded Atomic BEEF and `outputIndex` identifies the claim output:

```json
{
  "type": "output-list",
  "outputs": [
    {
      "beef": "<base64 Atomic BEEF>",
      "outputIndex": 0
    }
  ],
  "result": ""
}
```

The current overlay transport cannot carry cursor metadata on an output-list.
Compatible clients derive the next cursor from the final returned outpoint.

## Operations and deployment boundary

Enabling this package only starts a local module. It does not deploy a host,
configure DNS, or publish SHIP/SLAP advertisements.

The proposed Sigma appliance is an independently operated 1sat-stack instance
with the required shared services and selected identity modules, exposed at a
Sigma-owned hostname such as `overlay.sigmaidentity.com`. That hostname,
deployment, and its advertisements are rollout work; they are not live merely
because this module is present. `api.1sat.app` is not implied by this package.

Before advertising an instance:

1. choose SQLite for a single-node appliance or PostgreSQL for shared/durable
   database operations;
2. enable the shared overlay engine and this module;
3. verify the capability list includes `ecosystemalias`;
4. query the topic and lookup documentation endpoints for
   `tm_ecosystemalias` and `ls_ecosystemalias`;
5. exercise alias, domain, conflict, enumeration, spend, restart, and reorg
   behavior against staging;
6. publish discovery advertisements only after the public route and monitoring
   are ready.

## See also

- [`docs/architecture/ECOSYSTEM_ALIAS_OVERLAY.md`](../../docs/architecture/ECOSYSTEM_ALIAS_OVERLAY.md)
- [`docs/architecture/OVERLAY_ARCHITECTURE.md`](../../docs/architecture/OVERLAY_ARCHITECTURE.md)
- [BRC-169](https://github.com/bitcoin-sv/BRCs/blob/master/peer-to-peer/0169.md)
- [BRC-24](https://github.com/bitcoin-sv/BRCs/blob/master/peer-to-peer/0024.md)
- [BRC-48](https://github.com/bitcoin-sv/BRCs/blob/master/scripts/0048.md)

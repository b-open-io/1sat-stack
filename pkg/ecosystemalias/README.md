# Ecosystem Alias

`ecosystemalias` is the generic BRC-169 overlay module. It admits on-chain
alias claims, indexes them as overlay events, and answers BRC-24 lookups by
alias or domain.

The module has no Sigma-specific behavior.

## Contract

| Token | Value |
| --- | --- |
| Topic manager | `tm_ecosystemalias` |
| Lookup service | `ls_ecosystemalias` |
| Protocol | `ecosystem-alias` |
| Version | `1` |
| Default lookup path | `POST /1sat/ecosystemalias/overlay/lookup` |

A claim is a positive-satoshi BRC-48 PushDrop with six fields: protocol,
version, alias, domain, certifier key, DER signature. The signature is over
SHA-256 of fields 1–5 concatenated. Conflicts stay queryable. The module does not fetch
manifests. Enabling it does not publish SHIP/SLAP advertisements.

Lookup indexes `alias:` and `domain:` overlay events. Spends live on
`outputs.spend_txid`. Event order is `HeightScore` then `vout`. Paging is
`skip` + `limit` (default skip 0, limit 100, max 500). Full topic membership
is GASP (`FindUTXOs` / ingest scores), not a lookup mode.

## Configuration

Disabled by default. Modes: `disabled`, `embedded`.

```yaml
ecosystemalias:
  mode: embedded
  routes:
    enabled: true
    prefix: /ecosystemalias
  sync:
    enabled: false
    queue_name: ecosystemalias
    concurrency: 8
    batch_size: 1000
```

Admin runtime keys: `overlay.ecosystemalias.enabled`, `routes_enabled`,
`route_prefix`, `sync_enabled`, `concurrency`, `batch_size`, `log_level`.

## BRC-24 lookup

```http
POST /1sat/ecosystemalias/overlay/lookup
```

```json
{ "alias": "sigma", "limit": 100, "skip": 0 }
```

Exactly one of `alias` or `domain`. The engine hydrates formulas to:

```json
{ "type": "output-list", "outputs": [{ "beef": "<base64>", "outputIndex": 0 }] }
```

## See also

- [`docs/architecture/ECOSYSTEM_ALIAS_OVERLAY.md`](../../docs/architecture/ECOSYSTEM_ALIAS_OVERLAY.md)
- [BRC-169](https://github.com/bitcoin-sv/BRCs/blob/master/peer-to-peer/0169.md)

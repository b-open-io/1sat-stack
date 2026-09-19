# Sigma Identity Appliance Topology

Date: 2026-09-04

Scope: OPL-4470 consumer inventory and deployment boundary

Decision status: target architecture; no DNS, deployment, advertisement, or consumer cutover is authorized by this document

## Decision

Run Sigma's BAP and BRC-169 ecosystem-alias overlays as an independent
identity appliance built from the configurable `1sat-stack` binary. Its
canonical production name is `overlay.sigmaidentity.com`.

The appliance profile is:

- BAP;
- ecosystem alias (`tm_ecosystemalias` and `ls_ecosystemalias`); and
- the core services required to validate, store, hydrate, synchronize, and
  serve those overlays.

Do not add the alias workload to `api.1sat.app`. Do not remove BAP or any other
module from that shared service. Do not repoint `sigma.1sat.app` or change
`api.sigmaidentity.com` as part of provisioning the new appliance.

`overlay.sigmaidentity.com` is a DNS name, not the host itself. In this
document, a **host** means an independently deployable failure domain: its own
process or container, compute allocation, persistent state, release, secrets,
monitoring, and rollback. It may use the same cloud provider and the same
`1sat-stack` image as another host, but it must not share an ephemeral disk,
process lifecycle, service key, or release operation with `api.1sat.app`.

The resulting deployment boundary is:

```text
Internet
  |
  +-- api.1sat.app ---------------- existing shared 1Sat deployment (unchanged)
  |
  +-- sigma.1sat.app -------------- existing alternate 1Sat ingress
  |                                  (same app observed; purpose not proven)
  |
  +-- api.sigmaidentity.com ------- existing BAP + BSocial API (unchanged)
  |
  `-- overlay.sigmaidentity.com --- new DNS/TLS ingress
                                        |
                                        `-- Sigma identity appliance
                                            - dedicated 1sat-stack release
                                            - BAP + ecosystem-alias profile
                                            - dedicated state and service key
                                            - private proof/storage dependencies
```

A staging appliance is a separate environment with its own compute, volume,
database namespace, and non-production key. Its public name is still an open
operations decision. Production DNS must not be bound until the staging gates
in this document pass.

## What is proven today

The following facts come from the listed repositories and read-only public
checks. Public observations show behavior, not authoritative cloud or DNS
control-plane ownership.

### `1sat-stack` is a configurable appliance

- The repository builds one Go server and one container image. Protocol
  overlays are enabled independently, while the server mounts only initialized
  module routes.
- The overlay engine requires TXO storage. Module engines receive per-topic
  storage, BEEF storage, a chain tracker, an optional P2P bus, and a broadcaster.
- BAP is disabled by default and, when embedded, registers `tm_bap`, a BAP
  lookup, optional standard overlay routes, and optional sync.
- Overlay persistence can be per-topic SQLite files or PostgreSQL with topic
  isolation. The general store is Badger or Redis. BEEF supports filesystem,
  Badger, Redis, JungleBus, and volatile LRU providers.
- The container starts the server with `--data-dir /data`, but the repository
  contains no production deployment manifest or DNS configuration.
- `GET /1sat/health` is a shallow liveness endpoint. It always reports
  `status: ok`, version and uptime, and adds a block height when chaintracks has
  one. It does not probe topic storage, BEEF retrieval, sync lag, queues, or
  lookup correctness.

These facts are implemented in `cmd/server/config.go`, `cmd/server/main.go`,
`pkg/overlay/config.go`, `pkg/bap/config.go`, `pkg/beef/config.go`,
`pkg/store/config.go`, and the [overlay architecture](../architecture/OVERLAY_ARCHITECTURE.md).
The dependency summary is in the
[module dependency map](MODULE_DEPENDENCY_MAP.md).

### Current public host observations

Read-only checks on 2026-09-04 found:

| Public name | Observation | What it does and does not prove |
| --- | --- | --- |
| `1sat.app` | Redirected to `www.1sat.app`. | The product website is distinct from the API surface. |
| `api.1sat.app` | `/1sat/health` returned 200; `/1sat/capabilities` advertised BEEF, TXO, owner, BSV21, BAP, OPNS, market, overlay, ORDFS, chaintracks, Arcade, admin, sweep, and paymail. | This is the shared, broad 1Sat API. It does not prove which modules should be enabled on a new identity appliance. |
| `sigma.1sat.app` | Returned the same health height/uptime, identical capability list, and a byte-identical OpenAPI document as `api.1sat.app`. It used a different public ingress (`nginx` versus Cloudflare). | This is strong evidence that both names currently reach the same application release, and likely the same running deployment. It is not control-plane proof that their origins or volumes are identical. |
| `api.sigmaidentity.com` | Resolved through Railway, served a “SIGMA - Decentralized Identity API” landing page, exposed `/v1` BAP and social routes, and returned the existing `{status:"OK",result:...}` envelope. No health route was found at the two checked paths. | The live surface matches the `bsocial-overlay` server contract. The exact Railway project, database, backup, and release ownership remain unverified. |
| `social.sigmaidentity.com` | Served a Vercel application. | It is an existing product consumer/failure domain and is outside this implementation. |
| `overlay.sigmaidentity.com` | Did not resolve. | DNS/TLS and a backing deployment are not wired yet. |

Historical `1sat-sdk` planning material names `ovh-n0001` as the host for
`api.1sat.app`, but that is not current infrastructure evidence. The operations
owner must verify the live compute, volumes, reverse proxy, deployment unit,
and backup jobs before any cutover.

## Consumer and compatibility inventory

### `api.1sat.app`

This is a shared platform endpoint, not a suitable place to silently add the
Sigma alias service or narrow its module set.

- `1sat-sdk` defines it as the mainnet default. `OneSatServices` constructs
  clients for chaintracks, BEEF, transaction broadcast, BAP, BSV21, TXO, owner,
  ORDFS, market, OPNS, overlay, and wallet storage from that base URL.
- `yours-wallet` creates `OneSatServices('main')` without an alternate base URL
  in multiple runtime paths. Its network dependencies include owner discovery,
  TXO search, BEEF/raw transaction loading, stack submission, BSV21, and ORDFS
  content. Its identity and profile reads use `wallet.listOutputs` against the
  local BAP basket; they do not call the network BAP lookup client.
- `bsv-bap` defaults its BAP server to
  `https://api.1sat.app/1sat/bap` for identity and attestation lookups.
- Sigma Auth also uses `api.1sat.app` for ORDFS media and directly polls
  `/1sat/owner/:address/balance` from the account page.

Therefore, an outage, schema change, route removal, or incompatible BAP
response on `api.1sat.app` can affect wallets and applications unrelated to the
Sigma alias rollout. The new appliance must not be created by changing this
deployment in place.

### `sigma.1sat.app`

Sigma Auth documentation and diagnostic scripts still reference BAP resolution
under `sigma.1sat.app`. No inspected SDK or Yours Wallet runtime default selects
this name. The live comparison supports describing it only as an alternate
ingress to the same broad 1Sat application. Its identity-oriented purpose is
inferred from the hostname and those references, not proven by infrastructure
configuration.

Keep the name and current contract stable until access logs, DNS/origin config,
and all repositories outside this inventory have been searched. A matching
OpenAPI document is not permission to repoint the name.

### `api.sigmaidentity.com` and `bsocial-overlay`

The `bsocial-overlay` repository is a distinct Go service with:

- BAP and BSocial topic managers and lookup services;
- MongoDB identity, attestation, profile, post, like, and relationship data;
- Redis queues, BEEF cache, publication, and SSE;
- custom `/v1/identity`, `/v1/profile`, `/v1/post`, and `/v1/social` routes; and
- a standard overlay engine surface.

`sigma-auth-web` documentation references this host, including some historical
`/api/v1` examples, while the inspected service and live API use `/v1`.
That mismatch belongs in the deferred Sigma Social issue set (OPL-4452–4460),
not in this deployment change.

The new appliance may eventually replace a subset of BAP reads only after
response-contract parity and consumer tests prove it safe. It must not replace
BSocial search, relationships, SSE, image proxying, or MongoDB-backed product
behavior. Do not upgrade or deploy `bsocial-overlay` in this work.

### `social.sigmaidentity.com`

Treat Sigma Social as an existing consumer of the `api.sigmaidentity.com`
contract. Record defects as tickets. Do not point it at
`overlay.sigmaidentity.com`, move its data, change CORS, or update its runtime
dependencies under the BRC-169 rollout.

### Sigma Auth

Sigma Auth constructs `@1sat/client`'s `BapClient` from the required
`ONESAT_BASE_URL` environment variable. That is the safe, reversible cutover
seam: staging can select the staging appliance without changing SDK defaults,
Yours Wallet, or the shared API. The production value is an operations fact
that was not available in the repository and must be recorded before cutover.

Sigma Auth also maintains local profile/cache/database fallbacks. A successful
new-host lookup is not sufficient proof of login safety; key rotation,
`validByAddress`, profile shape, errors, cache invalidation, and unpublished
identity behavior need contract canaries.

## Appliance runtime contract

### Ingress and public surface

Terminate TLS for `overlay.sigmaidentity.com` at a dedicated ingress and route
only to the Sigma appliance. Preserve request bodies needed for Atomic BEEF and
set explicit size/time limits compatible with standard overlay submission.

Publicly expose only:

- `/1sat/health` and `/1sat/capabilities`;
- the BAP read routes proven necessary by Sigma clients;
- the ecosystem-alias standard overlay submission and BRC-24 lookup surface;
  and
- standard synchronization/discovery routes only when the advertisement and
  peer threat model is approved.

Keep admin, profiling, storage, raw TXO, internal BEEF/proof, chaintracker,
queue, and broadcaster surfaces private unless a concrete public consumer is
documented. This is an ingress allowlist; some core services may still run
inside the process.

### Compute and module profile

Use a dedicated deployment of the same versioned `1sat-stack` image. Enable
BAP, ecosystem alias, overlay infrastructure, and their required core. Disable
BSocial, BSV21, OPNS, market, paymail, owner sync, public wallet storage, and UI
modules unless a measured dependency is added to this inventory.

The exact core toggle set must be frozen after the ecosystem-alias module is
wired. At minimum, overlay processing needs per-topic storage, TXO ingestion,
BEEF, chaintracks, a general store, and the server identity key. Historical
ingestion additionally needs an approved JungleBus subscription or another
proven sync source. Transaction broadcast and P2P are separate choices; do not
enable them merely because the binary supports them.

### Persistence and proof dependencies

The appliance's durable state includes:

- config and operational SQLite databases under the data directory;
- the general Badger store, or its external Redis equivalent;
- per-topic overlay SQLite files plus the transaction/topic index, or a
  dedicated PostgreSQL database/schema;
- durable BEEF sufficient to hydrate BRC-24 `output-list` responses;
- chaintracks/header state;
- BAP and ecosystem-alias lookup indexes; and
- logs needed for rollback diagnosis, subject to retention policy.

An LRU-only BEEF chain is not durable. Configure a filesystem/Badger tier under
the mounted data volume, or a separately backed-up Redis service, and then use
JungleBus only as an explicit fallback/source. The current no-config BEEF
default resolves beneath the process user's home directory rather than
`--data-dir`; production must set the path explicitly so it cannot escape the
backed-up volume.

Atomic BEEF hydration and proof validation are part of lookup correctness, not
optional convenience. Readiness must fail when a stored claim cannot be loaded
with the transaction/proof material promised by the BRC-24 output-list result.

### Secrets and identity

Provision a dedicated production service wallet key through the platform
secret store using the `wallet.server_private_key` environment mapping. Do not
reuse the `api.1sat.app`, staging, Sigma Auth, alias certifier, or advertising
wallet key.

If the key is omitted, the current server generates and persists `server.key`
under the data directory. That behavior is useful for local development but is
not an acceptable implicit production custody decision. The production key
needs an owner, encrypted backup, recovery test, rotation procedure, and a rule
for what happens to P2P/service identity after rotation.

Other possible secrets include PostgreSQL/Redis/JungleBus credentials, API
authentication, and overlay/ARC callback tokens. Supply them from the secret
manager, never from committed YAML or container layers. SHIP/SLAP
advertisements and the alias claim wallet remain separate authorization steps;
this repository contains no evidence that either is currently wired.

### Liveness, readiness, and canaries

Use `/1sat/health` for process liveness only. A readiness gate must additionally
verify:

1. the expected capability/module list and exact build version;
2. chain height is present and within the approved lag;
3. topic storage and the transaction/topic index are writable/readable;
4. a known BAP identity and rotation-validity fixture return the expected
   direct response shape;
5. the known Sigma ecosystem claim can be found by alias and domain;
6. conflict enumeration, pagination, spend state, and restart persistence work;
7. each returned outpoint can be hydrated as valid Atomic BEEF; and
8. sync queues are advancing and expose no sustained error backlog.

Run read-only production canaries after cutover. Transaction admission,
spending, rejection, and reorg tests belong in staging or a controlled fixture
environment, not against the live Sigma claim.

### Backup and restore

Choose one supported persistence topology and document it before deployment.
For the initial single-node SQLite/Badger profile:

- mount one dedicated durable volume at `/data`;
- explicitly place overlay, BEEF, store, config, session, chaintracks, and
  diagnostic state on that volume;
- take application-consistent snapshots (including SQLite WAL state) or stop
  the process for the snapshot;
- retain the service key backup separately from data snapshots; and
- restore onto a blank staging host, then run every readiness canary.

For PostgreSQL/Redis, use dedicated databases/namespaces and provider-native
point-in-time backup. A successful snapshot is not a backup gate until a blank
host restore has reproduced alias ordering, BAP rotation history, spend state,
BEEF hydration, and the claimed chain height.

## Non-breaking rollout and rollback

1. Merge and release the complete ecosystem-alias module, including lifecycle
   and reorg behavior. A contract-only package is not deployable.
2. Provision a staging failure domain with a non-production key and dedicated
   state. Do not use a shared `api.1sat.app` volume or database namespace.
3. Sync BAP and ecosystem-alias history from the approved source. Prove restart
   and restore before exposing public traffic.
4. Run the readiness/canary matrix, including the existing Sigma claim as a
   read-only fixture. Compare BAP responses with the current Sigma Auth
   dependency.
5. Provision production independently and repeat sync, restore, and canaries.
6. Bind `overlay.sigmaidentity.com` only after DNS/TLS, observability, custody,
   and rollback owners approve the release.
7. Move Sigma Auth first through `ONESAT_BASE_URL`, using a reversible
   environment-only change. Hold `api.1sat.app`, `sigma.1sat.app`,
   `api.sigmaidentity.com`, social, Yours Wallet, and `bsv-bap` defaults steady.
8. Advertise the production topic manager and lookup service only after the
   public URL serves the exact tested build and its service key/custody is
   approved.

Rollback first means stopping new traffic to the failed appliance and restoring
the prior Sigma Auth environment value. It does not mean repointing a legacy
hostname, spending or replacing the existing alias claim, or deleting overlay
state. Preserve the failed deployment and logs until its indexed height and
lifecycle state have been compared with the last good snapshot.

If a SHIP/SLAP advertisement has already been published on-chain, withdrawing
or revoking it is a separate signed transaction, not an ingress toggle. That
transaction requires explicit authorization, an operator runbook, approved key
custody, and a plan for propagation plus peer/cache expiry. Traffic rollback can
complete without silently granting authority to perform that on-chain action.

## Release gates

- **Implementation gate:** strict decoder, topic manager, durable store,
  complete alias/domain/enumeration lookup, lifecycle rollback/reorg handling,
  module wiring, settings, and conformance tests are merged.
- **Consumer gate:** Sigma Auth contract tests pass; Yours Wallet and SDK
  defaults remain unchanged; BSocial behavior is explicitly out of scope.
- **Infrastructure gate:** staging and production compute, volume/database,
  ingress, secrets, monitoring, backup, restore, and rollback owners are named.
- **Proof gate:** known BAP and ecosystem-alias results hydrate as Atomic BEEF
  and survive restart/restore.
- **Discovery gate:** the advertised topic/lookup names and URL match the
  deployed build; key custody and withdrawal/replacement procedures are signed
  off.
- **Cutover gate:** production DNS and the Sigma Auth environment change are
  independently reversible. No legacy hostname is changed in the same window.

## Open evidence gaps

The rollout remains blocked on answers that cannot be derived from these
repositories:

- What compute, provider account, volumes, reverse proxy, and release process
  currently back `api.1sat.app` and `sigma.1sat.app`? Are they one origin?
- What are the current backup/restore RPO and RTO, and has either been tested?
- Which hostname will staging use, and who owns the Sigma DNS zone and TLS?
- Will the appliance start with SQLite/Badger or PostgreSQL/Redis? What capacity
  and high-availability threshold triggers a change?
- Which authoritative source/subscription provides historical BAP and BRC-169
  transactions, and from what block?
- Which production `ONESAT_BASE_URL` does Sigma Auth currently use?
- Do traffic logs reveal consumers of `sigma.1sat.app` or
  `api.sigmaidentity.com` that are absent from the seven inspected repositories?
- Who controls the service wallet, alias certifier, and SHIP/SLAP advertisement
  keys, and what is the recovery/rotation approval path for each?
- What exact readiness thresholds cover chain lag, queue age, lookup latency,
  storage errors, and BEEF hydration failures?

## Evidence inspected

Repository snapshots inspected locally:

- `b-open-io/1sat-stack` at `1595f29`: server configuration, module
  initialization, route registrar, storage implementations, Dockerfile,
  architecture and research docs, plus the unmerged OPL-4439 contract branch;
- `b-open-io/sigma-auth` at `9754e367`: BAP client, resolver, profile fallback,
  runtime environment seam, account media/service references, diagnostics, and
  identity docs;
- `b-open-io/sigma-auth-web` at `a24c254`: published BAP integration and profile
  documentation;
- `b-open-io/bsocial-overlay` at `95ab9e3`: server routes, BAP/BSocial topic and
  lookup services, MongoDB/Redis/BEEF dependencies, and environment contract;
- `b-open-io/1sat-sdk` working tree at `7ae3a4f`: mainnet constants,
  `OneSatServices`, BAP client, wallet blocks, skills, and deployment plans;
- `BitcoinSchema/bap` at `c732cfc`: API default, identity/attestation lookup
  methods, and package contract; and
- `yours-org/yours-wallet` at `b14be47`: service construction, sweep paths,
  identity hooks/actions, BEEF/TXO/owner usage, and user documentation.

Some source working trees contained unrelated user changes. They were inspected
read-only and were not modified. Live checks were limited to public DNS and
HTTP behavior; no cloud console, DNS account, logs, databases, deployments, or
wallets were accessed.

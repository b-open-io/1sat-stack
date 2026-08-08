# OrdFS

## Purpose

OrdFS is an HTTP gateway that serves on-chain inscribed content over standard HTTP. It resolves Bitcoin outpoints to their inscription or B protocol content, tracks ordinal transfer chains, merges MAP metadata across sequences, handles directory traversal for `ord-fs/json` inscriptions, and streams large files chunked across multiple inscriptions.

## Concepts

### Content Resolution

An outpoint (`txid_vout`) points to a transaction output. OrdFS loads the output's locking script and extracts content from two sources, in priority order:

1. **Inscription** -- ORD envelope with content type and body
2. **B protocol** -- Bitcom `19HxigV4QyBv3tHpQVcUEQyq1pzZVdoAut` data

If both are present, the inscription content type takes precedence, but B protocol data is used as fallback content.

### Sequence Model

The `seq` parameter controls how OrdFS resolves content along the ordinal transfer chain. This is the most important concept in OrdFS.

| seq value | Behavior |
|-----------|----------|
| **nil** (omitted) | Return raw content from the exact outpoint. No origin resolution, no crawl. |
| **-2** | Origin only. Backward crawl to find the origin outpoint, return its content directly. No forward crawl. |
| **0** | Resolve content at the requested outpoint. Also resolve origin to populate metadata headers. |
| **-1** | Latest. Full forward crawl to the tip of the transfer chain. |
| **N** (positive int) | Resolve to a specific absolute sequence number in the transfer chain. |

The seq is appended to the path with a colon: `/content/{txid_vout}:{seq}`.

### Sequence vs Content Revision

These are tracked separately in Redis sorted sets keyed by origin:

- **`seq:{origin}`** -- Every spend in the transfer chain, regardless of whether the output has content. This is the complete ownership history.
- **`rev:{origin}`** -- Only entries where the output contains content (inscription or B protocol). This tracks content revisions.
- **`map:{origin}`** -- Only entries where the output has MAP data.

When you request seq=5, OrdFS looks up `rev:{origin}` for the most recent content entry at or before seq 5. This means a transfer (ownership change without reinscription) does not change the content -- you still get the last reinscribed content.

### MAP Metadata Merging

MAP data (`1PuQa7K62MiKCtssSLKy1kh56WWU7MtUR5`) is merged chronologically across the transfer chain. All MAP entries from `map:{origin}` up to the requested sequence are loaded and merged, with later values overriding earlier ones.

Nested JSON fields `subTypeData` and `royalties` are parsed from their string representation into structured objects in the merged result.

### Directories

An inscription with content type `ord-fs/json` is a directory. Its body is a JSON object mapping filenames to outpoint pointers:

```json
{
  "index.html": "abc123_0",
  "style.css": "def456_0",
  "app.js": "789abc_0"
}
```

Directory behavior:
- Empty path default: serve map key `"."` in place if present; else redirect to `index.html` if present
- Path traversal resolves filenames against the directory mapping
- SPA fallback: if the requested file isn't found, `index.html` is served instead (not `"."`)
- Pass `?raw` to get the raw directory JSON instead of following the default

### BRC-150 provenance

`GET /ordfs/brc150/{txid_vout}` returns **Outpoint BEEF (BRC-158)** for a 1-sat tip:

1. Resolve ordinal path tip→origin (origin store / crawl)
2. Merge each path hop from beef storage
3. For every hop, merge source txs for inputs **0..carrier** only (carrier = spend of path parent, or origin funding input) so a verifier can re-run 1Sat assignment without pulling post-carrier noise inputs
4. Serialize `0x16a7beef || tip_outpoint(36) || BEEF`

`X-Origin` names the resolved origin. Body is raw bytes (`application/octet-stream`), not base64.

### Streaming

Large files can be split across multiple inscriptions in a transfer chain. The first inscription has its actual content type with `stream=ordfs` appended as a parameter. Subsequent chunks use the content type `ordfs/stream`.

OrdFS detects this pattern and concatenates chunks by following the spend chain. HTTP Range requests are supported for partial content retrieval.

### DNS Routing

A domain can point to an inscription by adding a TXT record:

```
_ordfs.yourdomain.com  TXT  "txid_vout"
```

Requests to that domain are resolved to the referenced inscription.

### Recursive Inscriptions

HTML inscriptions can reference other inscriptions using relative paths. For example, an HTML file can load CSS, JavaScript, fonts, or data from other outpoints via paths like `/content/{txid_vout}`.

## Configuration

```yaml
# Default: Badger origin store on local disk under {data_dir}/ordfs
ordfs:
  enabled: true
  cache:
    lru_size: 10000
    redis_url: "redis://localhost:6379/0"
    redis_ttl: "24h"
  routes:
    enabled: true
    prefix: "/ordfs"
```

```yaml
# Stateless deployments: Redis origin store, no local volume
ordfs:
  enabled: true
  origin_store_provider: "redis"
  origin_store_redis_url: "redis://localhost:6379/1"
```

| Field | Default | Description |
|-------|---------|-------------|
| `enabled` | `true` | Enable the OrdFS service |
| `origin_store_provider` | `badger` | Origin store backend: `badger` or `redis`. |
| `origin_store_path` | `{data_dir}/ordfs` | Badger data directory for the origin store (`badger` provider only). |
| `origin_store_redis_url` | — | Redis URL for the origin store; required when the provider is `redis`, with no fallback to badger. |
| `cache.lru_size` | `10000` | Max entries in the in-process parsed/merged cache |
| `cache.redis_url` | — | Optional Redis tier behind the LRU cache |
| `cache.redis_ttl` | — | TTL for Redis cache entries (e.g. `24h`); empty means no expiration |
| `routes.enabled` | `true` | Enable HTTP route registration |
| `routes.prefix` | `/ordfs` | Mount prefix for metadata/preview/stream routes |

Badger is the default and keeps the origin index on local disk. Redis holds the
same index in a shared server instead, which is what stateless deployments and
horizontally scaled replicas need since they have no durable local volume.

The Redis origin store writes keys with no TTL and requires a Redis configured
as a durable store: persistence on, eviction off (`maxmemory-policy noeviction`).
Do not point it at an instance tuned as a cache — evicted origin keys force full
chain re-crawls. Co-hosting with the cache tier is safe (key namespaces do not
collide) only when that instance meets the durability requirements.

OrdFS depends on `beef` (transaction storage) and `spends` (spend tracking) being available.

## Examples

### Get raw inscription content

No sequence resolution -- returns exactly what's at the outpoint:

```bash
curl https://api.1sat.app/content/{txid}_{vout}
```

### Get latest content

Forward crawl to the tip of the chain:

```bash
curl https://api.1sat.app/content/{txid}_{vout}:-1
```

### Get origin content

Backward crawl to find the origin, return its content:

```bash
curl https://api.1sat.app/content/{txid}_{vout}:-2
```

### Get content at a specific sequence

```bash
curl https://api.1sat.app/content/{txid}_{vout}:5
```

### Get content with MAP metadata

```bash
curl https://api.1sat.app/content/{txid}_{vout}:0?map=true
```

The `X-Map` response header contains the merged MAP JSON.

### Get metadata without content

```bash
curl https://api.1sat.app/1sat/ordfs/metadata/{txid}_{vout}:0
```

Returns JSON:

```json
{
  "contentType": "image/png",
  "contentLength": 48210,
  "sequence": 3,
  "outpoint": "abc123_0",
  "origin": "def456_0",
  "map": { "app": "myapp", "type": "image" }
}
```

### Bulk metadata lookup

```bash
curl -X POST https://api.1sat.app/1sat/ordfs/metadata \
  -H "Content-Type: application/json" \
  -d '{"outpoints": ["txid1_0", "txid2_0"]}'
```

Maximum 100 outpoints per request.

### Access a file in a directory inscription

```bash
curl https://api.1sat.app/content/{txid}_{vout}/style.css
```

### Stream content with Range header

```bash
curl -H "Range: bytes=0-1023" https://api.1sat.app/1sat/ordfs/stream/{txid}_{vout}
```

### Response headers

All content responses include:

| Header | Description |
|--------|-------------|
| `X-Outpoint` | Resolved outpoint |
| `X-Origin` | Origin outpoint (when seq is used) |
| `X-Ord-Seq` | Resolved sequence number |
| `X-Map` | Merged MAP JSON (when `?map=true`) |
| `X-Parent` | Parent outpoint (when `?parent=true`) |

### Caching

- **Specific sequence** (seq >= 0, seq == -2): `Cache-Control: public, max-age=31536000, immutable`
- **Latest** (seq == -1): `Cache-Control: no-store`

## Image transforms

Inscriptions are stored at their original size, commonly several megabytes for a
single image, which makes a grid of them expensive to render. `/ordfs/image`
returns a transformed copy (under the ordfs API prefix, not at app root — root
`/content` stays reserved for the ordfs content protocol).

**Concrete outpoint only.** This endpoint does not accept `:seq` or directory
paths. Resolve ordinality via metadata or content first, then pass the outpoint
that holds the inscription bytes.

```bash
curl "https://api.1sat.app/1sat/ordfs/image/{txid}_{vout}?w=384"
curl "https://api.1sat.app/1sat/ordfs/image/{txid}_{vout}?w=256&h=256&fit=fill&g=north"
```

| Param | Default | Behavior |
|-------|---------|----------|
| `w`   | 384     | Target width. Snaps **up** to the nearest supported width. |
| `h`   | —       | Target height. Snaps up the same way. |
| `fit` | `limit` | How the source maps onto the box. See below. |
| `g`   | `center`| Gravity for `fill` and `pad`. |
| `f`   | `auto`  | `auto`, `jpeg`, `png`, `webp`, `avif`. |
| `q`   | 75      | Quality 1-100. Rounded to the nearest 5. |

### Fit modes

The vocabulary follows Cloudinary's, which is the most widely understood in this
space. The endpoint is named for the resource, not for one use case — resizing a
hero image and cropping an avatar are the same operation with different modes.

| Mode | Behavior |
|------|----------|
| `limit` | Fit inside the box, preserve aspect ratio, **never upscale**. The default. |
| `fit` | Fit inside the box, preserve aspect ratio, upscale if the source is smaller. |
| `fill` | Cover the box exactly, cropping the overflow at the gravity. |
| `pad` | Fit inside the box and pad the remainder out to the exact size. |
| `scale` | Stretch to the exact box, ignoring aspect ratio. |

`fill`, `pad`, and `scale` need both `w` and `h` to mean anything; given only
one, they degrade to `limit` rather than producing a surprising crop.

Supported dimensions: 16, 32, 48, 64, 96, 128, 192, 256, 384, 512, 640, 828,
1080, 1200, 1920. Snapping bounds the CDN cache key space regardless of what
clients request.

### Format negotiation

`f=auto` picks the smallest encoding the client accepts, preferring AVIF, then
WebP, then PNG for transparent sources and JPEG otherwise. Negotiated responses
carry `Vary: Accept`. Measured on a 2,596,285 byte PNG inscription at `w=384`:

| Format | Bytes | % of source | Encode |
|--------|-------|-------------|--------|
| avif | 11,078 | 0.43% | 100ms |
| webp | 16,972 | 0.65% | 62ms |
| jpeg | 20,523 | 0.79% | 65ms |
| png | 250,016 | 9.63% | 410ms |

WebP and AVIF encode through WebAssembly, so no cgo is required. The runtimes
cost roughly a second to compile on first use; `WarmImageEncoders` does that at
startup so no request absorbs it.

### Caching

Derived bodies are **not** stored in the ordfs `parsed:`/`merged:` cache pool —
that pool is for small structural metadata. Every successful response is
content-addressed (concrete outpoint) and sent with:

- `Cache-Control: public, max-age=31536000, immutable`
- `Vary: Accept` when `f=auto` negotiated the format

### Behavior notes

- Path is a concrete outpoint (or bare txid). `:seq` and directory paths return
  **400** — resolve via metadata/content first.
- `image/jpeg`, `image/png`, `image/gif`, and `image/webp` are transformed.
- `image/svg+xml` is passed through unchanged (already scales; no rasterize).
- Anything else returns **415**.
- Type is checked before content bytes are loaded when the parse cache can
  answer, so non-images are rejected without pulling a multi-megabyte payload.

## See Also

- **Swagger**: Full endpoint specs at `/swagger/index.html`
- **`pkg/beef/`**: Transaction storage backing OrdFS content loading
- **`pkg/spends/`**: Spend tracking for forward crawl resolution

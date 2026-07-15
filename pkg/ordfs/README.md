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
ordfs:
  enabled: true
  redis:
    url: "redis://localhost:6379/0"
  routes:
    enabled: true
    prefix: "/ordfs"
```

| Field | Default | Description |
|-------|---------|-------------|
| `enabled` | `false` | Enable the OrdFS service |
| `redis.url` | `redis://localhost:6379/0` | Redis URL for ordinal chain caching |
| `routes.enabled` | `true` | Enable HTTP route registration |
| `routes.prefix` | `/ordfs` | Mount prefix for metadata/preview/stream routes |

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
- **Latest** (seq == -1): `Cache-Control: no-cache, no-store, must-revalidate`

## See Also

- **Swagger**: Full endpoint specs at `/swagger/index.html`
- **`pkg/beef/`**: Transaction storage backing OrdFS content loading
- **`pkg/spends/`**: Spend tracking for forward crawl resolution

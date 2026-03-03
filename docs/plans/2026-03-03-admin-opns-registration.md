# Admin UI: Yours Wallet Auth + OpNS Name Registration

## Context

The admin UI (`admin/ui/index.html`) is a vanilla JS SPA served via Go `embed.FS` at `/1sat/admin/`. It currently uses bearer token auth and has no wallet integration. The goal is to:

1. Connect the Yours Wallet browser extension for identity
2. Use that identity for admin auth (replacing/supplementing bearer token)
3. Allow registering (mining) an OpNS name through the admin UI

OpNS mining is proof-of-work — no private key signs the contract input. The contract uses `SIGHASH_ALL|ANYONECANPAY`, meaning the mine input commits to all outputs but additional funding inputs can be added. The name inscription (output 2) goes to whatever `ownerScript` is provided. Identity binding (MAP `opns.idKey`) happens as a separate inscription transfer after mining.

**Key constraint**: The OpNS unlocking script contains a sighash preimage that commits to ALL outputs. This means all outputs (including change) must be determined before building the unlocking script, then funding inputs are added after.

---

## Architecture Decisions

- **Auth model**: Yours extension provides identity key → sent as `X-Identity-Key` header → server checks against `admin.authorized_keys[]` config list. Not cryptographically signed for MVP; adequate for admin on localhost/LAN.
- **Funding**: Wallet service must be enabled (`wallet.mode: embedded`, `server_private_key` set). Server builds the mine transaction and uses its own wallet for funding inputs. The name inscription's owner script is a P2PKH derived from the user's identity key.
- **Transaction building**: Server-side. Uses go-sdk `transaction` package directly. OpNS unlocking via `go-templates/template/opns`. Manual UTXO selection from wallet.
- **PoW**: Server-side brute force (22 bits difficulty). Runs in a goroutine per registration request.

---

## Agent Orchestration

Two agents run in parallel on independent file sets:

| Agent | Scope | Files Modified |
|-------|-------|----------------|
| **Agent 1: Backend** | Go: auth middleware, OpNS registration API, config wiring | `admin/config.go`, `admin/routes.go`, `admin/opns_routes.go` (new), `cmd/server/config.go`, `config.yaml` |
| **Agent 2: Frontend** | HTML/JS: wallet connect, auth headers, registration UI | `admin/ui/index.html` |

**Shared API contract** (defined below) — both agents implement against it.

---

## API Contract

### Auth Header
All admin API calls include:
```
X-Identity-Key: <hex DER compressed public key, 66 chars>
```
Server validates this key exists in `admin.authorized_keys[]` config.
If no `authorized_keys` are configured, identity key auth is skipped (backwards compatible with bearer-only auth).

### Endpoints

**`GET /1sat/admin/opns/mine/:name`**
Proxy to existing `/1sat/opns/mine/:name`. Returns the mine outpoint for a domain.
```json
{"outpoint": {"txid": "abc...", "index": 0}, "domain": "hel"}
```
Returns 404 if domain is fully taken or no mine tree exists for the prefix.

**`POST /1sat/admin/opns/register`**
Starts an OpNS name registration (mine + broadcast).
```json
Request:  {"name": "hello", "identityKey": "03abc..."}
Response: {"txid": "abc...", "name": "hello", "outpoint": "abc..._2"}
```
Server-side flow:
1. Query mine outpoint for the name (via lookup service)
2. Get BEEF for the mine UTXO
3. Determine character to mine (first char of name after the prefix)
4. PoW: iterate nonces until `sha256d(pow + char + nonce)` has 22 leading zero bits
5. Build child contract locking scripts via `opns.Lock()`
6. Build name inscription via `inscription.Lock()` with `application/op-ns` content
7. Owner script = P2PKH from identity key
8. Estimate fees, determine change output
9. Build complete transaction with all outputs
10. Apply `OpnsUnlocker.Sign()` for mine input (needs all outputs known)
11. Add funding input(s) from server wallet, sign with server key
12. Broadcast via Arcade
13. Submit to overlay engine
14. Return txid + name outpoint

---

## Task 1: Backend — Admin Auth + OpNS Registration API

**Agent 1** — modifies Go files only.

### 1A: Add `authorized_keys` to admin config

**File: `admin/config.go`**
- Add `AuthorizedKeys []string` to `RoutesConfig`
- Map from `admin.routes.authorized_keys` in config

### 1B: Add identity key auth middleware

**File: `admin/routes.go`**
- New `identityKeyMiddleware()` that reads `X-Identity-Key` header
- Checks if key is in `config.AuthorizedKeys`
- If `AuthorizedKeys` is empty, skip check (no restriction)
- Apply alongside existing bearer token middleware (either can authorize)

### 1C: Create OpNS registration handler

**New file: `admin/opns_routes.go`**

Dependencies needed in `Routes` struct:
- `opnsLookup *opns.LookupService` — for mine outpoint query
- `beefStorage *beef.Storage` — for building BEEF
- `arcade` — for broadcasting
- `overlaySvc` — for submitting to overlay engine

Handler: `handleOpnsRegister(c *fiber.Ctx) error`
1. Parse request body `{name, identityKey}`
2. Call `opnsLookup.Mine(ctx, name)` to get parent mine outpoint + domain prefix
3. Derive the character: `name[len(domain)]` (next char after prefix)
4. Load parent mine UTXO script via BEEF storage: `beefStorage.BuildFullBeef(ctx, &outpoint.Txid)`
5. Parse parent OpNS contract: `opns.Decode(parentScript)`
6. PoW loop: iterate random nonces, test with `parent.TestSolution(char, nonce)`
7. Build child contract scripts: `opns.Lock(newClaimed, name[:len(domain)+1], newPow)`
8. Build name inscription: `inscription.Lock()` with content=name, type=`application/op-ns`, P2PKH prefix for identity key
9. Build the full transaction, apply `OpnsUnlocker.Sign()`, add funding, broadcast, submit to overlay
10. Return result

### 1D: Wire dependencies in config

**File: `cmd/server/config.go`**
- Pass `opns.LookupService`, `beef.Storage`, Arcade, and Overlay to admin `NewRoutes`
- Update `admin.InitializeDeps` struct

### 1E: Update config.yaml

Add example authorized_keys:
```yaml
admin:
  routes:
    authorized_keys: []  # hex identity public keys allowed to access admin
```

### Key files to reference:
- `go-templates/template/opns/opns.go` — `Lock()`, `Decode()`, `TestSolution()`, `OpnsUnlocker.Sign()`
- `go-templates/template/inscription/inscription.go` — `Inscription.Lock()`
- `pkg/opns/lookup.go:242` — `Mine()` method
- `pkg/opns/crawl.go` — reference for BEEF building + overlay submit pattern
- `pkg/beef/storage.go` — `BuildFullBeef()`
- `admin/routes.go` — existing route registration + auth middleware pattern

---

## Task 2: Frontend — Yours Wallet Connect + OpNS Registration UI

**Agent 2** — modifies `admin/ui/index.html` only.

### 2A: Yours Wallet connection

Add wallet connection to the header area:
- "Connect Wallet" / "Disconnect" button in the nav area
- Display truncated identity key when connected
- Persist connection state in `sessionStorage`

**Yours extension communication pattern** (from `1sat-wallet-toolbox/src/cwi/event.ts`):
```javascript
const YOURS_REQUEST = "YoursRequest";

function walletRequest(action, params) {
    return new Promise((resolve, reject) => {
        const messageId = `${action}-${Date.now()}-${Math.random()}`;
        const handler = (event) => {
            if (event.detail?.error) reject(new Error(event.detail.error));
            else resolve(event.detail?.result);
        };
        self.addEventListener(messageId, handler, { once: true });
        self.dispatchEvent(new CustomEvent(YOURS_REQUEST, {
            detail: { messageId, type: action, params }
        }));
        setTimeout(() => { self.removeEventListener(messageId, handler); reject(new Error('Wallet timeout')); }, 30000);
    });
}

// Key actions:
// walletRequest('cwi_isAuthenticated', {}) → boolean
// walletRequest('cwi_getPublicKey', { identityKey: true }) → { publicKey: string }
```

### 2B: Auth header injection

Wrap `fetch` calls to include identity key:
```javascript
async function adminFetch(path, options = {}) {
    if (identityKey) {
        options.headers = { ...options.headers, 'X-Identity-Key': identityKey };
    }
    return fetch(`${API_BASE}${path}`, options);
}
```

Update all existing `fetch()` calls to use `adminFetch()`.

### 2C: OpNS registration card

Add a new card to the grid:
```
┌─────────────────────────────────────────┐
│ OpNS Name Registration          [badge] │
│ Register a domain name on the BSV       │
│ blockchain using proof-of-work mining   │
│                                         │
│ [name input____________] [Register]     │
│                                         │
│ Status: Mining... (PoW in progress)     │
│ Result: hello → txid_2                  │
└─────────────────────────────────────────┘
```

- Input field for desired name
- Register button (disabled unless wallet connected)
- Status area showing: idle / checking availability / mining / broadcasting / complete / error
- On submit: `POST /admin/opns/register` with `{name, identityKey}`
- Show result with txid link

### 2D: Mine availability check

Before registration, check name availability:
- `GET /admin/opns/mine/{name}`
- If 200 → available (show "Available" badge + enable Register)
- If 404 → taken or no mine tree (show "Not available")
- Trigger on input blur or debounced typing

---

## Verification

1. `go build ./...` passes
2. Admin UI loads at `http://localhost:8080/1sat/admin/`
3. "Connect Wallet" button detects Yours extension
4. After connecting, identity key is displayed in header
5. OpNS name availability check works via mine endpoint
6. Registration endpoint completes PoW, builds transaction, broadcasts
7. Bearer token auth still works alongside identity key auth
8. No changes to existing admin functionality (whitelist, blacklist, etc.)

## End-to-end test sequence:
1. Enable wallet in config (`wallet.mode: embedded`, set `server_private_key`)
2. Add your identity key to `admin.routes.authorized_keys`
3. Fund the server wallet with some BSV
4. Open admin UI, connect Yours Wallet
5. Enter a name, verify availability
6. Click Register, wait for PoW + broadcast
7. Verify name appears in OpNS overlay (`GET /1sat/opns/mine/{name}` returns nil = taken)

# Sweep UI Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Standalone sweep UI served at `/1sat/sweep/` that lets users connect a BRC-100 wallet, enter legacy WIF(s), scan for assets via 1sat-stack endpoints, and sweep them into the connected wallet.

**Architecture:** New `sweep/` package in 1sat-stack, parallel to `admin/`. Vite+React SPA embedded via `go:embed`, served by the Go server with no auth required. Client-side wallet connection via `@1sat/connect`'s `connectWallet()`. Scanning uses OwnerClient SSE + TxoClient search from `@1sat/client`. Sweeping uses `@1sat/actions` sweep functions.

**Tech Stack:** Go (embed + Fiber), Vite, React 19, TypeScript, Tailwind v4, `@1sat/connect`, `@1sat/client`, `@1sat/actions`, `@bsv/sdk`, shadcn/ui components (reuse from admin UI)

---

## File Structure

### Go side (`sweep/`)
- `sweep/config.go` — Config, SetDefaults, Initialize (mirrors admin/config.go pattern)
- `sweep/routes.go` — `go:embed ui/dist/*`, SPA serving (no API routes needed)

### UI side (`sweep/ui/`)
- `sweep/ui/package.json` — Dependencies
- `sweep/ui/tsconfig.json` — TypeScript config
- `sweep/ui/vite.config.ts` — Vite config with `base: "./"` and `@` alias
- `sweep/ui/index.html` — HTML entrypoint
- `sweep/ui/src/main.tsx` — React root
- `sweep/ui/src/styles.css` — Tailwind import
- `sweep/ui/src/App.tsx` — Main app: wallet connection + sweep wizard
- `sweep/ui/src/lib/wallet.ts` — Wallet connection state (connectWallet, disconnect, getWallet)
- `sweep/ui/src/lib/services.ts` — OneSatServices singleton (same-origin)
- `sweep/ui/src/lib/scanner.ts` — Address scanning: derive address from WIF, SSE owner sync, txo/search queries, categorize results into funding/ordinals/bsv21/bsv20
- `sweep/ui/src/lib/sweeper.ts` — Execute sweeps: prepareSweepInputs + sweepBsv/sweepOrdinals/sweepBsv21
- `sweep/ui/src/lib/utils.ts` — cn() helper, formatSats, formatTokenAmount
- `sweep/ui/src/components/connect-wallet.tsx` — Wallet connection card
- `sweep/ui/src/components/wif-input.tsx` — WIF input form (1 or 2 keys)
- `sweep/ui/src/components/scan-progress.tsx` — SSE sync progress display
- `sweep/ui/src/components/asset-preview.tsx` — Asset sections: funding, ordinals (with selection), BSV-21 tokens, BSV-20 (info only)
- `sweep/ui/src/components/sweep-progress.tsx` — Sweep execution progress + results
- `sweep/ui/src/components/ui/` — Copied from admin UI: button, badge, input, card, progress (shadcn)

### Build integration
- Modify: `build.sh` — Add sweep UI build step
- Modify: `cmd/server/config.go` — Wire sweep routes

---

## Chunk 1: Go scaffold + empty SPA

### Task 1: Go package — config.go

**Files:**
- Create: `sweep/config.go`

- [ ] **Step 1: Create sweep config**

```go
package sweep

import (
	"context"
	"log/slog"

	"github.com/spf13/viper"
)

const (
	ModeDisabled = "disabled"
	ModeEnabled  = "enabled"
)

type Config struct {
	Mode   string       `mapstructure:"mode"`
	Routes RoutesConfig `mapstructure:"routes"`
}

type RoutesConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Prefix  string `mapstructure:"prefix"`
}

type Services struct {
	Routes *Routes
}

func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	v.SetDefault(prefix+".mode", ModeEnabled)
	v.SetDefault(prefix+".routes.enabled", true)
	v.SetDefault(prefix+".routes.prefix", "/sweep")
}

func (c *Config) Initialize(ctx context.Context, logger *slog.Logger) (*Services, error) {
	if c.Mode == ModeDisabled {
		return nil, nil
	}
	if logger == nil {
		logger = slog.Default()
	}

	svc := &Services{}
	if c.Routes.Enabled {
		svc.Routes = NewRoutes(&c.Routes, logger)
	}

	logger.Info("sweep service initialized", "mode", c.Mode)
	return svc, nil
}

func (svc *Services) Close() error {
	return nil
}
```

- [ ] **Step 2: Commit**
```bash
git add sweep/config.go
git commit -m "feat(sweep): add config package for sweep UI"
```

### Task 2: Go package — routes.go

**Files:**
- Create: `sweep/routes.go`

- [ ] **Step 1: Create routes with go:embed**

```go
package sweep

import (
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/filesystem"
)

//go:embed ui/dist/*
var uiFS embed.FS

type Routes struct {
	config *RoutesConfig
	logger *slog.Logger
}

func NewRoutes(cfg *RoutesConfig, logger *slog.Logger) *Routes {
	return &Routes{config: cfg, logger: logger}
}

func (r *Routes) Register(group fiber.Router) {
	uiSubFS, err := fs.Sub(uiFS, "ui/dist")
	if err != nil {
		r.logger.Error("failed to create sweep ui sub filesystem", "error", err)
		return
	}

	group.Get("/", func(c *fiber.Ctx) error {
		if !strings.HasSuffix(c.OriginalURL(), "/") {
			return c.Redirect(c.OriginalURL()+"/", fiber.StatusMovedPermanently)
		}
		content, err := fs.ReadFile(uiSubFS, "index.html")
		if err != nil {
			return c.Status(fiber.StatusNotFound).SendString("Not found")
		}
		c.Set("Content-Type", "text/html")
		return c.Send(content)
	})

	group.Use("/", filesystem.New(filesystem.Config{
		Root:   http.FS(uiSubFS),
		Browse: false,
	}))

	group.Get("/*", func(c *fiber.Ctx) error {
		content, err := fs.ReadFile(uiSubFS, "index.html")
		if err != nil {
			return c.Status(fiber.StatusNotFound).SendString("Not found")
		}
		c.Set("Content-Type", "text/html")
		return c.Send(content)
	})

	r.logger.Debug("registered sweep routes")
}
```

- [ ] **Step 2: Commit**
```bash
git add sweep/routes.go
git commit -m "feat(sweep): add routes with embedded SPA serving"
```

### Task 3: Wire into server config

**Files:**
- Modify: `cmd/server/config.go` — Add Sweep field to config struct, SetDefaults, Initialize, and route registration

- [ ] **Step 1: Add Sweep to the Config struct**

Find the `Config` struct that has `Admin admin.Config` and add `Sweep sweep.Config` alongside it. Add the import for `"github.com/b-open-io/1sat-stack/sweep"`.

- [ ] **Step 2: Add SetDefaults call**

Find where `c.Admin.SetDefaults(v, prefix+".admin")` is called and add `c.Sweep.SetDefaults(v, prefix+".sweep")` next to it.

- [ ] **Step 3: Add Initialize call**

Find where admin is initialized (`c.Admin.Initialize(...)`) and add sweep initialization nearby:
```go
if c.Sweep.Mode != sweep.ModeDisabled {
	sweepSvc, err := c.Sweep.Initialize(ctx, logger)
	if err != nil {
		return nil, fmt.Errorf("sweep: %w", err)
	}
	svc.Sweep = sweepSvc
}
```

Add `Sweep *sweep.Services` to the Services struct.

- [ ] **Step 4: Add route registration**

Find the admin route registration block (~line 1114) and add sweep registration after it:
```go
if svc.Sweep != nil && svc.Sweep.Routes != nil {
	prefix := c.Sweep.Routes.Prefix
	if prefix == "" {
		prefix = "/sweep"
	}
	sweepGroup := api.Group(prefix)
	svc.Sweep.Routes.Register(sweepGroup)
	capabilities = append(capabilities, "sweep")
	slog.Debug("registered sweep routes", "prefix", prefix)
}
```

- [ ] **Step 5: Commit**
```bash
git add cmd/server/config.go
git commit -m "feat(sweep): wire sweep service into server config"
```

### Task 4: Scaffold empty Vite SPA

**Files:**
- Create: `sweep/ui/package.json`
- Create: `sweep/ui/tsconfig.json`
- Create: `sweep/ui/vite.config.ts`
- Create: `sweep/ui/index.html`
- Create: `sweep/ui/src/main.tsx`
- Create: `sweep/ui/src/styles.css`
- Create: `sweep/ui/src/App.tsx`
- Create: `sweep/ui/src/vite-env.d.ts`

- [ ] **Step 1: Create package.json**

```json
{
  "name": "1sat-sweep-ui",
  "private": true,
  "version": "0.0.1",
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "tsc -b && vite build",
    "preview": "vite preview"
  },
  "dependencies": {
    "@1sat/actions": "0.0.51",
    "@1sat/client": "^0.0.9",
    "@1sat/connect": "^0.0.9",
    "@1sat/types": "^0.0.9",
    "@bsv/sdk": "^2.0.4",
    "class-variance-authority": "^0.7.1",
    "clsx": "^2.1.1",
    "lucide-react": "^0.577.0",
    "react": "^19.0.0",
    "react-dom": "^19.0.0",
    "sonner": "^2.0.7",
    "tailwind-merge": "^3.5.0"
  },
  "devDependencies": {
    "@tailwindcss/vite": "^4.2.1",
    "@types/react": "^19.0.0",
    "@types/react-dom": "^19.0.0",
    "@vitejs/plugin-react": "^4.0.0",
    "tailwindcss": "^4.2.1",
    "typescript": "~5.7.0",
    "vite": "^6.0.0"
  }
}
```

- [ ] **Step 2: Create tsconfig.json**

```json
{
  "compilerOptions": {
    "target": "ES2020",
    "useDefineForClassFields": true,
    "lib": ["ES2020", "DOM", "DOM.Iterable"],
    "module": "ESNext",
    "skipLibCheck": true,
    "moduleResolution": "bundler",
    "allowImportingTsExtensions": true,
    "isolatedModules": true,
    "moduleDetection": "force",
    "noEmit": true,
    "jsx": "react-jsx",
    "strict": true,
    "noUnusedLocals": true,
    "noUnusedParameters": true,
    "noFallthroughCasesInSwitch": true,
    "paths": { "@/*": ["./src/*"] }
  },
  "include": ["src"]
}
```

- [ ] **Step 3: Create vite.config.ts**

```ts
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import path from "path";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  base: "./",
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
});
```

- [ ] **Step 4: Create index.html**

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>1Sat Sweep</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
```

- [ ] **Step 5: Create src/vite-env.d.ts**

```ts
/// <reference types="vite/client" />
```

- [ ] **Step 6: Create src/styles.css**

```css
@import "tailwindcss";
```

- [ ] **Step 7: Create src/main.tsx**

```tsx
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import "./styles.css";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
```

- [ ] **Step 8: Create src/App.tsx** (placeholder)

```tsx
export default function App() {
  return (
    <div className="min-h-screen bg-background text-foreground flex items-center justify-center">
      <h1 className="text-2xl font-bold">1Sat Sweep</h1>
    </div>
  );
}
```

- [ ] **Step 9: Install deps and build**

```bash
cd sweep/ui && bun install && bun run build
```

- [ ] **Step 10: Commit**
```bash
git add sweep/ui/
git commit -m "feat(sweep): scaffold Vite+React SPA"
```

### Task 5: Update build.sh

**Files:**
- Modify: `build.sh`

- [ ] **Step 1: Add sweep UI build**

Add before the "Building server..." line:
```bash
echo "Building sweep UI..."
(cd sweep/ui && bun install && bun run build)
```

- [ ] **Step 2: Commit**
```bash
git add build.sh
git commit -m "feat(sweep): add sweep UI to build.sh"
```

---

## Chunk 2: UI foundation — utilities, wallet, services

### Task 6: Copy shadcn UI components from admin

**Files:**
- Create: `sweep/ui/src/components/ui/button.tsx`
- Create: `sweep/ui/src/components/ui/badge.tsx`
- Create: `sweep/ui/src/components/ui/input.tsx`
- Create: `sweep/ui/src/components/ui/card.tsx`
- Create: `sweep/ui/src/components/ui/progress.tsx`

- [ ] **Step 1: Copy UI primitives from admin**

Copy these files verbatim from `admin/ui/src/components/ui/`:
- `button.tsx`
- `badge.tsx`
- `input.tsx`
- `card.tsx`

If `progress.tsx` doesn't exist in admin, create a basic one using the shadcn/ui Progress pattern.

- [ ] **Step 2: Commit**
```bash
git add sweep/ui/src/components/ui/
git commit -m "feat(sweep): add shadcn UI components"
```

### Task 7: Utility helpers

**Files:**
- Create: `sweep/ui/src/lib/utils.ts`

- [ ] **Step 1: Create utils**

```ts
import { type ClassValue, clsx } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

export function formatSats(sats: number): string {
  return sats.toLocaleString();
}

export function formatTokenAmount(rawAmount: string, decimals: number): string {
  if (decimals === 0) return rawAmount;
  const padded = rawAmount.padStart(decimals + 1, "0");
  const intPart = padded.slice(0, -decimals) || "0";
  const decPart = padded.slice(-decimals).replace(/0+$/, "");
  return decPart ? `${intPart}.${decPart}` : intPart;
}

export function truncate(s: string, len = 8): string {
  if (s.length <= len * 2 + 3) return s;
  return `${s.slice(0, len)}...${s.slice(-len)}`;
}
```

- [ ] **Step 2: Commit**
```bash
git add sweep/ui/src/lib/utils.ts
git commit -m "feat(sweep): add utility helpers"
```

### Task 8: Wallet connection module

**Files:**
- Create: `sweep/ui/src/lib/wallet.ts`

- [ ] **Step 1: Create wallet module**

Uses `connectWallet` from `@1sat/connect`. Simple module state — no React context needed for this small app.

```ts
import { connectWallet as connect, type ConnectWalletResult } from "@1sat/connect";
import type { WalletInterface } from "@bsv/sdk";

let connection: ConnectWalletResult | null = null;

export async function connectWallet(): Promise<ConnectWalletResult> {
  const result = await connect();
  if (!result) throw new Error("No wallet available");
  connection = result;
  return result;
}

export function getWallet(): WalletInterface | null {
  return connection?.wallet ?? null;
}

export function getIdentityKey(): string | null {
  return connection?.identityKey ?? null;
}

export function getProvider(): string | null {
  return connection?.provider ?? null;
}

export function disconnectWallet(): void {
  connection?.disconnect();
  connection = null;
}

export function isConnected(): boolean {
  return connection !== null;
}
```

- [ ] **Step 2: Commit**
```bash
git add sweep/ui/src/lib/wallet.ts
git commit -m "feat(sweep): add wallet connection module"
```

### Task 9: Services module

**Files:**
- Create: `sweep/ui/src/lib/services.ts`

- [ ] **Step 1: Create services singleton**

```ts
import { OneSatServices } from "@1sat/client";

let _services: OneSatServices | null = null;

export function getServices(): OneSatServices {
  if (!_services) {
    // Derive base URL from current page location
    // Sweep UI is at /1sat/sweep/, stack API is at /1sat/
    const sweepIdx = window.location.pathname.indexOf("/sweep");
    const basePath = sweepIdx >= 0
      ? window.location.pathname.substring(0, sweepIdx)
      : "";
    _services = new OneSatServices("main", `${window.location.origin}${basePath}`);
  }
  return _services;
}
```

- [ ] **Step 2: Commit**
```bash
git add sweep/ui/src/lib/services.ts
git commit -m "feat(sweep): add OneSatServices singleton"
```

---

## Chunk 3: Scanner + Sweeper logic

### Task 10: Address scanner

**Files:**
- Create: `sweep/ui/src/lib/scanner.ts`

- [ ] **Step 1: Create scanner module**

This module handles: WIF → address derivation, owner sync via SSE, txo/search for asset categorization, and metadata enrichment via ORDFS.

```ts
import { PrivateKey } from "@bsv/sdk";
import type { IndexedOutput } from "@1sat/types";
import { getServices } from "./services";

export interface ScannedAssets {
  funding: IndexedOutput[];
  ordinals: IndexedOutput[];
  bsv21Tokens: IndexedOutput[];
  bsv20Tokens: IndexedOutput[];
  totalBsv: number;
}

export interface ScanProgress {
  phase: string;
  detail?: string;
}

export function deriveAddress(wif: string): string {
  const pk = PrivateKey.fromWif(wif.trim());
  return pk.toPublicKey().toAddress();
}

function getServerBase(): string {
  const sweepIdx = window.location.pathname.indexOf("/sweep");
  const basePath = sweepIdx >= 0
    ? window.location.pathname.substring(0, sweepIdx)
    : "";
  return `${window.location.origin}${basePath}`;
}

/**
 * Trigger owner sync for an address via SSE, then search for categorized assets.
 */
export async function scanAddress(
  address: string,
  onProgress?: (p: ScanProgress) => void,
): Promise<ScannedAssets> {
  const base = getServerBase();

  // Phase 1: Sync address via SSE
  onProgress?.({ phase: "sync", detail: "Syncing address..." });
  await new Promise<void>((resolve, reject) => {
    const es = new EventSource(`${base}/owner/${address}/txos?refresh=true&limit=1`);
    es.onmessage = (ev) => {
      try {
        const msg = JSON.parse(ev.data);
        if (msg.phase === "done" || msg.phase === "error") {
          es.close();
          if (msg.phase === "error") reject(new Error(msg.error || "Sync failed"));
          else resolve();
        } else if (msg.phase === "fetch" || msg.phase === "ingest") {
          onProgress?.({
            phase: "sync",
            detail: `${msg.phase}: ${msg.processed ?? 0}/${msg.total ?? "?"}`,
          });
        }
      } catch {
        // ignore non-JSON
      }
    };
    es.onerror = () => {
      es.close();
      resolve();
    };
  });

  // Phase 2: Search for all unspent outputs owned by this address
  onProgress?.({ phase: "search", detail: "Searching for assets..." });

  const searchUrl = new URL(`${base}/txo/search`);
  searchUrl.searchParams.append("key", `own:${address}`);
  searchUrl.searchParams.set("unspent", "true");
  searchUrl.searchParams.set("events", "true");
  searchUrl.searchParams.set("sats", "true");

  const res = await fetch(searchUrl.toString());
  if (!res.ok) throw new Error(`Search failed: ${res.statusText}`);
  const results: IndexedOutput[] = await res.json();

  // Phase 3: Categorize by events
  onProgress?.({ phase: "categorize", detail: "Categorizing assets..." });
  return categorizeOutputs(results);
}

function categorizeOutputs(outputs: IndexedOutput[]): ScannedAssets {
  const funding: IndexedOutput[] = [];
  const ordinals: IndexedOutput[] = [];
  const bsv21Tokens: IndexedOutput[] = [];
  const bsv20Tokens: IndexedOutput[] = [];

  for (const out of outputs) {
    const events = out.events ?? [];
    const types = events
      .filter((e) => e.startsWith("type:"))
      .map((e) => e.slice(5));

    if (types.some((t) => t.includes("bsv21") || t.includes("bsv-21"))) {
      bsv21Tokens.push(out);
    } else if (types.some((t) => t.includes("bsv20") || t.includes("bsv-20"))) {
      bsv20Tokens.push(out);
    } else if (
      events.some((e) => e.startsWith("origin:")) ||
      types.some((t) => t.includes("inscription") || t.includes("ord"))
    ) {
      ordinals.push(out);
    } else {
      funding.push(out);
    }
  }

  const totalBsv = funding.reduce((sum, o) => sum + (o.satoshis ?? 0), 0);

  return { funding, ordinals, bsv21Tokens, bsv20Tokens, totalBsv };
}

/**
 * Scan multiple addresses (dedup) and merge results.
 */
export async function scanAddresses(
  addresses: string[],
  onProgress?: (p: ScanProgress) => void,
): Promise<ScannedAssets> {
  const unique = [...new Set(addresses)];
  const allResults: ScannedAssets[] = [];

  for (const addr of unique) {
    onProgress?.({ phase: "sync", detail: `Scanning ${addr.slice(0, 8)}...` });
    allResults.push(await scanAddress(addr, onProgress));
  }

  return {
    funding: allResults.flatMap((r) => r.funding),
    ordinals: allResults.flatMap((r) => r.ordinals),
    bsv21Tokens: allResults.flatMap((r) => r.bsv21Tokens),
    bsv20Tokens: allResults.flatMap((r) => r.bsv20Tokens),
    totalBsv: allResults.reduce((sum, r) => sum + r.totalBsv, 0),
  };
}
```

Note: The categorization logic here uses the `events` array from `IndexedOutput` (tags like `type:application/op-ns`, `origin:txid_vout`). The exact event format depends on how the 1sat-stack indexes outputs. This may need adjustment once we test against live data — the OPNS page uses `type:application/op-ns` for content type filtering. We should verify what type tags are present for ordinals vs BSV-21 outputs and adjust the categorization accordingly.

- [ ] **Step 2: Commit**
```bash
git add sweep/ui/src/lib/scanner.ts
git commit -m "feat(sweep): add address scanner with SSE sync"
```

### Task 11: Sweeper module

**Files:**
- Create: `sweep/ui/src/lib/sweeper.ts`

- [ ] **Step 1: Create sweeper module**

```ts
import {
  createContext,
  prepareSweepInputs,
  sweepBsv,
  sweepOrdinals,
  sweepBsv21,
  type SweepInput,
  type SweepBsv21Input,
} from "@1sat/actions";
import type { IndexedOutput } from "@1sat/types";
import type { WalletInterface } from "@bsv/sdk";
import { getServices } from "./services";

export interface SweepResult {
  bsvTxid?: string;
  ordinalTxids: string[];
  bsv21Txids: string[];
  errors: string[];
}

export async function executeSweep(params: {
  wallet: WalletInterface;
  wif: string;
  funding: IndexedOutput[];
  ordinals: IndexedOutput[];
  bsv21Tokens: IndexedOutput[];
  onProgress: (stage: string) => void;
}): Promise<SweepResult> {
  const { wallet, wif, funding, ordinals, bsv21Tokens, onProgress } = params;
  const ctx = createContext(wallet, { services: getServices(), chain: "main" });

  const result: SweepResult = {
    ordinalTxids: [],
    bsv21Txids: [],
    errors: [],
  };

  // Sweep BSV funding
  if (funding.length > 0) {
    onProgress(`Sweeping ${funding.length} BSV UTXOs...`);
    try {
      const inputs = await prepareSweepInputs(ctx, funding);
      const bsvResult = await sweepBsv.execute(ctx, { inputs, wif });
      if (bsvResult.error) result.errors.push(`BSV: ${bsvResult.error}`);
      else if (bsvResult.txid) result.bsvTxid = bsvResult.txid;
    } catch (e) {
      result.errors.push(`BSV: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  // Sweep ordinals
  if (ordinals.length > 0) {
    onProgress(`Sweeping ${ordinals.length} ordinals...`);
    try {
      const inputs = await prepareSweepInputs(ctx, ordinals);
      const ordResult = await sweepOrdinals.execute(ctx, { inputs, wif });
      if (ordResult.error) result.errors.push(`Ordinals: ${ordResult.error}`);
      else if (ordResult.txid) result.ordinalTxids.push(ordResult.txid);
    } catch (e) {
      result.errors.push(`Ordinals: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  // Sweep BSV-21 tokens (grouped by tokenId from events)
  if (bsv21Tokens.length > 0) {
    // Group by tokenId extracted from events
    const groups = new Map<string, IndexedOutput[]>();
    for (const token of bsv21Tokens) {
      const tokenEvent = token.events?.find((e) => e.startsWith("tokenId:"));
      const tokenId = tokenEvent?.slice(8) ?? "unknown";
      const group = groups.get(tokenId) ?? [];
      group.push(token);
      groups.set(tokenId, group);
    }

    for (const [tokenId, tokens] of groups) {
      onProgress(`Sweeping ${tokens.length} tokens (${tokenId.slice(0, 8)}...)...`);
      try {
        const inputs = await prepareSweepInputs(ctx, tokens);
        const tokenResult = await sweepBsv21.execute(ctx, {
          inputs: inputs.map((inp) => ({
            ...inp,
            tokenId,
            amount: "0", // Server resolves actual amount
          })),
          wif,
        });
        if (tokenResult.error) result.errors.push(`BSV-21 (${tokenId.slice(0, 8)}): ${tokenResult.error}`);
        else if (tokenResult.txid) result.bsv21Txids.push(tokenResult.txid);
      } catch (e) {
        result.errors.push(`BSV-21 (${tokenId.slice(0, 8)}): ${e instanceof Error ? e.message : String(e)}`);
      }
    }
  }

  onProgress("Sweep complete");
  return result;
}
```

Note: The BSV-21 token grouping and amount extraction depends on the exact event/tag format from 1sat-stack indexing. The `tokenId:` event prefix and amount resolution may need adjustment once tested against live indexed outputs.

- [ ] **Step 2: Commit**
```bash
git add sweep/ui/src/lib/sweeper.ts
git commit -m "feat(sweep): add sweep executor"
```

---

## Chunk 4: UI components

### Task 12: Connect wallet component

**Files:**
- Create: `sweep/ui/src/components/connect-wallet.tsx`

- [ ] **Step 1: Create wallet connection card**

```tsx
import { useState } from "react";
import { Loader2, Wallet, CheckCircle2, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { connectWallet, disconnectWallet, getIdentityKey, getProvider } from "@/lib/wallet";

interface Props {
  onConnected: () => void;
  onDisconnected: () => void;
  connected: boolean;
}

export function ConnectWallet({ onConnected, onDisconnected, connected }: Props) {
  const [connecting, setConnecting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleConnect() {
    setConnecting(true);
    setError(null);
    try {
      await connectWallet();
      onConnected();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to connect wallet");
    } finally {
      setConnecting(false);
    }
  }

  function handleDisconnect() {
    disconnectWallet();
    onDisconnected();
  }

  if (connected) {
    return (
      <Card>
        <CardContent className="flex items-center justify-between py-4">
          <div className="flex items-center gap-3">
            <CheckCircle2 className="h-5 w-5 text-green-500" />
            <div>
              <div className="text-sm font-medium">Wallet Connected</div>
              <div className="text-xs text-muted-foreground">
                {getProvider() === "brc100" ? "BRC-100" : "OneSat"} · {getIdentityKey()?.slice(0, 12)}...
              </div>
            </div>
          </div>
          <Button variant="ghost" size="sm" onClick={handleDisconnect}>
            <X className="h-4 w-4" />
          </Button>
        </CardContent>
      </Card>
    );
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Wallet className="h-5 w-5" />
          Connect Destination Wallet
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        <p className="text-sm text-muted-foreground">
          Connect your BRC-100 wallet to receive swept assets.
        </p>
        {error && <p className="text-sm text-destructive">{error}</p>}
        <Button onClick={handleConnect} disabled={connecting} className="w-full">
          {connecting ? <Loader2 className="h-4 w-4 animate-spin mr-2" /> : null}
          {connecting ? "Connecting..." : "Connect Wallet"}
        </Button>
      </CardContent>
    </Card>
  );
}
```

- [ ] **Step 2: Commit**
```bash
git add sweep/ui/src/components/connect-wallet.tsx
git commit -m "feat(sweep): add wallet connection component"
```

### Task 13: WIF input component

**Files:**
- Create: `sweep/ui/src/components/wif-input.tsx`

- [ ] **Step 1: Create WIF input form**

```tsx
import { useState } from "react";
import { KeyRound, Loader2, Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";

interface Props {
  onScan: (payWif: string, ordWif: string) => void;
  scanning: boolean;
  disabled: boolean;
}

export function WifInput({ onScan, scanning, disabled }: Props) {
  const [payWif, setPayWif] = useState("");
  const [ordWif, setOrdWif] = useState("");
  const [sameKey, setSameKey] = useState(true);

  function handleScan() {
    const pay = payWif.trim();
    const ord = sameKey ? pay : ordWif.trim();
    if (!pay) return;
    onScan(pay, ord);
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <KeyRound className="h-5 w-5" />
          Legacy Keys
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="space-y-2">
          <label className="text-sm font-medium">
            {sameKey ? "Private Key (WIF)" : "Pay Key (WIF)"}
          </label>
          <Input
            type="password"
            placeholder="Enter WIF private key..."
            value={payWif}
            onChange={(e) => setPayWif(e.target.value)}
            disabled={disabled || scanning}
          />
        </div>

        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={sameKey}
            onChange={(e) => setSameKey(e.target.checked)}
            disabled={disabled || scanning}
          />
          Same key for pay and ordinals
        </label>

        {!sameKey && (
          <div className="space-y-2">
            <label className="text-sm font-medium">Ordinals Key (WIF)</label>
            <Input
              type="password"
              placeholder="Enter ordinals WIF..."
              value={ordWif}
              onChange={(e) => setOrdWif(e.target.value)}
              disabled={disabled || scanning}
            />
          </div>
        )}

        <Button
          onClick={handleScan}
          disabled={disabled || scanning || !payWif.trim()}
          className="w-full"
        >
          {scanning ? <Loader2 className="h-4 w-4 animate-spin mr-2" /> : <Search className="h-4 w-4 mr-2" />}
          {scanning ? "Scanning..." : "Scan for Assets"}
        </Button>
      </CardContent>
    </Card>
  );
}
```

- [ ] **Step 2: Commit**
```bash
git add sweep/ui/src/components/wif-input.tsx
git commit -m "feat(sweep): add WIF input component"
```

### Task 14: Asset preview component

**Files:**
- Create: `sweep/ui/src/components/asset-preview.tsx`

- [ ] **Step 1: Create asset preview sections**

Adapted from 1sat-website's `migration-sections.tsx` but uses `IndexedOutput` instead of `WalletOrdinal`.

```tsx
import { Badge } from "@/components/ui/badge";
import { formatSats } from "@/lib/utils";
import type { IndexedOutput } from "@1sat/types";

export function FundingSection({ funding, totalBsv }: { funding: IndexedOutput[]; totalBsv: number }) {
  if (funding.length === 0) return null;
  return (
    <div className="border border-green-500/20 bg-green-500/5 p-4 rounded-lg">
      <div className="flex items-center gap-2 mb-2">
        <span className="h-2 w-2 rounded-full bg-green-500" />
        <span className="text-sm font-semibold text-green-500">BSV Funding</span>
      </div>
      <div className="flex items-baseline justify-between">
        <div>
          <div className="text-2xl font-bold text-green-500">{formatSats(totalBsv)} sats</div>
          <div className="text-xs text-muted-foreground">{(totalBsv / 100_000_000).toFixed(8)} BSV</div>
        </div>
        <Badge variant="secondary">{funding.length} UTXO{funding.length !== 1 ? "s" : ""}</Badge>
      </div>
    </div>
  );
}

export function OrdinalsSection({ ordinals }: { ordinals: IndexedOutput[] }) {
  if (ordinals.length === 0) return null;
  return (
    <div className="border border-blue-500/20 bg-blue-500/5 p-4 rounded-lg">
      <div className="flex items-center gap-2 mb-2">
        <span className="h-2 w-2 rounded-full bg-blue-500" />
        <span className="text-sm font-semibold text-blue-500">Ordinals</span>
      </div>
      <div className="flex items-baseline justify-between">
        <span className="text-sm text-muted-foreground">
          {ordinals.length} inscription{ordinals.length !== 1 ? "s" : ""}
        </span>
        <Badge variant="secondary">{ordinals.length}</Badge>
      </div>
    </div>
  );
}

export function Bsv21Section({ tokens }: { tokens: IndexedOutput[] }) {
  if (tokens.length === 0) return null;
  return (
    <div className="border border-purple-500/20 bg-purple-500/5 p-4 rounded-lg">
      <div className="flex items-center gap-2 mb-2">
        <span className="h-2 w-2 rounded-full bg-purple-500" />
        <span className="text-sm font-semibold text-purple-500">BSV-21 Tokens</span>
      </div>
      <div className="flex items-baseline justify-between">
        <span className="text-sm text-muted-foreground">
          {tokens.length} token output{tokens.length !== 1 ? "s" : ""}
        </span>
        <Badge variant="secondary">{tokens.length}</Badge>
      </div>
    </div>
  );
}

export function Bsv20Section({ tokens }: { tokens: IndexedOutput[] }) {
  if (tokens.length === 0) return null;
  return (
    <div className="border border-muted/30 bg-muted/10 p-4 rounded-lg">
      <div className="flex items-center gap-2 mb-2">
        <span className="h-2 w-2 rounded-full bg-muted-foreground" />
        <span className="text-sm font-semibold text-muted-foreground">BSV-20 Tokens</span>
      </div>
      <p className="text-xs text-muted-foreground">
        {tokens.length} BSV-20 token{tokens.length !== 1 ? "s" : ""} found. Cannot be swept automatically.
      </p>
    </div>
  );
}
```

- [ ] **Step 2: Commit**
```bash
git add sweep/ui/src/components/asset-preview.tsx
git commit -m "feat(sweep): add asset preview sections"
```

### Task 15: Sweep progress + results component

**Files:**
- Create: `sweep/ui/src/components/sweep-progress.tsx`

- [ ] **Step 1: Create sweep progress component**

```tsx
import { CheckCircle2, Loader2, AlertTriangle } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import type { SweepResult } from "@/lib/sweeper";
import { truncate } from "@/lib/utils";

interface Props {
  sweeping: boolean;
  progress: string;
  result: SweepResult | null;
}

export function SweepProgress({ sweeping, progress, result }: Props) {
  if (sweeping) {
    return (
      <div className="text-center space-y-4 py-8">
        <Loader2 className="h-8 w-8 animate-spin mx-auto text-primary" />
        <p className="text-sm text-muted-foreground animate-pulse">{progress}</p>
        <p className="text-xs text-destructive/80">Do not close this page.</p>
      </div>
    );
  }

  if (!result) return null;

  const hasErrors = result.errors.length > 0;
  const hasTxids = result.bsvTxid || result.ordinalTxids.length > 0 || result.bsv21Txids.length > 0;

  return (
    <div className="space-y-4 py-4">
      <div className="flex items-center gap-2">
        {hasErrors ? (
          <AlertTriangle className="h-5 w-5 text-yellow-500" />
        ) : (
          <CheckCircle2 className="h-5 w-5 text-green-500" />
        )}
        <span className="font-semibold">
          {hasErrors && !hasTxids ? "Sweep Failed" : hasErrors ? "Sweep Completed with Errors" : "Sweep Complete"}
        </span>
      </div>

      {result.bsvTxid && (
        <div className="flex justify-between text-sm border-b border-border/30 pb-2">
          <span className="text-muted-foreground">BSV Sweep</span>
          <code className="text-xs font-mono">{truncate(result.bsvTxid, 12)}</code>
        </div>
      )}
      {result.ordinalTxids.map((txid) => (
        <div key={txid} className="flex justify-between text-sm border-b border-border/30 pb-2">
          <span className="text-muted-foreground">Ordinal Sweep</span>
          <code className="text-xs font-mono">{truncate(txid, 12)}</code>
        </div>
      ))}
      {result.bsv21Txids.map((txid) => (
        <div key={txid} className="flex justify-between text-sm border-b border-border/30 pb-2">
          <span className="text-muted-foreground">Token Sweep</span>
          <code className="text-xs font-mono">{truncate(txid, 12)}</code>
        </div>
      ))}

      {result.errors.map((err) => (
        <p key={err} className="text-xs text-destructive">{err}</p>
      ))}
    </div>
  );
}
```

- [ ] **Step 2: Commit**
```bash
git add sweep/ui/src/components/sweep-progress.tsx
git commit -m "feat(sweep): add sweep progress/results component"
```

---

## Chunk 5: Main app assembly

### Task 16: Wire up App.tsx

**Files:**
- Modify: `sweep/ui/src/App.tsx`

- [ ] **Step 1: Implement the main sweep flow**

Replace the placeholder App.tsx with the full sweep wizard:

```tsx
import { useCallback, useState } from "react";
import { Toaster, toast } from "sonner";
import { ArrowDown, RefreshCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { ConnectWallet } from "@/components/connect-wallet";
import { WifInput } from "@/components/wif-input";
import { FundingSection, OrdinalsSection, Bsv21Section, Bsv20Section } from "@/components/asset-preview";
import { SweepProgress } from "@/components/sweep-progress";
import { deriveAddress, scanAddresses, type ScannedAssets } from "@/lib/scanner";
import { executeSweep, type SweepResult } from "@/lib/sweeper";
import { getWallet } from "@/lib/wallet";

type AppState = "connect" | "input" | "scanning" | "preview" | "sweeping" | "complete";

export default function App() {
  const [state, setState] = useState<AppState>("connect");
  const [walletConnected, setWalletConnected] = useState(false);
  const [scanning, setScanning] = useState(false);
  const [scanProgress, setScanProgress] = useState("");
  const [assets, setAssets] = useState<ScannedAssets | null>(null);
  const [wifs, setWifs] = useState<{ pay: string; ord: string } | null>(null);
  const [sweeping, setSweeping] = useState(false);
  const [sweepProgress, setSweepProgress] = useState("");
  const [sweepResult, setSweepResult] = useState<SweepResult | null>(null);

  const handleScan = useCallback(async (payWif: string, ordWif: string) => {
    setScanning(true);
    setState("scanning");
    setAssets(null);
    setSweepResult(null);
    setWifs({ pay: payWif, ord: ordWif });

    try {
      const payAddr = deriveAddress(payWif);
      const ordAddr = deriveAddress(ordWif);

      const result = await scanAddresses(
        [payAddr, ordAddr],
        (p) => setScanProgress(p.detail ?? p.phase),
      );

      setAssets(result);
      const total = result.funding.length + result.ordinals.length +
        result.bsv21Tokens.length + result.bsv20Tokens.length;
      if (total === 0) {
        toast.info("No assets found at legacy addresses");
      }
      setState("preview");
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Scan failed");
      setState("input");
    } finally {
      setScanning(false);
    }
  }, []);

  const handleSweep = useCallback(async () => {
    const wallet = getWallet();
    if (!wallet || !wifs || !assets) return;

    setSweeping(true);
    setState("sweeping");

    try {
      const result = await executeSweep({
        wallet,
        wif: wifs.pay,
        funding: assets.funding,
        ordinals: assets.ordinals,
        bsv21Tokens: assets.bsv21Tokens,
        onProgress: setSweepProgress,
      });

      setSweepResult(result);
      setState("complete");

      if (result.errors.length === 0) {
        toast.success("Sweep complete!");
      } else {
        toast.warning("Sweep completed with some errors");
      }
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Sweep failed");
      setState("preview");
    } finally {
      setSweeping(false);
    }
  }, [wifs, assets]);

  const handleReset = useCallback(() => {
    setAssets(null);
    setSweepResult(null);
    setWifs(null);
    setState(walletConnected ? "input" : "connect");
  }, [walletConnected]);

  const sweepableCount = assets
    ? assets.funding.length + assets.ordinals.length + assets.bsv21Tokens.length
    : 0;

  return (
    <div className="min-h-screen bg-background text-foreground">
      <Toaster position="top-right" />
      <div className="mx-auto max-w-lg p-4 space-y-4 py-12">
        <div className="text-center space-y-2 mb-8">
          <h1 className="text-3xl font-bold tracking-tight">1Sat Sweep</h1>
          <p className="text-sm text-muted-foreground">
            Sweep legacy assets into your BRC-100 wallet
          </p>
        </div>

        {/* Step 1: Connect wallet */}
        <ConnectWallet
          onConnected={() => { setWalletConnected(true); setState("input"); }}
          onDisconnected={() => { setWalletConnected(false); setState("connect"); }}
          connected={walletConnected}
        />

        {/* Step 2: Enter WIF */}
        {walletConnected && state !== "sweeping" && state !== "complete" && (
          <>
            <div className="flex justify-center">
              <ArrowDown className="h-4 w-4 text-muted-foreground" />
            </div>
            <WifInput
              onScan={handleScan}
              scanning={scanning}
              disabled={!walletConnected}
            />
          </>
        )}

        {/* Scan progress */}
        {scanning && (
          <p className="text-sm text-center text-muted-foreground animate-pulse">
            {scanProgress}
          </p>
        )}

        {/* Step 3: Preview assets */}
        {assets && !sweeping && (
          <div className="space-y-3">
            <FundingSection funding={assets.funding} totalBsv={assets.totalBsv} />
            <OrdinalsSection ordinals={assets.ordinals} />
            <Bsv21Section tokens={assets.bsv21Tokens} />
            <Bsv20Section tokens={assets.bsv20Tokens} />

            {sweepableCount > 0 && state === "preview" && (
              <Button onClick={handleSweep} className="w-full h-12 text-base" size="lg">
                Sweep {sweepableCount} Asset{sweepableCount !== 1 ? "s" : ""}
              </Button>
            )}
          </div>
        )}

        {/* Step 4: Sweep progress / results */}
        <SweepProgress
          sweeping={sweeping}
          progress={sweepProgress}
          result={sweepResult}
        />

        {/* Reset */}
        {state === "complete" && (
          <Button variant="outline" onClick={handleReset} className="w-full gap-2">
            <RefreshCw className="h-4 w-4" />
            Sweep Another Wallet
          </Button>
        )}
      </div>
    </div>
  );
}
```

- [ ] **Step 2: Build and verify**

```bash
cd sweep/ui && bun run build
```

Expected: Build succeeds, `dist/` directory created.

- [ ] **Step 3: Commit**
```bash
git add sweep/ui/src/App.tsx
git commit -m "feat(sweep): implement main sweep wizard flow"
```

### Task 17: End-to-end verification

- [ ] **Step 1: Full build**
```bash
cd /path/to/1sat-stack && ./build.sh
```

- [ ] **Step 2: Verify Go compilation**
```bash
go vet ./sweep/...
```

- [ ] **Step 3: Run locally and verify `/1sat/sweep/` loads**

```bash
go run ./cmd/server
# Navigate to http://localhost:8080/1sat/sweep/
```

- [ ] **Step 4: Commit any fixes**

---

## Future Work (not in this plan)

- **Backup file import**: Upload `.json`/`.zip` backup file + passphrase, decrypt via `bitcoin-backup` library to extract WIFs. The 1sat-website already has this flow at `/wallet/import/` that can be adapted.
- **Ordinal selection**: Add per-ordinal selection UI with thumbnail previews (requires ORDFS content rendering). Current version sweeps all ordinals.
- **Separate pay/ord WIF for sweep**: Currently uses pay WIF for all sweeps. Need to determine which WIF controls each output based on address derivation and pass the correct one per sweep call.
- **BSV-21 amount resolution**: Current implementation passes placeholder amounts. Need to fetch actual token amounts from ORDFS metadata or indexed output data.
- **Dark mode**: Add theme support matching admin UI.

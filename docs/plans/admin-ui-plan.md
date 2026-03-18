# Admin UI Plan

Status: **In Progress** (requirements discussion)

Linear: OPL-1186, child of OPL-1183

## Overview

Complete admin UI for 1sat-stack: first-run setup wizard and post-setup settings management. The existing admin UI is a dev test fixture — this is a clean-slate redesign.

## Guiding Principles

- Don't spin up services in invalid states that can't be changed
- Infrastructure services (stateless/embedded) start unconditionally
- Services with their own databases must be configured before first initialization
- The wizard is minimal — only decisions that must happen before first boot
- Everything else goes on the Settings page

## Server Startup Model

### Infrastructure (starts unconditionally, embedded defaults)

These are plumbing. No meaningful operator choices needed at first run:

| Service | Notes |
|---------|-------|
| Store | Badger embedded, reconfigurable later |
| PubSub | Embedded, reconfigurable later |
| Beef | Filesystem storage |
| TXO | Output indexing |
| ORDFS | Content serving (passive, zero cost) |
| Indexer | Transaction parsing |
| MessageBox | Wallet-to-wallet communication |

### Database-backed services (must be configured before starting)

These create databases on first init. Wrong config = data in the wrong place:

| Service | Default | Options |
|---------|---------|---------|
| Wallet | SQLite at `~/.1sat/wallet.sqlite` | SQLite (custom path) or Postgres |
| Chaintracks | SQLite at `~/.1sat/chaintracks` | Embedded or remote |
| Arcade | SQLite at `~/.1sat/arcade/arcade.db` | Embedded or remote |

### Overlays (disabled by default, enabled via Settings page)

All overlays start disabled. Operator enables them after setup:

- Overlay engine, BAP, OPNS, BSV21, BSocial, OrdLock
- Owner sync, Paymail, JungleBus subscriptions

## Private Key Resolution

The server private key is the root identity — wallet, auth, P2P, BRC-42 derivation all depend on it.

**Resolution order:**
1. Env var (`ONESAT_WALLET_SERVER_PRIVATE_KEY`) — if set, use it, don't touch filesystem
2. File (`~/.1sat/server.key`) — if exists, use it
3. Neither — generate new key, write to `~/.1sat/server.key`

**In local mode**, the wizard also allows the user to paste in a WIF to import an existing identity. This is safe because the server is the user's own machine.

**In authenticated mode**, the key is resolved server-side only. No UI exposure — the key is a security-critical server secret.

## First-Run Setup Wizard

The wizard runs when the config store is empty (first boot). Only the infrastructure services and admin UI are running at this point. Database-backed services have NOT initialized yet.

### Step 1: Auth Mode + Private Key

**Choice: Local or Authenticated**

**Local mode** — "This is my personal machine"
- Default: generate a new key automatically
- Option: paste a WIF to import an existing identity (restoring a backup, migrating from another instance)
- The key goes in, never comes back out

**Authenticated mode** — "This is a remote server"
- Key resolves automatically (env → file → generate), no UI
- After restart, first admin connects their external wallet to register

### Step 2: Database Configuration

A single step with three sections, all defaulted to embedded SQLite with pre-filled paths. Operator can accept defaults and click through, or expand a section to change:

**Wallet**
- Embedded SQLite (default): path `~/.1sat/wallet.sqlite`
- Postgres: connection string input

**Chaintracks**
- Embedded (default): path `~/.1sat/chaintracks`
- Remote: URL of another instance

**Arcade**
- Embedded (default): path `~/.1sat/arcade/arcade.db`
- Remote: URL of another instance

### Step 3: Review & Complete

Summary of choices. "Complete Setup" button. Server restarts with full initialization.

In authenticated mode, after restart the wizard is replaced by the first-admin registration flow (connect wallet → become admin).

## Post-Setup Settings Page

*Requirements to be defined — discussion in progress.*

## ConfigStore Implementation

**Status: Code complete, pending review.**

SQLite backend at `~/.1sat/config.db`. Interface: Get, Set, Delete, List (by prefix), IsFirstRun. See `pkg/config/`.

## Implementation Sequence

1. ~~ConfigStore (OPL-1236)~~ — code written, tests pass
2. Private key auto-resolution — implement env → file → generate logic
3. Always-on module initialization (OPL-1237) — infrastructure starts with zero config
4. Integrate ConfigStore into startup (OPL-1238) — first-run detection, phased init
5. Wizard API endpoints (part of OPL-1240)
6. Wizard UI (OPL-1186)
7. Settings page API + UI

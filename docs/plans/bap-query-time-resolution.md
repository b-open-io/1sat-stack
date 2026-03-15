# BAP Query-Time Authority Resolution

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Status: Not Started**

**Goal:** Move BAP authority validation from write-time (topic admission) to read-time (lookup queries), enabling concurrent ingestion and resilience to out-of-order or missing data.

**Architecture:** The topic manager becomes a structural filter — admit anything that looks like a valid BAP+AIP output. The lookup handler stores raw facts keyed by signing address (who signed what, when). Authority resolution happens at query time by walking the address history ordered by compound score (block height + block index). Attestations, revocations, and aliases are stored by signing address and correlated to identities at query time.

**Tech Stack:** Go, SQLite/PostgreSQL, existing bitcom parser, HeightScore compound scoring.

**Deployment:** Drop BAP tables, reset JungleBus progress for BAP subscription, re-sync from block 575000. No migration needed — BAP transaction volume is small.

---

## Why

Write-time validation is fragile:
- Missing data (JungleBus gap, race) causes valid transactions to be rejected
- Out-of-order key rotations produce silently wrong state (A→C accepted when B was missed, then B arrives and invalidates C)
- Sequential processing (concurrency=1) is a bottleneck

Query-time resolution is safe:
- Missing data = incomplete history, not wrong history
- The rotation chain either validates or it doesn't — no silent corruption
- Concurrent ingestion because there's no shared state contention at write time

## Protocol Rules (for reference)

**Identity operations:**
- **ID**: Creates identity (first seen) or rotates signing key. The AIP signer is the *previous* authority; `bap.Address` is the *new* signing address. `bap.IDKey` is the stable BAP ID across all rotations.
- **ATTEST**: Identity attests to a URN hash. Signed by a key belonging to the identity.
- **REVOKE**: Withdraws a previous attestation. Same signing rules as ATTEST.
- **ALIAS**: Updates profile data. Same signing rules as ATTEST.

**Authority chain:** Each key rotation is signed by the previous valid key. The chain is: rootAddress → address1 → address2 → ... → currentAddress. Authority at a given point in time is the address with the highest score that was properly authorized by its predecessor.

**Key insight:** The BAP ID is the stable identifier across all rotations — it's derived from the root address and never changes. All ID transactions carry the same BAP ID. Attestations, revocations, and aliases are signed by an address that belongs to the identity — the signing address is the correlation key, resolved to a BAP ID at query time.

**Parsed BAP fields available:** `Type`, `IDKey`, `Address`, `Sequence`, `Profile`
**Parsed AIP fields available:** `Algorithm`, `Address`, `Signature`, `Valid`

## Schema Changes

### `bap_identity_addresses` — add signer and compound score

New columns:
- `signer TEXT` — the AIP signing address (the *previous* authority that authorized this rotation)
- `score REAL` — `HeightScore(blockHeight, blockIndex)` for deterministic ordering
- `txid` index — for future `OutputBlockHeightUpdated` score updates

Keep `block` for the API response type.

### `bap_attestations` — rekey by signing address

Current PK: `(urn_hash, bap_id)` — requires knowing the BAP ID at write time.
New PK: `(urn_hash, signing_address)` — the signing address is always available at write time.

Add `score REAL` column. Add `txid` index.

The `bap_id` column is removed from the table. BAP ID is resolved at query time by looking up the signing address in `bap_identity_addresses`.

### `bap_identities` — `current_address` becomes cached

`current_address` stays in the schema as a write-time best-effort cache. Query-time resolution validates and may correct it.

`root_address` stays — set once at identity creation.

## Phase 2 (follow-up, not this plan)

**Per-topic engine isolation + merkle proof routing.** Currently the overlay engine broadcasts `OutputBlockHeightUpdated` to ALL lookup services regardless of topic relevance. The `TxTopicIndex` (`txid_topics` table) already maps txid → topics. A future change should:
1. Wire `StatusHandler.handleMined` (and pending auditor) to look up relevant topics via `TxTopicIndex`
2. Route `OutputBlockHeightUpdated` only to the lookup services for those topics
3. This may require per-topic engine instances or a routing layer (see memory: "Overlay topic routing concern")

For now, `OutputBlockHeightUpdated` remains a no-op. All historical data (re-sync) already has merkle paths so scores are correct at ingestion. No live BAP transactions exist yet.

## Files to Modify

| File | Change |
|------|--------|
| `pkg/bap/types.go` | Add `Signer` and `Score` to `Address`. Add `Score` to `Signer`. |
| `pkg/bap/store.go` | Update schema: add `signer`, `score`, `txid` index to addresses. Rekey attestations by `(urn_hash, signing_address)`. Update `BAPStore` interface. |
| `pkg/bap/store_sql.go` | Updated queries for new columns. `loadAddresses` orders by score. Attestation queries use signing_address. Add `LoadAttestationsByAddress` for query-time resolution. |
| `pkg/bap/topic.go` | Remove `BAPStateError`, remove `Lookup` field, structural check only. |
| `pkg/bap/config.go` | Remove `ErrorClassifier`, remove forced `concurrency=1`, bump default to 8. |
| `pkg/bap/lookup.go` | Store raw facts with score and signer. Add `ResolveCurrentAddress`. No identity lookups for ATTEST/REVOKE/ALIAS at write time. |
| `pkg/bap/routes.go` | All identity-returning routes call `ResolveCurrentAddress`. Attestation queries resolve signing address → BAP ID. |

---

## Chunk 1: Types and Schema

### Task 1: Update types

**Files:**
- Modify: `pkg/bap/types.go`

- [ ] **Step 1: Add Signer and Score to Address, Score to Signer**

```go
type Address struct {
	Address string  `json:"address"`
	Signer  string  `json:"-"`
	Txid    string  `json:"txId"`
	Block   uint32  `json:"block"`
	Score   float64 `json:"-"`
}

type Signer struct {
	UrnHash   string  `json:"_"`
	BapID     string  `json:"idKey"`
	Address   string  `json:"signingAddress"`
	Sequence  uint64  `json:"sequence"`
	Block     uint32  `json:"block"`
	Score     float64 `json:"-"`
	Txid      string  `json:"txId"`
	Timestamp uint32  `json:"timestamp"`
	Revoked   bool    `json:"revoked"`
}
```

- [ ] **Step 2: Build**

Run: `go build ./pkg/bap/`

- [ ] **Step 3: Commit**

```bash
git add pkg/bap/types.go
git commit -m "bap: add Signer and Score fields to Address and Signer types"
```

### Task 2: Update schema

**Files:**
- Modify: `pkg/bap/store.go`

- [ ] **Step 1: Update SQLite schema**

```sql
CREATE TABLE IF NOT EXISTS bap_identity_addresses (
	bap_id TEXT NOT NULL,
	address TEXT NOT NULL,
	signer TEXT NOT NULL DEFAULT '',
	txid TEXT NOT NULL DEFAULT '',
	block INTEGER NOT NULL DEFAULT 0,
	score REAL NOT NULL DEFAULT 0,
	PRIMARY KEY (bap_id, address),
	FOREIGN KEY (bap_id) REFERENCES bap_identities(bap_id)
);

CREATE TABLE IF NOT EXISTS bap_attestations (
	urn_hash TEXT NOT NULL,
	signing_address TEXT NOT NULL DEFAULT '',
	sequence INTEGER NOT NULL DEFAULT 0,
	block INTEGER NOT NULL DEFAULT 0,
	score REAL NOT NULL DEFAULT 0,
	txid TEXT NOT NULL DEFAULT '',
	timestamp INTEGER NOT NULL DEFAULT 0,
	revoked INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (urn_hash, signing_address)
);

CREATE INDEX IF NOT EXISTS idx_bap_identity_addresses_address ON bap_identity_addresses(address);
CREATE INDEX IF NOT EXISTS idx_bap_identity_addresses_txid ON bap_identity_addresses(txid);
CREATE INDEX IF NOT EXISTS idx_bap_identities_root_address ON bap_identities(root_address);
CREATE INDEX IF NOT EXISTS idx_bap_identities_current_address ON bap_identities(current_address);
CREATE INDEX IF NOT EXISTS idx_bap_attestations_address ON bap_attestations(signing_address);
CREATE INDEX IF NOT EXISTS idx_bap_attestations_txid ON bap_attestations(txid);
```

Same pattern for Postgres with `topic_id` columns. Attestations PK becomes `(topic_id, urn_hash, signing_address)`.

- [ ] **Step 2: Update BAPStore interface**

Remove `bapID` parameter from attestation methods — they now use signing address:

```go
type BAPStore interface {
	LoadIdentityById(ctx context.Context, id string) (*Identity, error)
	LoadIdentityByAddress(ctx context.Context, address string) (*Identity, error)
	SaveIdentity(ctx context.Context, identity *Identity) error
	SaveAttestation(ctx context.Context, signer *Signer) error
	RevokeAttestation(ctx context.Context, urnHash, signingAddress string) error
	SaveProfile(ctx context.Context, bapId string, profile map[string]any) error
	LoadProfiles(ctx context.Context, limit, offset int) ([]Identity, error)
	Search(ctx context.Context, query string, limit, offset int) ([]Identity, error)
}
```

- [ ] **Step 3: Build** (will fail — store_sql.go not updated yet, that's expected)

- [ ] **Step 4: Commit**

```bash
git add pkg/bap/store.go
git commit -m "bap: rekey attestations by signing_address, add signer/score/txid columns"
```

### Task 3: Update store queries

**Files:**
- Modify: `pkg/bap/store_sql.go`

- [ ] **Step 1: Update `loadAddresses`**

```go
func (s *SQLStore) loadAddresses(ctx context.Context, bapID string) ([]Address, error) {
	q := s.newQB()
	query := fmt.Sprintf(
		`SELECT address, signer, txid, block, score FROM bap_identity_addresses WHERE %sbap_id = %s ORDER BY score ASC`,
		q.topicWhere(), q.ph(bapID),
	)
	rows, err := s.db.QueryContext(ctx, query, q.args...)
	if err != nil {
		return nil, fmt.Errorf("failed to load addresses for %s: %w", bapID, err)
	}
	defer rows.Close()

	var addresses []Address
	for rows.Next() {
		var addr Address
		if err := rows.Scan(&addr.Address, &addr.Signer, &addr.Txid, &addr.Block, &addr.Score); err != nil {
			return nil, fmt.Errorf("failed to scan address row: %w", err)
		}
		addresses = append(addresses, addr)
	}
	return addresses, rows.Err()
}
```

- [ ] **Step 2: Update `SaveIdentity` address insert to include signer and score**

```go
insQuery := fmt.Sprintf(
	`INSERT INTO bap_identity_addresses (%sbap_id, address, signer, txid, block, score) VALUES (%s%s, %s, %s, %s, %s, %s)`,
	aq.topicCols(), aq.topicVals(),
	aq.ph(identity.BapId), aq.ph(addr.Address), aq.ph(addr.Signer), aq.ph(addr.Txid), aq.ph(addr.Block), aq.ph(addr.Score),
)
```

- [ ] **Step 3: Update `SaveAttestation` — keyed by signing address**

```go
func (s *SQLStore) SaveAttestation(ctx context.Context, signer *Signer) error {
	if err := s.ensureSchema(); err != nil {
		return fmt.Errorf("failed to ensure schema: %w", err)
	}

	q := s.newQB()
	var conflictTarget string
	if s.topicID > 0 {
		conflictTarget = "(topic_id, urn_hash, signing_address)"
	} else {
		conflictTarget = "(urn_hash, signing_address)"
	}

	query := fmt.Sprintf(
		`INSERT INTO bap_attestations (%surn_hash, signing_address, sequence, block, score, txid, timestamp, revoked)
		VALUES (%s%s, %s, %s, %s, %s, %s, %s, %s)
		ON CONFLICT %s DO UPDATE SET
			sequence = excluded.sequence,
			block = excluded.block,
			score = excluded.score,
			txid = excluded.txid,
			timestamp = excluded.timestamp,
			revoked = excluded.revoked`,
		q.topicCols(), q.topicVals(),
		q.ph(signer.UrnHash), q.ph(signer.Address), q.ph(signer.Sequence),
		q.ph(signer.Block), q.ph(signer.Score), q.ph(signer.Txid), q.ph(signer.Timestamp), q.ph(signer.Revoked),
		conflictTarget,
	)

	if _, err := s.db.ExecContext(ctx, query, q.args...); err != nil {
		return fmt.Errorf("failed to save attestation %s/%s: %w", signer.UrnHash, signer.Address, err)
	}
	return nil
}
```

- [ ] **Step 4: Update `RevokeAttestation` — by signing address**

```go
func (s *SQLStore) RevokeAttestation(ctx context.Context, urnHash, signingAddress string) error {
	if err := s.ensureSchema(); err != nil {
		return fmt.Errorf("failed to ensure schema: %w", err)
	}

	q := s.newQB()
	revokedVal := 1
	query := fmt.Sprintf(
		`UPDATE bap_attestations SET revoked = %s WHERE %surn_hash = %s AND signing_address = %s`,
		q.ph(revokedVal), q.topicWhere(), q.ph(urnHash), q.ph(signingAddress),
	)

	if _, err := s.db.ExecContext(ctx, query, q.args...); err != nil {
		return fmt.Errorf("failed to revoke attestation %s/%s: %w", urnHash, signingAddress, err)
	}
	return nil
}
```

- [ ] **Step 5: Build**

Run: `go build ./pkg/bap/`

- [ ] **Step 6: Commit**

```bash
git add pkg/bap/store_sql.go
git commit -m "bap: update store queries for signer, score, and signing_address PK"
```

---

## Chunk 2: Topic Manager and Config

### Task 4: Simplify topic manager to structural validation

**Files:**
- Modify: `pkg/bap/topic.go`

- [ ] **Step 1: Remove BAPStateError and all state validation**

```go
package bap

import (
	"context"

	"github.com/bitcoin-sv/go-templates/template/bitcom"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// TopicManager implements the overlay TopicManager interface for BAP protocol outputs.
// Performs structural validation only — authority is resolved at query time.
type TopicManager struct{}

// IdentifyAdmissibleOutputs admits outputs that contain valid BAP protocol data
// with a valid AIP signature. No state or identity checks.
func (tm *TopicManager) IdentifyAdmissibleOutputs(ctx context.Context, beef *transaction.Beef, txid *chainhash.Hash, previousCoins []uint32) (admit overlay.AdmittanceInstructions, err error) {
	tx := beef.FindTransactionForSigningByHash(txid)
	if tx == nil {
		return admit, engine.ErrInvalidBeef
	}

	for vout, output := range tx.Outputs {
		bc := bitcom.Decode(output.LockingScript)
		if bc == nil {
			continue
		}
		bap := bitcom.DecodeBAP(bc)
		if bap == nil {
			continue
		}
		var hasValidAIP bool
		for _, a := range bitcom.DecodeAIP(bc) {
			if a.Valid {
				hasValidAIP = true
				break
			}
		}
		if !hasValidAIP {
			continue
		}
		admit.OutputsToAdmit = append(admit.OutputsToAdmit, uint32(vout))
	}
	return
}

// IdentifyNeededInputs returns the list of inputs needed for validation.
// BAP does not require any additional inputs.
func (tm *TopicManager) IdentifyNeededInputs(ctx context.Context, beef *transaction.Beef, txid *chainhash.Hash) ([]*transaction.Outpoint, error) {
	return nil, nil
}

// GetDocumentation returns documentation for this topic manager.
func (tm *TopicManager) GetDocumentation() string {
	return "BAP Topic Manager"
}

// GetMetaData returns metadata for this topic manager.
func (tm *TopicManager) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{
		Name: "bap",
	}
}
```

- [ ] **Step 2: Build**

Run: `go build ./pkg/bap/`

- [ ] **Step 3: Commit**

```bash
git add pkg/bap/topic.go
git commit -m "bap: simplify topic manager to structural validation only"
```

### Task 5: Update config

**Files:**
- Modify: `pkg/bap/config.go`

- [ ] **Step 1: Remove ErrorClassifier, concurrency override, Lookup dependency**

In `Initialize`, remove the `c.Sync.Concurrency = 1` line and the `c.Sync.ErrorClassifier` block. Change TopicManager construction from `&TopicManager{Lookup: lookupSvc}` to `&TopicManager{}`. Remove the `errors` import.

- [ ] **Step 2: Change default concurrency to 8**

In `SetDefaults`: `v.SetDefault(p+"sync.concurrency", 8)`

- [ ] **Step 3: Build**

Run: `go build ./pkg/bap/`

- [ ] **Step 4: Commit**

```bash
git add pkg/bap/config.go
git commit -m "bap: remove error classifier, set default concurrency to 8"
```

---

## Chunk 3: Lookup Handler — Store Raw Facts

### Task 6: Rewrite OutputAdmittedByTopic

**Files:**
- Modify: `pkg/bap/lookup.go`

- [ ] **Step 1: Rewrite handler to store raw facts**

Key changes:
- ID operations: store the new address with the AIP signer and compound score
- ATTEST/REVOKE/ALIAS: store by signing address only. No identity lookup needed at write time.
- Import `types` for `ScoreFromTx`

```go
func (l *LookupService) OutputAdmittedByTopic(ctx context.Context, payload *engine.OutputAdmittedByTopic) error {
	_, tx, txid, err := transaction.ParseBeef(payload.AtomicBEEF)
	if err != nil {
		return err
	}
	output := tx.Outputs[payload.OutputIndex]
	bc := bitcom.Decode(output.LockingScript)
	if bc == nil {
		return nil
	}

	score := types.ScoreFromTx(tx, txid)
	var height uint32
	if tx.MerklePath != nil {
		height = tx.MerklePath.BlockHeight
	}

	bap := bitcom.DecodeBAP(bc)
	if bap == nil {
		return nil
	}
	var aip *bitcom.AIP
	for _, a := range bitcom.DecodeAIP(bc) {
		if a.Valid {
			aip = a
			break
		}
	}
	if aip == nil {
		return nil
	}

	txidStr := txid.String()

	switch bap.Type {
	case bitcom.ID:
		id, err := l.store.LoadIdentityById(ctx, bap.IDKey)
		if err != nil {
			return err
		}
		if id == nil {
			id = &Identity{
				BapId:          bap.IDKey,
				RootAddress:    aip.Address,
				CurrentAddress: bap.Address,
				Addresses:      []Address{},
				FirstSeen:      height,
				FirstSeenTxid:  txidStr,
			}
		}
		id.CurrentAddress = bap.Address
		id.Addresses = append(id.Addresses, Address{
			Address: bap.Address,
			Signer:  aip.Address,
			Txid:    txidStr,
			Block:   height,
			Score:   score,
		})
		if err := l.store.SaveIdentity(ctx, id); err != nil {
			return err
		}

	case bitcom.ATTEST:
		signer := &Signer{
			UrnHash: bap.IDKey,
			Address: aip.Address,
			Txid:    txidStr,
			Block:   height,
			Score:   score,
			Revoked: false,
		}
		if err := l.store.SaveAttestation(ctx, signer); err != nil {
			return err
		}

	case bitcom.REVOKE:
		if err := l.store.RevokeAttestation(ctx, bap.IDKey, aip.Address); err != nil {
			return err
		}

	case bitcom.ALIAS:
		if len(bap.Profile) > 0 {
			p := map[string]any{}
			if err := json.Unmarshal(bap.Profile, &p); err != nil {
				return fmt.Errorf("failed to unmarshal profile: %w", err)
			}
			if err := l.store.SaveProfile(ctx, bap.IDKey, p); err != nil {
				return err
			}
		}
	}
	return nil
}
```

- [ ] **Step 2: Build**

Run: `go build ./pkg/bap/`

- [ ] **Step 3: Commit**

```bash
git add pkg/bap/lookup.go
git commit -m "bap: store raw facts by signing address, no identity lookup at write time"
```

---

## Chunk 4: Query-Time Authority Resolution

### Task 7: Add ResolveCurrentAddress

**Files:**
- Modify: `pkg/bap/lookup.go`

- [ ] **Step 1: Add authority resolution function**

```go
// ResolveCurrentAddress walks the address chain ordered by score and validates
// that each rotation was authorized by the previous key. Returns the latest
// validated address and the validated chain.
func ResolveCurrentAddress(identity *Identity) (currentAddr string, validChain []Address) {
	if identity == nil || len(identity.Addresses) == 0 {
		return "", nil
	}

	// First address: root. Self-authorizing — the signer IS the root.
	validChain = append(validChain, identity.Addresses[0])
	currentAddr = identity.Addresses[0].Address

	for i := 1; i < len(identity.Addresses); i++ {
		addr := identity.Addresses[i]
		// Valid rotation: this ID tx was signed by the previous current address.
		if addr.Signer != currentAddr {
			// Chain broken — a rotation step is missing or out of order.
			break
		}
		validChain = append(validChain, addr)
		currentAddr = addr.Address
	}

	return currentAddr, validChain
}

// IsAuthorityAtScore checks whether the given address was the valid authority
// for the identity at the specified score (block height + index).
func IsAuthorityAtScore(identity *Identity, address string, atScore float64) bool {
	if identity == nil || len(identity.Addresses) == 0 {
		return false
	}

	currentAddr := identity.Addresses[0].Address
	for i := 1; i < len(identity.Addresses); i++ {
		addr := identity.Addresses[i]
		if addr.Score > atScore {
			break
		}
		if addr.Signer != currentAddr {
			break
		}
		currentAddr = addr.Address
	}

	return address == currentAddr
}
```

- [ ] **Step 2: Build**

Run: `go build ./pkg/bap/`

- [ ] **Step 3: Commit**

```bash
git add pkg/bap/lookup.go
git commit -m "bap: add query-time authority resolution functions"
```

---

## Chunk 5: Update API Routes

### Task 8: Wire authority resolution into routes

**Files:**
- Modify: `pkg/bap/routes.go`

- [ ] **Step 1: Update GetIdentity**

After loading identity, resolve authority before returning:

```go
func (r *Routes) GetIdentity(c *fiber.Ctx) error {
	req := map[string]string{}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"message": "Invalid request body: " + err.Error(),
		})
	}

	id, err := r.lookup.LoadIdentityById(c.Context(), req["idKey"])
	if err != nil {
		r.logger.Error("failed to fetch identity", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"message": "Failed to fetch identity: " + err.Error(),
		})
	}
	if id == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"message": "Identity not found for ID key: " + req["idKey"],
		})
	}

	currentAddr, validChain := ResolveCurrentAddress(id)
	id.CurrentAddress = currentAddr
	id.Addresses = validChain

	return c.JSON(id)
}
```

- [ ] **Step 2: Update ValidByAddress**

Use score-based chain walking:

```go
func (r *Routes) ValidByAddress(c *fiber.Ctx) error {
	var req ValidByAddressRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"message": "Invalid request body: " + err.Error(),
		})
	}
	if req.Address == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"message": "Address is required",
		})
	}

	identity, err := r.lookup.LoadIdentityByAddress(c.Context(), req.Address)
	if err != nil {
		r.logger.Error("failed to fetch identity by address", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"message": "Failed to fetch identity: " + err.Error(),
		})
	}
	if identity == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"message": "Identity not found for address: " + req.Address,
		})
	}

	currentAddr, validChain := ResolveCurrentAddress(identity)
	identity.CurrentAddress = currentAddr
	identity.Addresses = validChain

	var valid bool
	var validBlock uint32

	if req.Block > 0 {
		for _, addr := range validChain {
			if addr.Block <= req.Block {
				valid = addr.Address == req.Address
				validBlock = addr.Block
			}
		}
	} else {
		valid = req.Address == currentAddr
	}

	resp := ValidByAddressResponse{
		Identity:       *identity,
		ValidityRecord: ValidityRecord{Valid: valid, Block: validBlock},
	}
	if valid {
		resp.Profile = identity.Profile
	}

	return c.JSON(resp)
}
```

- [ ] **Step 3: Update SearchIdentities and ListProfiles to resolve authority**

Both should call `ResolveCurrentAddress` on each returned identity so `CurrentAddress` is validated.

- [ ] **Step 4: Build**

Run: `go build ./pkg/bap/`

- [ ] **Step 5: Commit**

```bash
git add pkg/bap/routes.go
git commit -m "bap: wire query-time authority resolution into all API routes"
```

### Task 9: Verify full build

- [ ] **Step 1: Build entire project**

Run: `go build ./...`

- [ ] **Step 2: Run tests**

Run: `go test ./pkg/bap/... -v`
Run: `go test ./... -count=1`

- [ ] **Step 3: Commit any fixes**

---

## Deployment

1. Drop BAP overlay tables (or delete the SQLite database for `tm_bap`)
2. Reset JungleBus progress for the BAP subscription ID
3. Restart the stack — BAP will re-sync from block 575000 with the new schema

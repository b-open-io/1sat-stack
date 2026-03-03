# OpNS Overlay Simplification + ORDFS Integration — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Strip the OpNS overlay to a thin name→origin registry, resolve identity via ORDFS, and add synchronous Arcade broadcast to paymail receive.

**Architecture:** The overlay stops tracking addresses, identity keys, origins, and ordlock listings — it only maps `opns:{domain}` and `mine:{prefix}`. Identity resolution moves to ORDFS (`Load` with `Seq: -1, Map: true`), which returns merged MAP data including `opns.idKey`. Paymail receive broadcasts through Arcade before calling InternalizeAction.

**Tech Stack:** Go (1sat-stack), Fiber HTTP framework, go-sdk wallet, go-templates, ORDFS, Arcade

---

## Task 1: Strip OutputAdmittedByTopic to name registry events only

**Files:**
- Modify: `pkg/opns/lookup.go:50-178`

**Step 1: Remove origin tracking, p2pkh tracking, ordlock tracking, and MAP parsing**

In `OutputAdmittedByTopic`, the event taxonomy comment (line 51) currently says:
```go
// Events follow the taxonomy: opns:{domain}, mine:{domain}, origin:{outpoint}, p2pkh:{address}, list:{domain}
```

Replace the entire method body to only emit `opns:{domain}` and `mine:{prefix}` events. The ordinal-origin tracking block (lines 72-113), the p2pkh/ordlock detection block (lines 130-135), and the MAP parsing block (lines 137-148) are all removed.

The new method:

```go
func (l *LookupService) OutputAdmittedByTopic(ctx context.Context, payload *engine.OutputAdmittedByTopic) error {
	_, tx, txid, err := transaction.ParseBeef(payload.AtomicBEEF)
	if err != nil {
		return fmt.Errorf("failed to parse BEEF: %w", err)
	}

	if int(payload.OutputIndex) >= len(tx.Outputs) {
		return nil
	}

	outpoint := &transaction.Outpoint{
		Txid:  *txid,
		Index: payload.OutputIndex,
	}

	txOut := tx.Outputs[payload.OutputIndex]
	outputEvents := make([]string, 0, 2)

	// Decode OpNS contract state (mine event)
	if o := opns.Decode(txOut.LockingScript); o != nil {
		outputEvents = append(outputEvents, "mine:"+o.Domain)
	} else if insc := inscription.Decode(txOut.LockingScript); insc != nil && insc.File.Type == "application/op-ns" {
		// Inscription claiming a domain — track name→origin
		domain := string(insc.File.Content)
		outputEvents = append(outputEvents, "opns:"+domain)

		// Inherit opns: events from input ordinal (transfer tracking)
		if txOut.Satoshis == 1 {
			satsOut := uint64(0)
			for _, output := range tx.Outputs[:payload.OutputIndex] {
				satsOut += output.Satoshis
			}
			satsIn := uint64(0)
			for _, input := range tx.Inputs {
				sourceOut := input.SourceTxOutput()
				if sourceOut == nil {
					break
				}
				if satsIn < satsOut {
					satsIn += sourceOut.Satoshis
					continue
				} else if satsIn == satsOut && sourceOut.Satoshis == 1 {
					inputOutpoint := &transaction.Outpoint{
						Txid:  *input.SourceTXID,
						Index: input.SourceTxOutIndex,
					}
					inputEvents, err := l.store.SMembers(ctx, outpointEventsKey(inputOutpoint))
					if err != nil {
						return fmt.Errorf("failed to load input events: %w", err)
					}
					for _, evt := range inputEvents {
						evtStr := string(evt)
						if strings.HasPrefix(evtStr, "opns:") {
							// Don't duplicate if we already have it
							found := false
							for _, existing := range outputEvents {
								if existing == evtStr {
									found = true
									break
								}
							}
							if !found {
								outputEvents = append(outputEvents, evtStr)
							}
						}
					}
					break
				} else {
					break
				}
			}
		}
	} else if txOut.Satoshis == 1 {
		// Transfer of an existing ordinal — inherit opns: events only
		satsOut := uint64(0)
		for _, output := range tx.Outputs[:payload.OutputIndex] {
			satsOut += output.Satoshis
		}
		satsIn := uint64(0)
		for _, input := range tx.Inputs {
			sourceOut := input.SourceTxOutput()
			if sourceOut == nil {
				break
			}
			if satsIn < satsOut {
				satsIn += sourceOut.Satoshis
				continue
			} else if satsIn == satsOut && sourceOut.Satoshis == 1 {
				inputOutpoint := &transaction.Outpoint{
					Txid:  *input.SourceTXID,
					Index: input.SourceTxOutIndex,
				}
				inputEvents, err := l.store.SMembers(ctx, outpointEventsKey(inputOutpoint))
				if err != nil {
					return fmt.Errorf("failed to load input events: %w", err)
				}
				for _, evt := range inputEvents {
					evtStr := string(evt)
					if strings.HasPrefix(evtStr, "opns:") {
						outputEvents = append(outputEvents, evtStr)
					}
				}
				break
			} else {
				break
			}
		}
	}

	if len(outputEvents) == 0 {
		return nil
	}

	score := types.ScoreFromTx(tx, txid)
	opBytes := outpoint.Bytes()
	member := store.ScoredMember{Member: opBytes, Score: score}

	eventMembers := make([][]byte, 0, len(outputEvents))
	for _, evt := range outputEvents {
		if err := l.store.ZAdd(ctx, eventKey(evt), member); err != nil {
			return fmt.Errorf("failed to add to event ZSet %s: %w", evt, err)
		}
		eventMembers = append(eventMembers, []byte(evt))
	}

	if err := l.store.SAdd(ctx, outpointEventsKey(outpoint), eventMembers...); err != nil {
		return fmt.Errorf("failed to save outpoint events: %w", err)
	}

	slog.Debug("OpNS events indexed",
		"outpoint", outpoint.OrdinalString(),
		"events", outputEvents,
	)
	return nil
}
```

**Step 2: Remove unused imports**

Remove these imports that are no longer needed:
- `"github.com/bitcoin-sv/go-templates/template/bitcom"`
- `"github.com/bitcoin-sv/go-templates/template/ordlock"`
- `"github.com/bsv-blockchain/go-sdk/script"`
- `"github.com/bsv-blockchain/go-sdk/transaction/template/p2pkh"`

Keep:
- `"github.com/bitcoin-sv/go-templates/template/inscription"` — still used for domain detection
- `"github.com/bitcoin-sv/go-templates/template/opns"` — still used for mine detection

**Step 3: Build to verify**

Run: `cd /home/shruggr/Code/1sat-stack && go build ./pkg/opns/...`
Expected: PASS

**Step 4: Commit**

```bash
git add pkg/opns/lookup.go
git commit -m "refactor: strip OpNS overlay to name registry (opns: and mine: events only)"
```

---

## Task 2: Replace Owner() with Origin() on LookupService

**Files:**
- Modify: `pkg/opns/lookup.go:280-329`

**Step 1: Delete OwnerResult type and Owner() method**

Remove lines 280-329 (the `OwnerResult` struct and `Owner()` method).

**Step 2: Add Origin() method**

Add after `GetMetaData()`:

```go
// Origin returns the origin outpoint for a registered OpNS domain.
// The origin is the outpoint where the ordinal was first inscribed — it never changes.
func (l *LookupService) Origin(ctx context.Context, domain string) (*transaction.Outpoint, error) {
	key := eventKey("opns:" + domain)
	members, err := l.store.ZRange(ctx, key, store.ScoreRange{})
	if err != nil {
		return nil, fmt.Errorf("failed to query opns event for domain %s: %w", domain, err)
	}

	if len(members) == 0 {
		return nil, nil
	}

	// The current outpoint holding this domain
	currentOutpoint := transaction.NewOutpointFromBytes(members[0].Member)
	if currentOutpoint == nil {
		return nil, fmt.Errorf("failed to decode outpoint for domain %s", domain)
	}

	// Walk back through events to find the origin
	// The origin is the outpoint that first inscribed this domain — it's stored as an opns: event
	// Since the overlay tracks the current UTXO (not the origin), and the origin never changes,
	// we need the origin from ORDFS. But we can derive it: the first inscription of the domain
	// IS the origin. For domains that have been transferred, the origin is inherited through
	// the ordinal chain — ORDFS resolves this.
	//
	// For now, return the current outpoint. The caller (paymail) will pass this to ORDFS
	// with seq=-1, and ORDFS will resolve back to the origin internally.
	return currentOutpoint, nil
}
```

**Step 3: Build to verify**

Run: `cd /home/shruggr/Code/1sat-stack && go build ./pkg/opns/...`
Expected: May fail if paymail/service.go still references Owner(). That's fine — we fix it in Task 4.

**Step 4: Commit**

```bash
git add pkg/opns/lookup.go
git commit -m "refactor: replace Owner() with Origin() on OpNS LookupService"
```

---

## Task 3: Delete GetOwner route, update OpNS route registration

**Files:**
- Modify: `pkg/opns/routes.go`

**Step 1: Remove GetOwner handler and route registration**

Remove the `GetOwner` method (lines 32-68) and remove the owner route from `Register`:

```go
func (r *Routes) Register(router fiber.Router) {
	router.Get("/mine/:name", r.GetMine)
}
```

**Step 2: Build to verify**

Run: `cd /home/shruggr/Code/1sat-stack && go build ./pkg/opns/...`
Expected: PASS

**Step 3: Commit**

```bash
git add pkg/opns/routes.go
git commit -m "refactor: remove /opns/owner route (replaced by paymail endpoints)"
```

---

## Task 4: Update paymail Service — add ORDFS + Arcade deps, rewrite ResolveIdentityKey

**Files:**
- Modify: `pkg/paymail/service.go`

**Step 1: Add ORDFS and Arcade imports and struct fields**

Update the import block to add:
```go
	"encoding/json"

	"github.com/b-open-io/1sat-stack/pkg/ordfs"
	arcadeservice "github.com/bsv-blockchain/arcade/service"
```

Update the Service struct to:

```go
type Service struct {
	opns          *opns.LookupService
	ordfs         *ordfs.Ordfs
	arcade        arcadeservice.ArcadeService
	wallet        wallet.Interface
	store         *Store
	anyoneDeriver *wallet.KeyDeriver
	logger        *slog.Logger
}
```

**Step 2: Update NewService constructor**

```go
func NewService(
	opnsLookup *opns.LookupService,
	ordfsService *ordfs.Ordfs,
	arcadeService arcadeservice.ArcadeService,
	w wallet.Interface,
	logger *slog.Logger,
) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	anyonePriv, _ := wallet.AnyoneKey()
	return &Service{
		opns:          opnsLookup,
		ordfs:         ordfsService,
		arcade:        arcadeService,
		wallet:        w,
		store:         NewStore(),
		anyoneDeriver: wallet.NewKeyDeriver(anyonePriv),
		logger:        logger,
	}
}
```

**Step 3: Rewrite ResolveIdentityKey to use ORDFS**

Replace the entire `ResolveIdentityKey` method:

```go
// ResolveIdentityKey resolves a paymail alias to an identity public key
// by looking up the OpNS origin and reading the MAP opns.idKey field via ORDFS.
func (s *Service) ResolveIdentityKey(ctx context.Context, alias string) (*ec.PublicKey, error) {
	// Step 1: Get the current outpoint for this domain
	outpoint, err := s.opns.Origin(ctx, alias)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve OpNS name %q: %w", alias, err)
	}
	if outpoint == nil {
		return nil, fmt.Errorf("no OpNS registration found for %q", alias)
	}

	// Step 2: Load latest state via ORDFS (seq=-1 means latest)
	seq := -1
	resp, err := s.ordfs.Load(ctx, &ordfs.Request{
		Outpoint: outpoint,
		Seq:      &seq,
		Map:      true,
	})
	if err != nil {
		return nil, fmt.Errorf("ORDFS resolution failed for %q: %w", alias, err)
	}
	if resp == nil || resp.Map == nil {
		return nil, fmt.Errorf("no MAP data found for OpNS name %q", alias)
	}

	// Step 3: Extract opns.idKey from merged MAP data
	var mapData map[string]string
	if err := json.Unmarshal(resp.Map, &mapData); err != nil {
		return nil, fmt.Errorf("failed to parse MAP data for %q: %w", alias, err)
	}

	idKeyHex, ok := mapData["opns.idKey"]
	if !ok || idKeyHex == "" {
		return nil, fmt.Errorf("no identity key registered for OpNS name %q", alias)
	}

	// Step 4: Parse as public key
	pubKeyBytes, err := hex.DecodeString(idKeyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid identity key hex for %q: %w", alias, err)
	}

	pubKey, err := ec.PublicKeyFromBytes(pubKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("invalid identity public key for %q: %w", alias, err)
	}

	return pubKey, nil
}
```

**Step 4: Add Arcade() accessor**

Add after the existing accessor methods:

```go
// Arcade returns the arcade service for direct broadcast.
func (s *Service) Arcade() arcadeservice.ArcadeService {
	return s.arcade
}
```

**Step 5: Build to verify**

Run: `cd /home/shruggr/Code/1sat-stack && go build ./pkg/paymail/...`
Expected: PASS (or fail on config.go call site — fixed in Task 6)

**Step 6: Commit**

```bash
git add pkg/paymail/service.go
git commit -m "feat: resolve identity key via ORDFS, add Arcade dependency to paymail"
```

---

## Task 5: Add Arcade broadcast to paymail receive flow

**Files:**
- Modify: `pkg/paymail/routes.go`

**Step 1: Add Arcade broadcast to ReceiveBeef before InternalizeAction**

In the `ReceiveBeef` method, after `verifyPayment` succeeds and before `internalizePayment`, add:

```go
	// Broadcast through Arcade synchronously — sender gets immediate feedback
	status, err := r.service.Arcade().SubmitTransaction(c.Context(), beefBytes, nil)
	if err != nil {
		r.logger.Error("arcade broadcast failed", "alias", alias, "error", err)
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": fmt.Sprintf("broadcast failed: %v", err)})
	}
	if status != nil && status.TxStatus == "rejected" {
		r.logger.Warn("arcade rejected transaction", "alias", alias, "status", status)
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "transaction rejected by network"})
	}
```

**Step 2: Add broadcast to ReceiveTransaction similarly**

In the `ReceiveTransaction` method, after `verifyPayment`, add the same broadcast pattern but using `tx.Bytes()`:

```go
	// Broadcast through Arcade synchronously
	txBytes := tx.Bytes()
	status, err := r.service.Arcade().SubmitTransaction(c.Context(), txBytes, nil)
	if err != nil {
		r.logger.Error("arcade broadcast failed", "alias", alias, "error", err)
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": fmt.Sprintf("broadcast failed: %v", err)})
	}
	if status != nil && status.TxStatus == "rejected" {
		r.logger.Warn("arcade rejected transaction", "alias", alias, "status", status)
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "transaction rejected by network"})
	}
```

**Step 3: Add the `arcade/models` import**

Add to the import block:
```go
	"github.com/bsv-blockchain/arcade/models"
```

Note: `models` may not be needed directly if we only check `status.TxStatus`. Verify the `TransactionStatus` type fields. If `TxStatus` doesn't exist, check the actual struct definition:

```bash
grep -r 'type TransactionStatus struct' $(go env GOMODCACHE)/github.com/bsv-blockchain/arcade*/
```

Adjust the rejection check based on the actual struct fields.

**Step 4: Build to verify**

Run: `cd /home/shruggr/Code/1sat-stack && go build ./pkg/paymail/...`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/paymail/routes.go
git commit -m "feat: broadcast through Arcade synchronously before InternalizeAction"
```

---

## Task 6: Update paymail config and server wiring

**Files:**
- Modify: `pkg/paymail/config.go`
- Modify: `cmd/server/config.go`

**Step 1: Update paymail InitializeDeps to include ORDFS and Arcade**

In `pkg/paymail/config.go`, update `InitializeDeps`:

```go
type InitializeDeps struct {
	OpnsLookup *opns.LookupService
	Ordfs      *ordfs.Ordfs
	Arcade     arcadeservice.ArcadeService
	Wallet     sdk.Interface
}
```

Add imports:
```go
	"github.com/b-open-io/1sat-stack/pkg/ordfs"
	arcadeservice "github.com/bsv-blockchain/arcade/service"
```

**Step 2: Update Initialize to pass new deps**

Update the `NewService` call:
```go
	if deps.Ordfs == nil {
		return nil, fmt.Errorf("ORDFS service is required for paymail")
	}
	if deps.Arcade == nil {
		return nil, fmt.Errorf("Arcade service is required for paymail")
	}

	service := NewService(deps.OpnsLookup, deps.Ordfs, deps.Arcade, deps.Wallet, logger)
```

**Step 3: Update server config.go to pass ORDFS and Arcade to paymail**

In `cmd/server/config.go`, update the paymail initialization block to include ORDFS and Arcade:

```go
	if c.Paymail.Mode != paymail.ModeDisabled {
		paymailDeps := &paymail.InitializeDeps{}
		if svc.OPNS != nil && svc.OPNS.Lookup != nil {
			paymailDeps.OpnsLookup = svc.OPNS.Lookup
		}
		if svc.ORDFS != nil && svc.ORDFS.Ordfs != nil {
			paymailDeps.Ordfs = svc.ORDFS.Ordfs
		}
		if svc.Arcade != nil && svc.Arcade.ArcadeService != nil {
			paymailDeps.Arcade = svc.Arcade.ArcadeService
		}
		if svc.Wallet != nil && svc.Wallet.Wallet != nil {
			paymailDeps.Wallet = svc.Wallet.Wallet
		}
		paymailSvc, err := c.Paymail.Initialize(ctx, logger, paymailDeps)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize paymail: %w", err)
		}
		svc.Paymail = paymailSvc
	}
```

**Step 4: Build to verify**

Run: `cd /home/shruggr/Code/1sat-stack && go build ./...`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/paymail/config.go cmd/server/config.go
git commit -m "feat: wire ORDFS and Arcade into paymail initialization"
```

---

## Task 7: Verification — build and check

**Step 1: Full Go build**

Run: `cd /home/shruggr/Code/1sat-stack && go build ./...`
Expected: PASS

**Step 2: Verify no remaining Owner references in paymail**

Run: `grep -r 'Owner' pkg/paymail/`
Expected: No matches

**Step 3: Verify no remaining idkey/p2pkh/origin events in lookup**

Run: `grep -rn 'idkey:\|p2pkh:\|origin:\|list:' pkg/opns/lookup.go`
Expected: No matches

**Step 4: Verify ORDFS is used for identity resolution**

Run: `grep -n 'ordfs' pkg/paymail/service.go`
Expected: Shows ORDFS import and usage in ResolveIdentityKey

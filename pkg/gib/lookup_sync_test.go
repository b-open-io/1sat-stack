package gib

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	"github.com/b-open-io/1sat-stack/pkg/overlay"
	overlaystorage "github.com/b-open-io/1sat-stack/pkg/overlay/storage"
	gibtpl "github.com/b-open-io/1sat-stack/pkg/template/gib"
	overlaylookup "github.com/bsv-blockchain/go-sdk/overlay/lookup"
	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/spv"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/chaintracker"
	"github.com/spf13/viper"
)

// The sync lookups exercised the way production drives them: real signed
// transactions submitted through the module engine, then questions asked of
// engine.Lookup (not the service directly), so the formula-hydration path is
// in the test's way exactly as it is in a deployment's.

type syncEnv struct {
	t        *testing.T
	svc      *Services
	identity *ec.PublicKey
	beefDir  string
	// mint carries the main and dev heads; push1 and push2 advance main.
	mint, push1, push2 *transaction.Transaction
}

func hexPub(k *ec.PublicKey) string { return hex.EncodeToString(k.Compressed()) }

// ask puts a question to the engine, the way a BRC-24 client does.
func (e *syncEnv) ask(query string) *overlaylookup.LookupAnswer {
	e.t.Helper()
	ans, err := e.svc.Engine.Lookup(e.t.Context(), &overlaylookup.LookupQuestion{
		Service: LookupName,
		Query:   json.RawMessage(query),
	})
	if err != nil {
		e.t.Fatalf("lookup %s: %v", query, err)
	}
	if ans.Type != overlaylookup.AnswerTypeOutputList {
		e.t.Fatalf("answer type = %q, want output-list", ans.Type)
	}
	return ans
}

// headsSince decodes the answer into the outpoints it carries plus its
// result envelope, checking the BEEF really is the head's transaction.
func (e *syncEnv) headsSince(query string) ([]string, HeadsSinceResult) {
	e.t.Helper()
	ans := e.ask(query)
	var res HeadsSinceResult
	decodeResult(e.t, ans.Result, &res)
	got := []string{}
	for i, item := range ans.Outputs {
		_, tx, txid, err := transaction.ParseBeef(item.Beef)
		if err != nil {
			e.t.Fatalf("output %d: parse BEEF: %v", i, err)
		}
		op := (&transaction.Outpoint{Txid: *txid, Index: item.OutputIndex}).OrdinalString()
		if int(item.OutputIndex) >= len(tx.Outputs) {
			e.t.Fatalf("output %d: index %d out of range", i, item.OutputIndex)
		}
		out := tx.Outputs[item.OutputIndex]
		if _, err := gibtpl.Decode(out.LockingScript, out.Satoshis); err != nil {
			e.t.Fatalf("output %d (%s) is not a head: %v", i, op, err)
		}
		got = append(got, op)
	}
	if len(got) != len(res.Outpoints) {
		e.t.Fatalf("outputs %v and result outpoints %v are not aligned", got, res.Outpoints)
	}
	for i := range got {
		if got[i] != res.Outpoints[i] {
			e.t.Fatalf("output %d is %s, result says %s", i, got[i], res.Outpoints[i])
		}
	}
	return got, res
}

// decodeResult re-marshals the answer's result so the test reads it the way
// a client does, through JSON, rather than by type assertion.
func decodeResult(t *testing.T, result any, into any) {
	t.Helper()
	if result == nil {
		t.Fatal("answer carries no result")
	}
	b, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, into); err != nil {
		t.Fatal(err)
	}
}

func newSyncEnv(t *testing.T) *syncEnv {
	t.Helper()
	factory, err := overlaystorage.NewSQLiteFactory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = factory.Close() })
	beefDir := t.TempDir()
	fs, err := beef.NewFilesystemBeefStorage(beefDir)
	if err != nil {
		t.Fatal(err)
	}
	beefStore := beef.NewStorageFromProviders([]beef.BaseBeefStorage{fs}, nil)

	v := viper.New()
	var cfg Config
	cfg.SetDefaults(v, "")
	v.Set("mode", ModeEmbedded)
	if err := v.Unmarshal(&cfg); err != nil {
		t.Fatal(err)
	}
	svc, err := cfg.Initialize(t.Context(), nil, &overlay.ModuleDeps{
		Factory:      factory.Factory(),
		BeefStorage:  beefStore,
		ChainTracker: chaintracker.ChainTracker(&spv.GullibleHeadersClient{}),
	})
	if err != nil {
		t.Fatal(err)
	}

	publisher := newParty(t, 0x11)
	identity, _ := ec.PrivateKeyFromHex("0000000000000000000000000000000000000000000000000000000000000002")
	lockKey := newParty(t, 0x13).key
	head := func(branch, root string) *script.Script {
		fields, err := gibtpl.Fields(testOrigin, branch, root, identity.PubKey())
		if err != nil {
			t.Fatal(err)
		}
		s, err := gibtpl.LockingScript(lockKey.PubKey(), fields, []byte(testCommit), "application/x-git-commit")
		if err != nil {
			t.Fatal(err)
		}
		return s
	}

	funding := fundingTx(t, publisher, 5000)
	prove(funding, 900000)

	// Mint both branches in one transaction: two heads at the same score,
	// separated only by vout, which is what the branch cursor must survive.
	mint := transaction.NewTransaction()
	mint.AddInputFromTx(funding, 0, publisher.unlock)
	mint.AddOutput(&transaction.TransactionOutput{Satoshis: 1, LockingScript: head("main", testRoot1)})
	mint.AddOutput(&transaction.TransactionOutput{Satoshis: 1, LockingScript: head("dev", testRoot1)})
	mint.AddOutput(&transaction.TransactionOutput{Satoshis: 4000, LockingScript: publisher.lock})
	if err := mint.Sign(); err != nil {
		t.Fatal(err)
	}
	prove(mint, 900001)
	submitTx(t, svc.Engine, mint)

	// Two pushes on main. Heights are explicit so push order is the score
	// order the store pages by.
	push1 := transaction.NewTransaction()
	push1.AddInputFromTx(mint, 0, nil)
	push1.AddInputFromTx(mint, 2, publisher.unlock)
	push1.AddOutput(&transaction.TransactionOutput{Satoshis: 1, LockingScript: head("main", testRoot2)})
	push1.AddOutput(&transaction.TransactionOutput{Satoshis: 3000, LockingScript: publisher.lock})
	if err := push1.Sign(); err != nil {
		t.Fatal(err)
	}
	signHeadInput(t, push1, 0, lockKey)
	prove(push1, 900002)
	submitTx(t, svc.Engine, push1)

	push2 := transaction.NewTransaction()
	push2.AddInputFromTx(push1, 0, nil)
	push2.AddInputFromTx(push1, 1, publisher.unlock)
	push2.AddOutput(&transaction.TransactionOutput{Satoshis: 1, LockingScript: head("main", testRoot1)})
	push2.AddOutput(&transaction.TransactionOutput{Satoshis: 2000, LockingScript: publisher.lock})
	if err := push2.Sign(); err != nil {
		t.Fatal(err)
	}
	signHeadInput(t, push2, 0, lockKey)
	prove(push2, 900003)
	submitTx(t, svc.Engine, push2)

	return &syncEnv{t: t, svc: svc, identity: identity.PubKey(), beefDir: beefDir,
		mint: mint, push1: push1, push2: push2}
}

func TestHeadsSinceLookup(t *testing.T) {
	e := newSyncEnv(t)
	base := `"origin":"` + testOrigin + `","branch":"main"`
	all := []string{op(e.mint, 0), op(e.push1, 0), op(e.push2, 0)}

	t.Run("whole branch oldest first", func(t *testing.T) {
		got, res := e.headsSince(`{"type":"headsSince",` + base + `}`)
		if len(got) != 3 || got[0] != all[0] || got[1] != all[1] || got[2] != all[2] {
			t.Fatalf("heads = %v, want %v", got, all)
		}
		if res.More || res.Code != "" {
			t.Fatalf("result = %+v", res)
		}
		if res.Origin != testOrigin || res.Branch != "main" {
			t.Fatalf("result = %+v", res)
		}
	})

	t.Run("since is exclusive", func(t *testing.T) {
		got, res := e.headsSince(`{"type":"headsSince",` + base + `,"since":"` + all[0] + `"}`)
		if len(got) != 2 || got[0] != all[1] || got[1] != all[2] {
			t.Fatalf("heads = %v, want %v", got, all[1:])
		}
		if res.Since != all[0] || res.Code != "" {
			t.Fatalf("result = %+v", res)
		}
	})

	t.Run("nothing new at the tip", func(t *testing.T) {
		got, res := e.headsSince(`{"type":"headsSince",` + base + `,"since":"` + all[2] + `"}`)
		if len(got) != 0 || res.More || res.Code != "" {
			t.Fatalf("heads = %v result = %+v", got, res)
		}
	})

	t.Run("unknown since is distinguishable", func(t *testing.T) {
		stranger := "0000000000000000000000000000000000000000000000000000000000000abc_0"
		got, res := e.headsSince(`{"type":"headsSince",` + base + `,"since":"` + stranger + `"}`)
		if len(got) != 0 || res.Code != CodeUnknownSince {
			t.Fatalf("heads = %v result = %+v", got, res)
		}
	})

	t.Run("since from another branch is unknown here", func(t *testing.T) {
		// The dev head exists, but not on main: answering with all of main
		// would hand the client heads it may already hold as new work.
		got, res := e.headsSince(`{"type":"headsSince",` + base + `,"since":"` + op(e.mint, 1) + `"}`)
		if len(got) != 0 || res.Code != CodeUnknownSince {
			t.Fatalf("heads = %v result = %+v", got, res)
		}
	})

	t.Run("branch filter", func(t *testing.T) {
		got, _ := e.headsSince(`{"type":"headsSince","origin":"` + testOrigin + `","branch":"dev"}`)
		if len(got) != 1 || got[0] != op(e.mint, 1) {
			t.Fatalf("dev heads = %v", got)
		}
	})

	t.Run("paging reports more", func(t *testing.T) {
		got, res := e.headsSince(`{"type":"headsSince",` + base + `,"limit":2}`)
		if len(got) != 2 || !res.More {
			t.Fatalf("heads = %v result = %+v", got, res)
		}
		// Resume from the last outpoint the page carried.
		rest, restRes := e.headsSince(`{"type":"headsSince",` + base + `,"since":"` + res.Outpoints[1] + `"}`)
		if len(rest) != 1 || rest[0] != all[2] || restRes.More {
			t.Fatalf("rest = %v result = %+v", rest, restRes)
		}
	})

	t.Run("identity filter", func(t *testing.T) {
		got, _ := e.headsSince(`{"type":"headsSince",` + base + `,"identity":"` + hexPub(e.identity) + `"}`)
		if len(got) != 3 {
			t.Fatalf("own heads = %v", got)
		}
		other, _ := ec.PrivateKeyFromHex("0000000000000000000000000000000000000000000000000000000000000009")
		none, res := e.headsSince(`{"type":"headsSince",` + base + `,"identity":"` + hexPub(other.PubKey()) + `"}`)
		if len(none) != 0 || res.Code != "" {
			t.Fatalf("stranger heads = %v result = %+v", none, res)
		}
		// A since that belongs to another identity is unknown under this filter.
		_, mismatch := e.headsSince(`{"type":"headsSince",` + base +
			`,"identity":"` + hexPub(other.PubKey()) + `","since":"` + all[0] + `"}`)
		if mismatch.Code != CodeUnknownSince {
			t.Fatalf("mismatched since result = %+v", mismatch)
		}
	})

	t.Run("bad input", func(t *testing.T) {
		for name, query := range map[string]string{
			"no origin":      `{"type":"headsSince","branch":"main"}`,
			"no branch":      `{"type":"headsSince","origin":"` + testOrigin + `"}`,
			"bad origin":     `{"type":"headsSince","origin":"nope","branch":"main"}`,
			"bad since":      `{"type":"headsSince",` + base + `,"since":"nope"}`,
			"bad identity":   `{"type":"headsSince",` + base + `,"identity":"xyz"}`,
			"unknown type":   `{"type":"nonsense"}`,
			"malformed json": `{"type":"headsSince","limit":"two"}`,
		} {
			if _, err := e.svc.Engine.Lookup(t.Context(), &overlaylookup.LookupQuestion{
				Service: LookupName, Query: json.RawMessage(query),
			}); err == nil {
				t.Fatalf("%s: expected an error", name)
			}
		}
	})
}

func TestTxsLookup(t *testing.T) {
	e := newSyncEnv(t)
	mint, push1 := e.mint.TxID().String(), e.push1.TxID().String()
	unknown := "0000000000000000000000000000000000000000000000000000000000000abc"

	txs := func(query string) ([]string, TxsResult) {
		t.Helper()
		ans := e.ask(query)
		var res TxsResult
		decodeResult(t, ans.Result, &res)
		got := []string{}
		for i, item := range ans.Outputs {
			_, _, txid, err := transaction.ParseBeef(item.Beef)
			if err != nil {
				t.Fatalf("output %d: parse BEEF: %v", i, err)
			}
			if item.OutputIndex != 0 {
				t.Fatalf("output %d: index = %d, want 0 for a whole transaction", i, item.OutputIndex)
			}
			got = append(got, txid.String())
		}
		if len(got) != len(res.Txids) {
			t.Fatalf("outputs %v and result txids %v are not aligned", got, res.Txids)
		}
		for i := range got {
			if got[i] != res.Txids[i] {
				t.Fatalf("output %d is %s, result says %s", i, got[i], res.Txids[i])
			}
		}
		return got, res
	}

	t.Run("returns whole transactions", func(t *testing.T) {
		got, res := txs(`{"type":"txs","txids":["` + mint + `","` + push1 + `"]}`)
		if len(got) != 2 || got[0] != mint || got[1] != push1 {
			t.Fatalf("txs = %v", got)
		}
		if len(res.Missing) != 0 {
			t.Fatalf("missing = %v", res.Missing)
		}
	})

	t.Run("deduplicates at the txid", func(t *testing.T) {
		got, _ := txs(`{"type":"txs","txids":["` + mint + `","` + mint + `","` + push1 + `","` + mint + `"]}`)
		if len(got) != 2 || got[0] != mint || got[1] != push1 {
			t.Fatalf("txs = %v, want the two distinct transactions once each", got)
		}
	})

	t.Run("unknown txids are reported not fatal", func(t *testing.T) {
		got, res := txs(`{"type":"txs","txids":["` + unknown + `","` + mint + `"]}`)
		if len(got) != 1 || got[0] != mint {
			t.Fatalf("txs = %v", got)
		}
		if len(res.Missing) != 1 || res.Missing[0] != unknown {
			t.Fatalf("missing = %v", res.Missing)
		}
	})

	t.Run("bad input", func(t *testing.T) {
		over := make([]string, MaxTxids+1)
		for i := range over {
			over[i] = mint
		}
		overJSON, _ := json.Marshal(over)
		for name, query := range map[string]string{
			"empty":      `{"type":"txs","txids":[]}`,
			"bad txid":   `{"type":"txs","txids":["nope"]}`,
			"over cap":   `{"type":"txs","txids":` + string(overJSON) + `}`,
			"wrong type": `{"type":"txs","txids":"` + mint + `"}`,
		} {
			if _, err := e.svc.Engine.Lookup(t.Context(), &overlaylookup.LookupQuestion{
				Service: LookupName, Query: json.RawMessage(query),
			}); err == nil {
				t.Fatalf("%s: expected an error", name)
			}
		}
	})
}

// TestFormulaHydrationStillRejectsNilTopic pins the defect the sync lookups
// are built around: engine.hydrateOneFormula calls FindOutput with a nil
// topic, which this stack's EngineAdapter refuses, so a formula answer never
// reaches a client. If this test starts failing the defect is fixed and the
// head query below could return formulas again.
func TestFormulaHydrationStillRejectsNilTopic(t *testing.T) {
	e := newSyncEnv(t)

	// The typeless head query still answers with formulas, and the engine
	// fails to hydrate them.
	_, err := e.svc.Engine.Lookup(t.Context(), &overlaylookup.LookupQuestion{
		Service: LookupName,
		Query:   json.RawMessage(`{"origin":"` + testOrigin + `"}`),
	})
	if err == nil {
		t.Fatal("formula hydration succeeded; the nil-topic defect is fixed")
	}
	if !strings.Contains(err.Error(), "topic is required") {
		t.Fatalf("formula hydration failed for another reason: %v", err)
	}

	// The lookup service itself does still produce the formulas: the break
	// is in hydration, not in the query.
	ans, err := e.svc.Lookup.Lookup(t.Context(), &overlaylookup.LookupQuestion{
		Service: LookupName,
		Query:   json.RawMessage(`{"origin":"` + testOrigin + `"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if ans.Type != overlaylookup.AnswerTypeFormula || len(ans.Formulas) == 0 {
		t.Fatalf("service answer = %+v", ans)
	}

	// The sync queries go through the same engine entry point and answer.
	if got, _ := e.headsSince(`{"type":"headsSince","origin":"` + testOrigin + `","branch":"main"}`); len(got) != 3 {
		t.Fatalf("headsSince through the engine = %v", got)
	}
}

// TestSyncLookupsWithoutBeefLoader covers a module wired without the shared
// BEEF store: the sync queries must refuse rather than answer empty, which a
// client would read as "nothing to fetch".
func TestSyncLookupsWithoutBeefLoader(t *testing.T) {
	f := newFixture(t)
	for name, query := range map[string]string{
		"headsSince": `{"type":"headsSince","origin":"` + testOrigin + `","branch":"main"}`,
		"txs":        `{"type":"txs","txids":["` + strings.Repeat("0", 63) + `1"]}`,
	} {
		if _, err := f.svc.Lookup(t.Context(), &overlaylookup.LookupQuestion{
			Service: LookupName, Query: json.RawMessage(query),
		}); err == nil {
			t.Fatalf("%s: expected an error without a BEEF loader", name)
		}
	}
}

// TestHeadsSinceStopsAtAMissingTransaction covers an indexed head whose
// transaction has left the BEEF store. Silently shortening the page would
// look like the branch tip; reporting more work without saying why would
// make the client page the same gap forever.
func TestHeadsSinceStopsAtAMissingTransaction(t *testing.T) {
	e := newSyncEnv(t)
	// Drop push1's BEEF, leaving the mint head readable and the two heads
	// after it unreachable.
	txid := e.push1.TxID().String()
	if err := os.Remove(filepath.Join(e.beefDir, txid[:2], txid+".beef")); err != nil {
		t.Fatal(err)
	}

	got, res := e.headsSince(`{"type":"headsSince","origin":"` + testOrigin + `","branch":"main"}`)
	if len(got) != 1 || got[0] != op(e.mint, 0) {
		t.Fatalf("heads = %v, want the page to stop before the gap", got)
	}
	if !res.More || res.Code != CodeMissingBeef {
		t.Fatalf("result = %+v", res)
	}

	// The same transaction is reported missing, not fatal, by the txs query.
	ans := e.ask(`{"type":"txs","txids":["` + txid + `","` + e.push2.TxID().String() + `"]}`)
	var txsRes TxsResult
	decodeResult(t, ans.Result, &txsRes)
	if len(ans.Outputs) != 1 || len(txsRes.Missing) != 1 || txsRes.Missing[0] != txid {
		t.Fatalf("outputs = %d result = %+v", len(ans.Outputs), txsRes)
	}
}

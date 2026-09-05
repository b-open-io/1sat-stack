package ecosystemalias

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/b-open-io/1sat-stack/pkg/beef"
	stackoverlay "github.com/b-open-io/1sat-stack/pkg/overlay"
	overlaystorage "github.com/b-open-io/1sat-stack/pkg/overlay/storage"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/gofiber/fiber/v2"
)

// Synthetic roots and a recording broadcaster are the only chain substitutes.
// Routes, SPV verification, topic/lookup, engine, SQLite, and disk BEEF are real.
// This does not establish mainnet custody, arcade delivery, or reorg recovery.
type lifecycleChain struct {
	roots      map[uint32]chainhash.Hash
	broadcasts []string
}

func (c *lifecycleChain) IsValidRootForHeight(_ context.Context, root *chainhash.Hash, height uint32) (bool, error) {
	expected, ok := c.roots[height]
	return ok && expected == *root, nil
}
func (c *lifecycleChain) CurrentHeight(context.Context) (uint32, error) { return 900000, nil }
func (c *lifecycleChain) Broadcast(tx *transaction.Transaction) (*transaction.BroadcastSuccess, *transaction.BroadcastFailure) {
	c.broadcasts = append(c.broadcasts, tx.TxID().String())
	return &transaction.BroadcastSuccess{Txid: tx.TxID().String()}, nil
}
func (c *lifecycleChain) BroadcastCtx(_ context.Context, tx *transaction.Transaction) (*transaction.BroadcastSuccess, *transaction.BroadcastFailure) {
	return c.Broadcast(tx)
}
func (c *lifecycleChain) prove(t *testing.T, tx *transaction.Transaction, height uint32) []byte {
	t.Helper()
	yes := true
	id := tx.TxID()
	tx.MerklePath = transaction.NewMerklePath(height, [][]*transaction.PathElement{{{Offset: 0, Hash: id, Txid: &yes}}})
	root, err := tx.MerklePath.ComputeRoot(id)
	if err != nil {
		t.Fatal(err)
	}
	c.roots[height] = *root
	bf := transaction.NewBeef()
	if _, err := bf.MergeTransaction(tx); err != nil {
		t.Fatal(err)
	}
	raw, err := bf.AtomicBytes(id)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

type lifecycleNode struct {
	app   *fiber.App
	svc   *Services
	close func()
}

func openLifecycleNode(t *testing.T, dir, prefix string, chain *lifecycleChain) *lifecycleNode {
	t.Helper()
	factory, err := overlaystorage.NewSQLiteFactory(filepath.Join(dir, "topics"))
	if err != nil {
		t.Fatal(err)
	}
	disk, err := beef.NewFilesystemBeefStorage(filepath.Join(dir, "beef"))
	if err != nil {
		t.Fatal(err)
	}
	beefStore := beef.NewStorageFromProviders([]beef.BaseBeefStorage{disk}, chain)
	cfg := Config{Mode: ModeEmbedded, Routes: RoutesConfig{Enabled: true, Prefix: prefix}}
	svc, err := cfg.Initialize(t.Context(), slog.New(slog.NewTextHandler(io.Discard, nil)), &stackoverlay.ModuleDeps{
		Factory: factory.Factory(), TxTopicIndex: factory.TxTopicIndex(), BeefStorage: beefStore, ChainTracker: chain, Broadcaster: chain, RoutesConfig: &stackoverlay.RoutesConfig{Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	svc.OverlayRoutes.Register(app.Group("/1sat"+prefix+"/overlay"), 4*1024*1024)
	var once sync.Once
	node := &lifecycleNode{app: app, svc: svc, close: func() {
		once.Do(func() { _ = app.Shutdown(); _ = svc.Close(); _ = factory.Close(); _ = beefStore.Close() })
	}}
	t.Cleanup(node.close)
	return node
}
func lifecycleRequest(t *testing.T, node *lifecycleNode, method, path, contentType string, body []byte) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	if contentType == "application/octet-stream" {
		req.Header.Set("X-Topics", TopicName)
	}
	res, err := node.app.Test(req, 5000)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, raw
}
func lifecycleLookup(t *testing.T, node *lifecycleNode, base, query string) []string {
	t.Helper()
	status, raw := lifecycleRequest(t, node, http.MethodPost, base+"/lookup", "application/json", []byte(`{"service":"ls_ecosystemalias","query":`+query+`}`))
	if status != 200 {
		t.Fatalf("lookup %s: HTTP %d: %s", query, status, raw)
	}
	var answer struct {
		Type    string `json:"type"`
		Outputs []struct {
			Beef  []byte `json:"beef"`
			Index uint32 `json:"outputIndex"`
		} `json:"outputs"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		t.Fatal(err)
	}
	if answer.Type != "output-list" {
		t.Fatalf("unexpected answer: %s", raw)
	}
	out := make([]string, 0, len(answer.Outputs))
	for _, item := range answer.Outputs {
		bf, id, err := transaction.NewBeefFromAtomicBytes(item.Beef)
		if err != nil {
			t.Fatalf("response BEEF: %v", err)
		}
		tx := bf.FindTransactionByHash(id)
		if tx == nil || int(item.Index) >= len(tx.Outputs) {
			t.Fatal("response outpoint missing from BEEF")
		}
		if _, err := Decode(tx.Outputs[item.Index].LockingScript, tx.Outputs[item.Index].Satoshis); err != nil {
			t.Fatalf("response claim: %v", err)
		}
		out = append(out, (&transaction.Outpoint{Txid: *id, Index: item.Index}).String())
	}
	return out
}
func TestHTTPLifecycleSubmitImportLookupAndReopen(t *testing.T) {
	for _, prefix := range []string{"/ecosystemalias", "/identity/aliases"} {
		t.Run(prefix, func(t *testing.T) {
			dir := t.TempDir()
			chain := &lifecycleChain{roots: map[uint32]chainhash.Hash{}}
			node := openLifecycleNode(t, dir, prefix, chain)
			base := "/1sat" + prefix + "/overlay"
			for _, listing := range []struct{ path, name string }{{"/listTopicManagers", TopicName}, {"/listLookupServiceProviders", LookupName}} {
				status, raw := lifecycleRequest(t, node, http.MethodGet, base+listing.path, "", nil)
				var names map[string]json.RawMessage
				if status != 200 || json.Unmarshal(raw, &names) != nil || names[listing.name] == nil {
					t.Fatalf("listing %s: %d %s", listing.path, status, raw)
				}
			}
			if got := lifecycleLookup(t, node, base, `{}`); len(got) != 0 {
				t.Fatalf("fresh node: %v", got)
			}
			owner := decodePrivateKey(t, "0000000000000000000000000000000000000000000000000000000000000002").PubKey().Compressed()
			first := transaction.NewTransaction()
			first.Outputs = []*transaction.TransactionOutput{{Satoshis: 1, LockingScript: topicSignedScript(t, "sigma", "sigma.example", owner)}}
			firstRaw := chain.prove(t, first, 800002)
			status, raw := lifecycleRequest(t, node, http.MethodPost, base+"/submit", "application/octet-stream", firstRaw)
			if status != 200 {
				t.Fatalf("submit: %d %s", status, raw)
			}
			var response struct {
				Steak overlay.Steak `json:"STEAK"`
			}
			if err := json.Unmarshal(raw, &response); err != nil {
				t.Fatal(err)
			}
			if response.Steak[TopicName] == nil || !reflect.DeepEqual(response.Steak[TopicName].OutputsToAdmit, []uint32{0}) {
				t.Fatalf("admission: %s", raw)
			}
			if !reflect.DeepEqual(chain.broadcasts, []string{first.TxID().String()}) {
				t.Fatalf("shared broadcaster not used: %v", chain.broadcasts)
			}
			// Historical import uses the engine's explicit historical mode, not /submit.
			second := transaction.NewTransaction()
			second.Outputs = []*transaction.TransactionOutput{{Satoshis: 1, LockingScript: topicSignedScript(t, "sigma", "other.example", owner)}}
			secondRaw := chain.prove(t, second, 800001)
			imported, err := node.svc.Engine.Submit(t.Context(), overlay.TaggedBEEF{Beef: secondRaw, Topics: []string{TopicName}}, engine.SubmitModeHistorical, nil)
			if err != nil || imported[TopicName] == nil || len(imported[TopicName].OutputsToAdmit) != 1 {
				t.Fatalf("historical import: %v %v", imported, err)
			}
			if len(chain.broadcasts) != 1 {
				t.Fatal("historical import broadcast")
			}
			firstOut := (&transaction.Outpoint{Txid: *first.TxID(), Index: 0}).String()
			secondOut := (&transaction.Outpoint{Txid: *second.TxID(), Index: 0}).String()
			for _, query := range []string{`{}`, `{"alias":"sigma"}`} {
				if got := lifecycleLookup(t, node, base, query); !reflect.DeepEqual(got, []string{secondOut, firstOut}) {
					t.Fatalf("ordered conflicts %s: %v", query, got)
				}
			}
			if got := lifecycleLookup(t, node, base, `{"domain":"sigma.example"}`); !reflect.DeepEqual(got, []string{firstOut}) {
				t.Fatalf("domain: %v", got)
			}
			if got := lifecycleLookup(t, node, base, `{"skip":1,"limit":1}`); !reflect.DeepEqual(got, []string{firstOut}) {
				t.Fatalf("page: %v", got)
			}
			for _, query := range []string{`{"findAll":true}`, `{"cursor":"ea1.old"}`, `{"skip":4294967296}`} {
				status, _ := lifecycleRequest(t, node, http.MethodPost, base+"/lookup", "application/json", []byte(`{"service":"ls_ecosystemalias","query":`+query+`}`))
				if status < 400 {
					t.Fatalf("invalid query accepted: %s", query)
				}
			}
			node.close()
			node = openLifecycleNode(t, dir, prefix, chain)
			if got := lifecycleLookup(t, node, base, `{}`); !reflect.DeepEqual(got, []string{secondOut, firstOut}) {
				t.Fatalf("reopened persistence: %v", got)
			}
			// A confirmed non-alias output is not admitted to this topic.
			invalid := transaction.NewTransaction()
			invalid.Outputs = []*transaction.TransactionOutput{{Satoshis: 1, LockingScript: &script.Script{script.OpTRUE}}}
			status, raw = lifecycleRequest(t, node, http.MethodPost, base+"/submit", "application/octet-stream", chain.prove(t, invalid, 800003))
			if status != 200 {
				t.Fatalf("non-alias submit: %d %s", status, raw)
			}
			if err := json.Unmarshal(raw, &response); err != nil {
				t.Fatal(err)
			}
			if response.Steak[TopicName] == nil || len(response.Steak[TopicName].OutputsToAdmit) != 0 {
				t.Fatalf("non-alias admitted: %s", raw)
			}
			if got := lifecycleLookup(t, node, base, `{}`); len(got) != 2 {
				t.Fatalf("non-alias changed claims: %v", got)
			}
			// A synthetic confirmed spend exercises real input membership and event eviction.
			spend := transaction.NewTransaction()
			spend.Inputs = []*transaction.TransactionInput{{SourceTXID: first.TxID(), SourceTxOutIndex: 0, SourceTransaction: first, UnlockingScript: &script.Script{}, SequenceNumber: 0xffffffff}}
			spend.Outputs = []*transaction.TransactionOutput{{Satoshis: 1, LockingScript: &script.Script{script.OpTRUE}}}
			status, raw = lifecycleRequest(t, node, http.MethodPost, base+"/submit", "application/octet-stream", chain.prove(t, spend, 800004))
			if status != 200 {
				t.Fatalf("spend: %d %s", status, raw)
			}
			if got := lifecycleLookup(t, node, base, `{}`); !reflect.DeepEqual(got, []string{secondOut}) {
				t.Fatalf("spent claim retained: %v", got)
			}
			if got := lifecycleLookup(t, node, base, `{"domain":"sigma.example"}`); len(got) != 0 {
				t.Fatalf("spent domain retained: %v", got)
			}
			node.close()
			node = openLifecycleNode(t, dir, prefix, chain)
			if got := lifecycleLookup(t, node, base, `{}`); !reflect.DeepEqual(got, []string{secondOut}) {
				t.Fatalf("spend lost on reopen: %v", got)
			}

		})
	}
}

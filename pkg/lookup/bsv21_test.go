package lookup

import (
	"context"
	"strconv"
	"testing"

	storage "github.com/b-open-io/1sat-stack/pkg/overlay/storage"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/b-open-io/1sat-stack/pkg/template/inscription"
	"github.com/b-open-io/1sat-stack/pkg/template/p2pkh"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

func newTestFactory(t *testing.T) storage.Factory {
	t.Helper()
	basePath := t.TempDir() + "/topic"
	f, err := storage.NewSQLiteFactory(basePath)
	if err != nil {
		t.Fatal(err)
	}
	return f.Factory()
}

func newTestLookup(t *testing.T) *BSV21Lookup {
	t.Helper()
	return NewBSV21Lookup(newTestFactory(t))
}

// insertTokenOutput inserts directly into a topic's token_outputs table for testing.
func insertTokenOutput(t *testing.T, lookup *BSV21Lookup, topic, tokenId, op, lockType, address string, amount uint64, score float64) *transaction.Outpoint {
	t.Helper()
	txid := &chainhash.Hash{}
	txid[0] = byte(score)
	outpoint := &transaction.Outpoint{Txid: *txid, Index: 0}

	ts, err := lookup.db(topic)
	if err != nil {
		t.Fatal(err)
	}

	_, err = ts.DB().Exec(
		`INSERT OR REPLACE INTO token_outputs (outpoint, token_id, op, lock_type, address, amount, score) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		outpoint.Bytes(), tokenId, op, lockType, address, strconv.FormatUint(amount, 10), score,
	)
	if err != nil {
		t.Fatal(err)
	}
	return outpoint
}

func TestNewBSV21Lookup(t *testing.T) {
	lookup := newTestLookup(t)
	if lookup == nil {
		t.Fatal("expected non-nil lookup")
	}
}

func TestGetBalance(t *testing.T) {
	lookup := newTestLookup(t)
	ctx := context.Background()
	tokenId := "abc123_0"
	topic := "tm_" + tokenId
	address := "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa"

	insertTokenOutput(t, lookup, topic, tokenId, "transfer", "p2pkh", address, 100, 1.0)
	insertTokenOutput(t, lookup, topic, tokenId, "transfer", "p2pkh", address, 250, 2.0)

	// Insert a spent output (should not count)
	spentOp := insertTokenOutput(t, lookup, topic, tokenId, "transfer", "p2pkh", address, 500, 3.0)
	ts, _ := lookup.db(topic)
	spendTxid := &chainhash.Hash{}
	spendTxid[0] = 0xFF
	ts.DB().Exec(`UPDATE token_outputs SET spend_txid = ? WHERE outpoint = ?`, spendTxid[:], spentOp.Bytes())

	balance, count, err := lookup.GetBalance(ctx, tokenId, "p2pkh", address)
	if err != nil {
		t.Fatal(err)
	}
	if balance != 350 {
		t.Errorf("expected balance 350, got %d", balance)
	}
	if count != 2 {
		t.Errorf("expected count 2, got %d", count)
	}
}

func TestGetMultiBalance(t *testing.T) {
	lookup := newTestLookup(t)
	ctx := context.Background()
	tokenId := "abc123_0"
	topic := "tm_" + tokenId
	addr1 := "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa"
	addr2 := "1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2"

	insertTokenOutput(t, lookup, topic, tokenId, "transfer", "p2pkh", addr1, 100, 1.0)
	insertTokenOutput(t, lookup, topic, tokenId, "transfer", "p2pkh", addr2, 200, 2.0)

	balance, count, err := lookup.GetMultiBalance(ctx, tokenId, "p2pkh", []string{addr1, addr2})
	if err != nil {
		t.Fatal(err)
	}
	if balance != 300 {
		t.Errorf("expected balance 300, got %d", balance)
	}
	if count != 2 {
		t.Errorf("expected count 2, got %d", count)
	}
}

func TestSearchUTXOs(t *testing.T) {
	lookup := newTestLookup(t)
	ctx := context.Background()
	tokenId := "abc123_0"
	topic := "tm_" + tokenId
	address := "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa"

	insertTokenOutput(t, lookup, topic, tokenId, "transfer", "p2pkh", address, 100, 1.0)
	spentOp := insertTokenOutput(t, lookup, topic, tokenId, "transfer", "p2pkh", address, 200, 2.0)
	insertTokenOutput(t, lookup, topic, tokenId, "transfer", "p2pkh", address, 300, 3.0)

	// Mark one as spent
	ts, _ := lookup.db(topic)
	spendTxid := &chainhash.Hash{}
	spendTxid[0] = 0xFF
	ts.DB().Exec(`UPDATE token_outputs SET spend_txid = ? WHERE outpoint = ?`, spendTxid[:], spentOp.Bytes())

	outpoints, err := lookup.SearchUTXOs(ctx, tokenId, "p2pkh", address, &store.SearchCfg{})
	if err != nil {
		t.Fatal(err)
	}
	if len(outpoints) != 2 {
		t.Errorf("expected 2 UTXOs, got %d", len(outpoints))
	}
}

func TestSearchHistory(t *testing.T) {
	lookup := newTestLookup(t)
	ctx := context.Background()
	tokenId := "abc123_0"
	topic := "tm_" + tokenId
	address := "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa"

	insertTokenOutput(t, lookup, topic, tokenId, "transfer", "p2pkh", address, 100, 1.0)
	spentOp := insertTokenOutput(t, lookup, topic, tokenId, "transfer", "p2pkh", address, 200, 2.0)
	insertTokenOutput(t, lookup, topic, tokenId, "transfer", "p2pkh", address, 300, 3.0)

	// Mark one as spent
	ts, _ := lookup.db(topic)
	spendTxid := &chainhash.Hash{}
	spendTxid[0] = 0xFF
	ts.DB().Exec(`UPDATE token_outputs SET spend_txid = ? WHERE outpoint = ?`, spendTxid[:], spentOp.Bytes())

	outpoints, err := lookup.SearchHistory(ctx, tokenId, "p2pkh", address, &store.SearchCfg{})
	if err != nil {
		t.Fatal(err)
	}
	if len(outpoints) != 3 {
		t.Errorf("expected 3 history entries, got %d", len(outpoints))
	}
}

func TestGetToken(t *testing.T) {
	lookup := newTestLookup(t)
	ctx := context.Background()

	txid := &chainhash.Hash{}
	txid[0] = 0x01
	op := &transaction.Outpoint{Txid: *txid, Index: 0}
	tokenId := op.OrdinalString()

	// Insert a deploy output into tm_bsv21 (discovery topic)
	ts, err := lookup.db("tm_bsv21")
	if err != nil {
		t.Fatal(err)
	}
	_, err = ts.DB().Exec(
		`INSERT INTO token_outputs (outpoint, token_id, op, lock_type, address, amount, sym, dec, icon, score) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		op.Bytes(), tokenId, "deploy+mint", "p2pkh", "1test", "1000000", "TEST", 8, "https://example.com/icon.png", 1.0,
	)
	if err != nil {
		t.Fatal(err)
	}

	token, err := lookup.GetToken(ctx, op)
	if err != nil {
		t.Fatal(err)
	}
	if token.Id != tokenId {
		t.Errorf("expected token id %s, got %s", tokenId, token.Id)
	}
	if token.Op != "deploy+mint" {
		t.Errorf("expected op deploy+mint, got %s", token.Op)
	}
	if token.Symbol == nil || *token.Symbol != "TEST" {
		t.Errorf("expected symbol TEST, got %v", token.Symbol)
	}
	if token.Decimals == nil || *token.Decimals != 8 {
		t.Errorf("expected decimals 8, got %v", token.Decimals)
	}
	if token.Icon == nil || *token.Icon != "https://example.com/icon.png" {
		t.Errorf("expected icon URL, got %v", token.Icon)
	}
	if token.Amt != 1000000 {
		t.Errorf("expected amt 1000000, got %d", token.Amt)
	}
}

func TestGetTokenResolvesRelativeIcon(t *testing.T) {
	lookup := newTestLookup(t)
	ctx := context.Background()

	txid := &chainhash.Hash{}
	txid[0] = 0x02
	op := &transaction.Outpoint{Txid: *txid, Index: 1}
	tokenId := op.OrdinalString()

	ts, err := lookup.db("tm_bsv21")
	if err != nil {
		t.Fatal(err)
	}
	_, err = ts.DB().Exec(
		`INSERT INTO token_outputs (outpoint, token_id, op, lock_type, address, amount, sym, dec, icon, score) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		op.Bytes(), tokenId, "deploy+mint", "p2pkh", "1test", "1000", "REL", 0, "_0", 1.0,
	)
	if err != nil {
		t.Fatal(err)
	}

	token, err := lookup.GetToken(ctx, op)
	if err != nil {
		t.Fatal(err)
	}
	want := txid.String() + "_0"
	if token.Icon == nil || *token.Icon != want {
		t.Errorf("icon = %v, want %q", token.Icon, want)
	}

	tokens, err := lookup.ListTokens(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 1 || tokens[0].Icon == nil || *tokens[0].Icon != want {
		t.Errorf("ListTokens icon = %v, want %q", tokens, want)
	}
}

func TestLoadOutputs(t *testing.T) {
	lookup := newTestLookup(t)
	ctx := context.Background()
	tokenId := "abc123_0"
	topic := "tm_" + tokenId

	op1 := insertTokenOutput(t, lookup, topic, tokenId, "transfer", "p2pkh", "addr1", 100, 1.0)
	op2 := insertTokenOutput(t, lookup, topic, tokenId, "transfer", "p2pkh", "addr2", 200, 2.0)

	outputs, err := lookup.LoadOutputs(ctx, tokenId, []*transaction.Outpoint{op1, op2})
	if err != nil {
		t.Fatal(err)
	}
	if len(outputs) != 2 {
		t.Fatalf("expected 2 outputs, got %d", len(outputs))
	}

	for _, out := range outputs {
		bsv21Data, ok := out.Data["bsv21"]
		if !ok {
			t.Error("expected bsv21 data in output")
			continue
		}
		dataMap := bsv21Data.(map[string]any)
		if dataMap["id"] != tokenId {
			t.Errorf("expected token id %s, got %s", tokenId, dataMap["id"])
		}
	}
}

func TestListTokens(t *testing.T) {
	lookup := newTestLookup(t)
	ctx := context.Background()

	// Insert deploy outputs into tm_bsv21
	insertTokenOutput(t, lookup, "tm_bsv21", "token1_0", "deploy+mint", "p2pkh", "addr1", 1000, 1.0)
	insertTokenOutput(t, lookup, "tm_bsv21", "token2_0", "deploy+mint", "p2pkh", "addr2", 2000, 2.0)

	tokens, err := lookup.ListTokens(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 2 {
		t.Errorf("expected 2 tokens, got %d", len(tokens))
	}
}

func TestCountOutputs(t *testing.T) {
	lookup := newTestLookup(t)
	ctx := context.Background()
	tokenId := "abc123_0"
	topic := "tm_" + tokenId

	insertTokenOutput(t, lookup, topic, tokenId, "transfer", "p2pkh", "addr1", 100, 1.0)
	spentOp := insertTokenOutput(t, lookup, topic, tokenId, "transfer", "p2pkh", "addr2", 200, 2.0)
	insertTokenOutput(t, lookup, topic, tokenId, "transfer", "p2pkh", "addr3", 300, 3.0)

	// Spending an output must not reduce the count - the fee for indexing it
	// was already charged and is not refunded.
	ts, _ := lookup.db(topic)
	spendTxid := &chainhash.Hash{}
	spendTxid[0] = 0xFF
	ts.DB().Exec(`UPDATE token_outputs SET spend_txid = ? WHERE outpoint = ?`, spendTxid[:], spentOp.Bytes())

	count, err := lookup.CountOutputs(ctx, topic)
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Errorf("expected 3 outputs, got %d", count)
	}
}

func TestLargeUint64Amount(t *testing.T) {
	lookup := newTestLookup(t)
	ctx := context.Background()
	tokenId := "mnee_0"
	topic := "tm_" + tokenId

	// MNEE-scale amount: high bit set, exceeds int64 max
	var largeAmt uint64 = 18446744073709500000
	insertTokenOutput(t, lookup, topic, tokenId, "transfer", "p2pkh", "addr1", largeAmt, 1.0)
	insertTokenOutput(t, lookup, topic, tokenId, "transfer", "p2pkh", "addr1", 500000, 2.0)

	balance, count, err := lookup.GetBalance(ctx, tokenId, "p2pkh", "addr1")
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("expected 2 utxos, got %d", count)
	}
	if balance != largeAmt+500000 {
		t.Errorf("expected balance %d, got %d", largeAmt+500000, balance)
	}

	// Verify LoadOutputs round-trips correctly
	outputs, err := lookup.LoadOutputs(ctx, tokenId, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Verify via SearchUTXOs + LoadOutputs
	outpoints, err := lookup.SearchUTXOs(ctx, tokenId, "p2pkh", "addr1", &store.SearchCfg{})
	if err != nil {
		t.Fatal(err)
	}
	outputs, err = lookup.LoadOutputs(ctx, tokenId, outpoints)
	if err != nil {
		t.Fatal(err)
	}
	if len(outputs) != 2 {
		t.Fatalf("expected 2 outputs, got %d", len(outputs))
	}
	for _, out := range outputs {
		bsv21Data := out.Data["bsv21"].(map[string]any)
		amt := bsv21Data["amt"].(string)
		if amt != "18446744073709500000" && amt != "500000" {
			t.Errorf("unexpected amount: %s", amt)
		}
	}
}

// admitDeploy builds a deploy+mint tx with the given script layout and runs it
// through OutputAdmittedByTopic on the tm_bsv21 topic.
func admitDeploy(t *testing.T, lookup *BSV21Lookup, prefix, suffix []byte) *transaction.Outpoint {
	t.Helper()
	return admitDeployJSON(t, lookup, `{"p":"bsv-20","op":"deploy+mint","sym":"TEST","amt":"1000"}`, prefix, suffix)
}

func admitDeployJSON(t *testing.T, lookup *BSV21Lookup, content string, prefix, suffix []byte) *transaction.Outpoint {
	t.Helper()
	scr, err := (&inscription.Inscription{
		File: inscription.File{
			Content: []byte(content),
			Type:    "application/bsv-20",
		},
		ScriptPrefix: prefix,
		ScriptSuffix: suffix,
	}).Lock()
	if err != nil {
		t.Fatal(err)
	}

	tx := transaction.NewTransaction()
	tx.AddOutput(&transaction.TransactionOutput{LockingScript: scr, Satoshis: 1})
	beef, err := tx.AtomicBEEF(true)
	if err != nil {
		t.Fatal(err)
	}

	err = lookup.OutputAdmittedByTopic(context.Background(), &engine.OutputAdmittedByTopic{
		Topic:       "tm_bsv21",
		OutputIndex: 0,
		AtomicBEEF:  beef,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &transaction.Outpoint{Txid: *tx.TxID(), Index: 0}
}

func TestOutputAdmittedByTopicLockLayouts(t *testing.T) {
	addr, err := script.NewAddressFromPublicKeyHash(make([]byte, 20), true)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := p2pkh.Lock(addr)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("p2pkh before envelope", func(t *testing.T) {
		lookup := newTestLookup(t)
		outpoint := admitDeploy(t, lookup, *lock, nil)

		token, err := lookup.GetToken(context.Background(), outpoint)
		if err != nil {
			t.Fatalf("expected token to be indexed, got %v", err)
		}
		if token.Id != outpoint.OrdinalString() {
			t.Errorf("expected token id %s, got %s", outpoint.OrdinalString(), token.Id)
		}

		ts, err := lookup.db("tm_bsv21")
		if err != nil {
			t.Fatal(err)
		}
		var lockType, address string
		if err := ts.DB().QueryRow(
			`SELECT lock_type, address FROM token_outputs WHERE outpoint = ?`, outpoint.Bytes(),
		).Scan(&lockType, &address); err != nil {
			t.Fatal(err)
		}
		if lockType != "" || address != "" {
			t.Errorf("expected empty lock_type/address, got %q/%q", lockType, address)
		}
	})

	t.Run("p2pkh after envelope", func(t *testing.T) {
		lookup := newTestLookup(t)
		outpoint := admitDeploy(t, lookup, nil, *lock)

		if _, err := lookup.GetToken(context.Background(), outpoint); err != nil {
			t.Fatalf("expected token to be indexed, got %v", err)
		}

		ts, err := lookup.db("tm_bsv21")
		if err != nil {
			t.Fatal(err)
		}
		var lockType, address string
		if err := ts.DB().QueryRow(
			`SELECT lock_type, address FROM token_outputs WHERE outpoint = ?`, outpoint.Bytes(),
		).Scan(&lockType, &address); err != nil {
			t.Fatal(err)
		}
		if lockType != "p2pkh" || address != addr.AddressString {
			t.Errorf("expected p2pkh/%s, got %q/%q", addr.AddressString, lockType, address)
		}
	})
}

func TestOutputAdmittedByTopicResolvesRelativeIcon(t *testing.T) {
	lookup := newTestLookup(t)
	outpoint := admitDeployJSON(t, lookup,
		`{"p":"bsv-20","op":"deploy+mint","sym":"TEST","amt":"1000","icon":"_0"}`,
		nil, nil,
	)

	token, err := lookup.GetToken(context.Background(), outpoint)
	if err != nil {
		t.Fatalf("GetToken: %v", err)
	}
	want := outpoint.Txid.String() + "_0"
	if token.Icon == nil || *token.Icon != want {
		t.Errorf("icon = %v, want %q", token.Icon, want)
	}
}

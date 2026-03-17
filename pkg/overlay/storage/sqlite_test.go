package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

func testDB(t *testing.T) *SQLiteStorage {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := NewSQLiteStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func testOutpoint(t *testing.T, hex string, index uint32) *transaction.Outpoint {
	t.Helper()
	h, err := chainhash.NewHashFromHex(hex)
	if err != nil {
		t.Fatal(err)
	}
	return &transaction.Outpoint{Txid: *h, Index: index}
}

var testTxid1 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
var testTxid2 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func TestInsertAndGetOutput(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()

	op := testOutpoint(t, testTxid1, 0)
	txid, _ := chainhash.NewHashFromHex(testTxid1)

	if err := s.InsertOutput(ctx, op, txid, 1000, nil, nil, 1.5); err != nil {
		t.Fatal(err)
	}

	rec, err := s.GetOutput(ctx, op)
	if err != nil {
		t.Fatal(err)
	}
	if rec == nil {
		t.Fatal("expected output, got nil")
	}
	if rec.Satoshis != 1000 {
		t.Errorf("satoshis: got %d, want 1000", rec.Satoshis)
	}
	if rec.Score != 1.5 {
		t.Errorf("score: got %f, want 1.5", rec.Score)
	}
	if rec.SpendTxid != nil {
		t.Error("expected unspent")
	}
}

func TestGetOutputNotFound(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()

	op := testOutpoint(t, testTxid1, 99)
	rec, err := s.GetOutput(ctx, op)
	if err != nil {
		t.Fatal(err)
	}
	if rec != nil {
		t.Error("expected nil for missing output")
	}
}

func TestMarkSpent(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()

	op := testOutpoint(t, testTxid1, 0)
	txid, _ := chainhash.NewHashFromHex(testTxid1)
	spendTxid, _ := chainhash.NewHashFromHex(testTxid2)

	s.InsertOutput(ctx, op, txid, 500, nil, nil, 1.0)
	if err := s.MarkSpent(ctx, op, spendTxid); err != nil {
		t.Fatal(err)
	}

	rec, _ := s.GetOutput(ctx, op)
	if rec.SpendTxid == nil {
		t.Fatal("expected spent")
	}
	if !rec.SpendTxid.IsEqual(spendTxid) {
		t.Errorf("spend txid mismatch")
	}
}

func TestFindUTXOs(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()

	txid, _ := chainhash.NewHashFromHex(testTxid1)
	spendTxid, _ := chainhash.NewHashFromHex(testTxid2)

	// Insert 3 outputs, spend the second
	for i := uint32(0); i < 3; i++ {
		op := testOutpoint(t, testTxid1, i)
		s.InsertOutput(ctx, op, txid, 100*uint64(i+1), nil, nil, float64(i+1))
	}
	s.MarkSpent(ctx, testOutpoint(t, testTxid1, 1), spendTxid)

	utxos, err := s.FindUTXOs(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(utxos) != 2 {
		t.Fatalf("expected 2 UTXOs, got %d", len(utxos))
	}

	// With since filter
	utxos, err = s.FindUTXOs(ctx, &QueryOpts{Since: 1.0})
	if err != nil {
		t.Fatal(err)
	}
	if len(utxos) != 1 {
		t.Fatalf("expected 1 UTXO after since filter, got %d", len(utxos))
	}

	// With limit
	utxos, err = s.FindUTXOs(ctx, &QueryOpts{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(utxos) != 1 {
		t.Fatalf("expected 1 UTXO with limit, got %d", len(utxos))
	}
}

func TestUpdateConsumedBy(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()

	op := testOutpoint(t, testTxid1, 0)
	txid, _ := chainhash.NewHashFromHex(testTxid1)
	s.InsertOutput(ctx, op, txid, 100, nil, nil, 1.0)

	consumedBy := []byte("fake-consumed-by-data-36-bytes!!")
	if err := s.UpdateConsumedBy(ctx, op, consumedBy); err != nil {
		t.Fatal(err)
	}

	rec, _ := s.GetOutput(ctx, op)
	if string(rec.ConsumedBy) != string(consumedBy) {
		t.Error("consumed_by mismatch")
	}
}

func TestFindOutputsForTransaction(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()

	txid, _ := chainhash.NewHashFromHex(testTxid1)
	for i := uint32(0); i < 3; i++ {
		op := testOutpoint(t, testTxid1, i)
		s.InsertOutput(ctx, op, txid, 100, nil, nil, 1.0)
	}

	outputs, err := s.FindOutputsForTransaction(ctx, txid)
	if err != nil {
		t.Fatal(err)
	}
	if len(outputs) != 3 {
		t.Fatalf("expected 3, got %d", len(outputs))
	}
}

func TestRollback(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()

	txid, _ := chainhash.NewHashFromHex(testTxid1)
	for i := uint32(0); i < 3; i++ {
		op := testOutpoint(t, testTxid1, i)
		s.InsertOutput(ctx, op, txid, 100, nil, nil, 1.0)
	}

	if err := s.Rollback(ctx, txid); err != nil {
		t.Fatal(err)
	}

	outputs, _ := s.FindOutputsForTransaction(ctx, txid)
	if len(outputs) != 0 {
		t.Fatalf("expected 0 after rollback, got %d", len(outputs))
	}
}

func TestDeleteOutput(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()

	op := testOutpoint(t, testTxid1, 0)
	txid, _ := chainhash.NewHashFromHex(testTxid1)
	s.InsertOutput(ctx, op, txid, 100, nil, nil, 1.0)
	s.SaveEvent(ctx, "test:event", op, 1.0)

	if err := s.DeleteOutput(ctx, op); err != nil {
		t.Fatal(err)
	}

	rec, _ := s.GetOutput(ctx, op)
	if rec != nil {
		t.Error("expected nil after delete")
	}

	// Events should also be deleted
	results, _ := s.FindByEvent(ctx, "test:event", nil)
	if len(results) != 0 {
		t.Error("expected events cleaned up")
	}
}

func TestAppliedTx(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()

	txid, _ := chainhash.NewHashFromHex(testTxid1)

	exists, err := s.HasAppliedTx(ctx, txid)
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("should not exist yet")
	}

	if err := s.InsertAppliedTx(ctx, txid, 1.0); err != nil {
		t.Fatal(err)
	}

	exists, _ = s.HasAppliedTx(ctx, txid)
	if !exists {
		t.Error("should exist after insert")
	}

	// Insert again — should not error (INSERT OR IGNORE)
	if err := s.InsertAppliedTx(ctx, txid, 2.0); err != nil {
		t.Fatal(err)
	}
}

func TestPeerInteractions(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()

	since, err := s.GetLastInteraction(ctx, "peer1.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if since != 0 {
		t.Errorf("expected 0 for unknown host, got %f", since)
	}

	if err := s.UpdateLastInteraction(ctx, "peer1.example.com", 1234567890.5); err != nil {
		t.Fatal(err)
	}

	since, _ = s.GetLastInteraction(ctx, "peer1.example.com")
	if since != 1234567890.5 {
		t.Errorf("expected 1234567890.5, got %f", since)
	}

	// Update overwrites
	s.UpdateLastInteraction(ctx, "peer1.example.com", 9999999999.0)
	since, _ = s.GetLastInteraction(ctx, "peer1.example.com")
	if since != 9999999999.0 {
		t.Errorf("expected updated value, got %f", since)
	}
}

func TestEvents(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()

	txid, _ := chainhash.NewHashFromHex(testTxid1)
	op1 := testOutpoint(t, testTxid1, 0)
	op2 := testOutpoint(t, testTxid1, 1)
	s.InsertOutput(ctx, op1, txid, 100, nil, nil, 1.0)
	s.InsertOutput(ctx, op2, txid, 200, nil, nil, 2.0)

	s.SaveEvent(ctx, "name:example.bsv", op1, 1.0)
	s.SaveEvent(ctx, "name:example.bsv", op2, 2.0)
	s.SaveEvent(ctx, "name:other.bsv", op1, 1.0)

	results, err := s.FindByEvent(ctx, "name:example.bsv", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Since filter
	results, _ = s.FindByEvent(ctx, "name:example.bsv", &QueryOpts{Since: 1.0})
	if len(results) != 1 {
		t.Fatalf("expected 1 result with since, got %d", len(results))
	}

	// Delete event
	s.DeleteEvent(ctx, "name:example.bsv", op1)
	results, _ = s.FindByEvent(ctx, "name:example.bsv", nil)
	if len(results) != 1 {
		t.Fatalf("expected 1 after delete, got %d", len(results))
	}
}

func TestFactory(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "overlay")
	f, err := NewSQLiteFactory(basePath)
	if err != nil {
		t.Fatal(err)
	}
	factory := f.Factory()

	s1, err := factory("topic_a")
	if err != nil {
		t.Fatal(err)
	}
	defer s1.Close()

	s2, err := factory("topic_b")
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	// Same topic returns same instance
	s1Again, err := factory("topic_a")
	if err != nil {
		t.Fatal(err)
	}
	if s1 != s1Again {
		t.Error("expected same instance for same topic")
	}

	// Verify separate databases
	dbFileA := filepath.Join(dir, "overlay", "topic_a.db")
	dbFileB := filepath.Join(dir, "overlay", "topic_b.db")
	if _, err := os.Stat(dbFileA); err != nil {
		t.Errorf("expected DB file for topic_a: %v", err)
	}
	if _, err := os.Stat(dbFileB); err != nil {
		t.Errorf("expected DB file for topic_b: %v", err)
	}
}

func TestFindOutputs(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()

	txid, _ := chainhash.NewHashFromHex(testTxid1)
	op1 := testOutpoint(t, testTxid1, 0)
	op2 := testOutpoint(t, testTxid1, 1)
	op3 := testOutpoint(t, testTxid1, 2)
	s.InsertOutput(ctx, op1, txid, 100, nil, nil, 1.0)
	s.InsertOutput(ctx, op2, txid, 200, nil, nil, 2.0)

	results, err := s.FindOutputs(ctx, []*transaction.Outpoint{op1, op2, op3})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 found, got %d", len(results))
	}
}

func TestDepsAndInputsConsumed(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()

	op := testOutpoint(t, testTxid1, 0)
	txid, _ := chainhash.NewHashFromHex(testTxid1)
	deps := []byte("deps-data-32-bytes-of-hash!!!!!")
	inputs := []byte("inputs-consumed-36-bytes-data!!!")

	s.InsertOutput(ctx, op, txid, 100, deps, inputs, 1.0)

	rec, _ := s.GetOutput(ctx, op)
	if string(rec.Deps) != string(deps) {
		t.Error("deps mismatch")
	}
	if string(rec.InputsConsumed) != string(inputs) {
		t.Error("inputs_consumed mismatch")
	}
}

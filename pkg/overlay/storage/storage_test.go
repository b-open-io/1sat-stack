package storage

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

type testBackend struct {
	name    string
	factory func(t *testing.T) (TopicStorage, func())
}

func backends(t *testing.T) []testBackend {
	t.Helper()
	bs := []testBackend{
		{name: "sqlite", factory: newSQLiteTestStorage},
	}
	if os.Getenv("SKIP_POSTGRES_TESTS") != "1" {
		bs = append(bs, testBackend{name: "postgres", factory: newPostgresTestStorage})
	}
	return bs
}

func newSQLiteTestStorage(t *testing.T) (TopicStorage, func()) {
	t.Helper()
	dir := t.TempDir()
	s, err := NewSQLiteStorage(fmt.Sprintf("%s/test.db", dir))
	if err != nil {
		t.Fatal(err)
	}
	return s, func() { s.Close() }
}

func newPostgresTestStorage(t *testing.T) (TopicStorage, func()) {
	t.Helper()
	ctx := context.Background()

	container, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("test_overlay"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("5432/tcp")),
	)
	if err != nil {
		t.Skipf("failed to start postgres container: %v", err)
	}

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		container.Terminate(ctx)
		t.Fatal(err)
	}

	factory, err := NewPostgresFactory(connStr)
	if err != nil {
		container.Terminate(ctx)
		t.Fatal(err)
	}

	ts, err := factory.Topic("test_topic")
	if err != nil {
		factory.Close()
		container.Terminate(ctx)
		t.Fatal(err)
	}

	return ts, func() {
		factory.Close()
		container.Terminate(ctx)
	}
}

func makeOutpoint(txidByte byte, index uint32) *transaction.Outpoint {
	var h chainhash.Hash
	h[0] = txidByte
	return &transaction.Outpoint{Txid: h, Index: index}
}

func makeTxid(b byte) *chainhash.Hash {
	var h chainhash.Hash
	h[0] = b
	return &h
}

func TestTopicStorage_InsertAndGet(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ts, cleanup := b.factory(t)
			defer cleanup()
			ctx := context.Background()

			op := makeOutpoint(0x01, 0)
			txid := makeTxid(0x01)

			if err := ts.InsertOutput(ctx, op, txid, 100, nil, nil, 1.0); err != nil {
				t.Fatal(err)
			}

			rec, err := ts.GetOutput(ctx, op)
			if err != nil {
				t.Fatal(err)
			}
			if rec == nil {
				t.Fatal("expected record, got nil")
			}
			if rec.Satoshis != 100 {
				t.Errorf("satoshis = %d, want 100", rec.Satoshis)
			}
			if rec.Score != 1.0 {
				t.Errorf("score = %f, want 1.0", rec.Score)
			}
			if rec.SpendTxid != nil {
				t.Error("expected nil spend_txid")
			}
		})
	}
}

func TestTopicStorage_FindOutputs(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ts, cleanup := b.factory(t)
			defer cleanup()
			ctx := context.Background()

			ops := []*transaction.Outpoint{
				makeOutpoint(0x01, 0),
				makeOutpoint(0x02, 0),
				makeOutpoint(0x03, 0),
			}
			for i, op := range ops {
				txid := makeTxid(byte(i + 1))
				if err := ts.InsertOutput(ctx, op, txid, uint64(i*100), nil, nil, float64(i)); err != nil {
					t.Fatal(err)
				}
			}

			found, err := ts.FindOutputs(ctx, ops[:2])
			if err != nil {
				t.Fatal(err)
			}
			if len(found) != 2 {
				t.Errorf("found %d outputs, want 2", len(found))
			}
		})
	}
}

func TestTopicStorage_MarkSpent(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ts, cleanup := b.factory(t)
			defer cleanup()
			ctx := context.Background()

			op := makeOutpoint(0x01, 0)
			txid := makeTxid(0x01)
			spendTxid := makeTxid(0x02)

			if err := ts.InsertOutput(ctx, op, txid, 1, nil, nil, 1.0); err != nil {
				t.Fatal(err)
			}
			if err := ts.MarkSpent(ctx, op, spendTxid); err != nil {
				t.Fatal(err)
			}

			rec, err := ts.GetOutput(ctx, op)
			if err != nil {
				t.Fatal(err)
			}
			if rec.SpendTxid == nil {
				t.Fatal("expected spend_txid to be set")
			}
			if *rec.SpendTxid != *spendTxid {
				t.Errorf("spend_txid = %s, want %s", rec.SpendTxid, spendTxid)
			}
		})
	}
}

func TestTopicStorage_FindUTXOs(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ts, cleanup := b.factory(t)
			defer cleanup()
			ctx := context.Background()

			// Insert 3 outputs, spend one
			for i := byte(1); i <= 3; i++ {
				op := makeOutpoint(i, 0)
				if err := ts.InsertOutput(ctx, op, makeTxid(i), 1, nil, nil, float64(i)); err != nil {
					t.Fatal(err)
				}
			}
			if err := ts.MarkSpent(ctx, makeOutpoint(2, 0), makeTxid(0xFF)); err != nil {
				t.Fatal(err)
			}

			utxos, err := ts.FindUTXOs(ctx, &QueryOpts{})
			if err != nil {
				t.Fatal(err)
			}
			if len(utxos) != 2 {
				t.Errorf("utxos = %d, want 2", len(utxos))
			}

			// Test with limit
			utxos, err = ts.FindUTXOs(ctx, &QueryOpts{Limit: 1})
			if err != nil {
				t.Fatal(err)
			}
			if len(utxos) != 1 {
				t.Errorf("utxos = %d, want 1", len(utxos))
			}
		})
	}
}

func TestTopicStorage_Rollback(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ts, cleanup := b.factory(t)
			defer cleanup()
			ctx := context.Background()

			txid := makeTxid(0x01)
			op1 := makeOutpoint(0x01, 0)
			op2 := makeOutpoint(0x01, 1)

			if err := ts.InsertOutput(ctx, op1, txid, 1, nil, nil, 1.0); err != nil {
				t.Fatal(err)
			}
			if err := ts.InsertOutput(ctx, op2, txid, 1, nil, nil, 1.0); err != nil {
				t.Fatal(err)
			}

			if err := ts.Rollback(ctx, txid); err != nil {
				t.Fatal(err)
			}

			recs, err := ts.FindOutputsForTransaction(ctx, txid)
			if err != nil {
				t.Fatal(err)
			}
			if len(recs) != 0 {
				t.Errorf("expected 0 outputs after rollback, got %d", len(recs))
			}
		})
	}
}

func TestReversibleTopicStorage_RestoresRecursiveAncestryAndFinalizes(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			topicStorage, cleanup := b.factory(t)
			defer cleanup()
			storage, ok := topicStorage.(ReversibleTopicStorage)
			if !ok {
				t.Fatal("backend does not implement reversible storage")
			}
			ctx := context.Background()
			grandparent := makeOutpoint(0x51, 0)
			parent := makeOutpoint(0x52, 0)
			direct := makeOutpoint(0x53, 0)
			mutationTxid := makeTxid(0x54)
			created := makeOutpoint(0x54, 0)

			if err := storage.InsertOutput(ctx, grandparent, &grandparent.Txid, 11, []byte("g-deps"), nil, 1); err != nil {
				t.Fatal(err)
			}
			if err := storage.MarkSpent(ctx, grandparent, &parent.Txid); err != nil {
				t.Fatal(err)
			}
			if err := storage.UpdateConsumedBy(ctx, grandparent, parent.Bytes()); err != nil {
				t.Fatal(err)
			}
			if err := storage.SaveEvent(ctx, "grandparent", grandparent, 1); err != nil {
				t.Fatal(err)
			}
			if err := storage.InsertOutput(ctx, parent, &parent.Txid, 12, []byte("p-deps"), grandparent.Bytes(), 2); err != nil {
				t.Fatal(err)
			}
			if err := storage.MarkSpent(ctx, parent, &direct.Txid); err != nil {
				t.Fatal(err)
			}
			if err := storage.UpdateConsumedBy(ctx, parent, direct.Bytes()); err != nil {
				t.Fatal(err)
			}
			if err := storage.SaveEvent(ctx, "parent", parent, 2); err != nil {
				t.Fatal(err)
			}
			if err := storage.InsertOutput(ctx, direct, &direct.Txid, 13, []byte("d-deps"), parent.Bytes(), 3); err != nil {
				t.Fatal(err)
			}
			if err := storage.SaveEvent(ctx, "direct", direct, 3); err != nil {
				t.Fatal(err)
			}

			if err := storage.BeginMutation(ctx, mutationTxid, []*transaction.Outpoint{direct}); err != nil {
				t.Fatal(err)
			}
			if err := storage.DeleteOutput(ctx, direct); err != nil {
				t.Fatal(err)
			}
			if err := storage.UpdateConsumedBy(ctx, parent, nil); err != nil {
				t.Fatal(err)
			}
			if err := storage.DeleteOutput(ctx, parent); err != nil {
				t.Fatal(err)
			}
			if err := storage.UpdateConsumedBy(ctx, grandparent, nil); err != nil {
				t.Fatal(err)
			}
			if err := storage.DeleteOutput(ctx, grandparent); err != nil {
				t.Fatal(err)
			}
			if err := storage.InsertOutput(ctx, created, mutationTxid, 36, nil, nil, 4); err != nil {
				t.Fatal(err)
			}
			if err := storage.SaveEvent(ctx, "created", created, 4); err != nil {
				t.Fatal(err)
			}
			if err := storage.CommitMutation(ctx, mutationTxid, 4); err != nil {
				t.Fatal(err)
			}

			result, err := storage.RollbackMutation(ctx, mutationTxid)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Evicted) != 1 || result.Evicted[0] != *created {
				t.Fatalf("evicted = %+v, want %s", result.Evicted, created)
			}
			if len(result.Restored) != 3 {
				t.Fatalf("restored %d outputs, want 3", len(result.Restored))
			}
			assertOutputBeforeImage(t, storage, grandparent, &parent.Txid, parent.Bytes(), []byte("g-deps"))
			assertOutputBeforeImage(t, storage, parent, &direct.Txid, direct.Bytes(), []byte("p-deps"))
			assertOutputBeforeImage(t, storage, direct, nil, nil, []byte("d-deps"))
			for event := range map[string]struct{}{"grandparent": {}, "parent": {}, "direct": {}} {
				outputs, err := storage.FindByEvent(ctx, event, nil)
				if err != nil || len(outputs) != 1 {
					t.Fatalf("event %q was not restored: outputs=%v err=%v", event, outputs, err)
				}
			}
			if outputs, err := storage.FindByEvent(ctx, "created", nil); err != nil || len(outputs) != 0 {
				t.Fatalf("created event survived rollback: outputs=%v err=%v", outputs, err)
			}
			if guarded, err := storage.HasMutationGuard(ctx, mutationTxid); err != nil || !guarded {
				t.Fatalf("guard before finalization: guarded=%v err=%v", guarded, err)
			}
			if mutation, err := storage.GetMutation(ctx, mutationTxid); err != nil || mutation == nil || mutation.Phase != MutationPhaseRollbackPending {
				t.Fatalf("rollback phase: mutation=%+v err=%v", mutation, err)
			}
			if applied, err := storage.HasAppliedTx(ctx, mutationTxid); err != nil || !applied {
				t.Fatalf("applied marker before finalization: applied=%v err=%v", applied, err)
			}
			retry, err := storage.RollbackMutation(ctx, mutationTxid)
			if err != nil || len(retry.Evicted) != 1 || len(retry.Restored) != 3 {
				t.Fatalf("idempotent rollback result=%+v err=%v", retry, err)
			}
			if err := storage.FinalizeRollback(ctx, mutationTxid); err != nil {
				t.Fatal(err)
			}
			if err := storage.FinalizeRollback(ctx, mutationTxid); err != nil {
				t.Fatalf("idempotent finalization: %v", err)
			}
			if guarded, err := storage.HasMutationGuard(ctx, mutationTxid); err != nil || guarded {
				t.Fatalf("guard after finalization: guarded=%v err=%v", guarded, err)
			}
			if applied, err := storage.HasAppliedTx(ctx, mutationTxid); err != nil || applied {
				t.Fatalf("applied marker after finalization: applied=%v err=%v", applied, err)
			}
			if err := storage.BeginMutation(ctx, mutationTxid, []*transaction.Outpoint{direct}); err != nil {
				t.Fatalf("replay after finalization: %v", err)
			}
		})
	}
}

func TestReversibleTopicStorage_EnumeratesAndPrunesAppliedMutations(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			topicStorage, cleanup := b.factory(t)
			defer cleanup()
			storage := topicStorage.(ReversibleTopicStorage)
			ctx := context.Background()
			txid := makeTxid(0x59)
			if mutations, err := storage.ListMutations(ctx); err != nil || len(mutations) != 0 {
				t.Fatalf("dormant mutation tables: mutations=%v err=%v", mutations, err)
			}
			if err := storage.BeginMutation(ctx, txid, nil); err != nil {
				t.Fatal(err)
			}
			if mutation, err := storage.GetMutation(ctx, txid); err != nil || mutation == nil || mutation.Phase != MutationPhaseActive {
				t.Fatalf("active phase: mutation=%+v err=%v", mutation, err)
			}
			if err := storage.PruneMutation(ctx, txid); err == nil {
				t.Fatal("active mutation was pruned")
			}
			if err := storage.CommitMutation(ctx, txid, 1); err != nil {
				t.Fatal(err)
			}
			mutations, err := storage.ListMutations(ctx)
			if err != nil || len(mutations) != 1 || mutations[0].Txid != *txid || mutations[0].Phase != MutationPhaseApplied {
				t.Fatalf("applied enumeration: mutations=%+v err=%v", mutations, err)
			}
			if err := storage.PruneMutation(ctx, txid); err != nil {
				t.Fatal(err)
			}
			if err := storage.PruneMutation(ctx, txid); err != nil {
				t.Fatalf("idempotent prune: %v", err)
			}
			if mutation, err := storage.GetMutation(ctx, txid); err != nil || mutation != nil {
				t.Fatalf("mutation after prune: mutation=%+v err=%v", mutation, err)
			}
			if applied, err := storage.HasAppliedTx(ctx, txid); err != nil || !applied {
				t.Fatalf("prune removed applied marker: applied=%v err=%v", applied, err)
			}
			if _, err := storage.RollbackMutation(ctx, txid); err == nil {
				t.Fatal("rollback silently accepted an applied transaction after its journal was pruned")
			}
		})
	}
}

func TestReversibleTopicStorage_BeginIsAtomicOnSnapshotFailure(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			topicStorage, cleanup := b.factory(t)
			defer cleanup()
			storage := topicStorage.(ReversibleTopicStorage)
			ctx := context.Background()
			input := makeOutpoint(0x5a, 0)
			mutationTxid := makeTxid(0x5b)
			if err := storage.InsertOutput(ctx, input, &input.Txid, 1, nil, []byte{0x01}, 1); err != nil {
				t.Fatal(err)
			}
			if err := storage.BeginMutation(ctx, mutationTxid, []*transaction.Outpoint{input}); err == nil {
				t.Fatal("begin accepted a malformed ancestry snapshot")
			}
			if mutation, err := storage.GetMutation(ctx, mutationTxid); err != nil || mutation != nil {
				t.Fatalf("failed begin left a mutation guard: mutation=%+v err=%v", mutation, err)
			}
			rec, err := storage.GetOutput(ctx, input)
			if err != nil || rec == nil || rec.SpendTxid != nil {
				t.Fatalf("failed begin partially marked its input: output=%+v err=%v", rec, err)
			}
		})
	}
}

func TestReversibleTopicStorage_SerializesInputReservations(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			topicStorage, cleanup := b.factory(t)
			defer cleanup()
			storage := topicStorage.(ReversibleTopicStorage)
			ctx := context.Background()
			input := makeOutpoint(0x61, 0)
			first := makeTxid(0x62)
			second := makeTxid(0x63)
			if err := storage.InsertOutput(ctx, input, &input.Txid, 1, nil, nil, 1); err != nil {
				t.Fatal(err)
			}
			start := make(chan struct{})
			type beginResult struct {
				txid *chainhash.Hash
				err  error
			}
			results := make(chan beginResult, 2)
			for _, txid := range []*chainhash.Hash{first, second} {
				go func(txid *chainhash.Hash) {
					<-start
					results <- beginResult{txid: txid, err: storage.BeginMutation(ctx, txid, []*transaction.Outpoint{input})}
				}(txid)
			}
			close(start)
			var winner, loser *chainhash.Hash
			for range 2 {
				result := <-results
				if result.err == nil {
					if winner != nil {
						t.Fatal("both concurrent mutations reserved the same input")
					}
					winner = result.txid
				} else {
					loser = result.txid
				}
			}
			if winner == nil || loser == nil {
				t.Fatalf("concurrent begin results: winner=%v loser=%v", winner, loser)
			}
			rec, err := storage.GetOutput(ctx, input)
			if err != nil || rec == nil || rec.SpendTxid == nil || !rec.SpendTxid.Equal(*winner) {
				t.Fatalf("winning reservation was not preserved: output=%+v err=%v", rec, err)
			}
			if _, err := storage.RollbackMutation(ctx, winner); err != nil {
				t.Fatal(err)
			}
			if err := storage.FinalizeRollback(ctx, winner); err != nil {
				t.Fatal(err)
			}
			if err := storage.BeginMutation(ctx, loser, []*transaction.Outpoint{input}); err != nil {
				t.Fatalf("input remained reserved after finalization: %v", err)
			}
		})
	}
}

func TestReversibleTopicStorage_RollbackRejectsActiveAndCommittedDescendants(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			topicStorage, cleanup := b.factory(t)
			defer cleanup()
			storage := topicStorage.(ReversibleTopicStorage)
			ctx := context.Background()
			parentTxid := makeTxid(0x71)
			parentOutput := makeOutpoint(0x71, 0)
			descendantTxid := makeTxid(0x72)
			if err := storage.BeginMutation(ctx, parentTxid, nil); err != nil {
				t.Fatal(err)
			}
			if err := storage.InsertOutput(ctx, parentOutput, parentTxid, 1, nil, nil, 1); err != nil {
				t.Fatal(err)
			}
			if err := storage.CommitMutation(ctx, parentTxid, 1); err != nil {
				t.Fatal(err)
			}
			if err := storage.BeginMutation(ctx, descendantTxid, []*transaction.Outpoint{parentOutput}); err != nil {
				t.Fatal(err)
			}
			if _, err := storage.RollbackMutation(ctx, parentTxid); err == nil {
				t.Fatal("parent rollback overwrote an active descendant reservation")
			}
			if rec, err := storage.GetOutput(ctx, parentOutput); err != nil || rec == nil || rec.SpendTxid == nil || !rec.SpendTxid.Equal(*descendantTxid) {
				t.Fatalf("parent changed after rejected rollback: output=%+v err=%v", rec, err)
			}
			if err := storage.CommitMutation(ctx, descendantTxid, 2); err != nil {
				t.Fatal(err)
			}
			if _, err := storage.RollbackMutation(ctx, parentTxid); err == nil {
				t.Fatal("parent rollback ignored a committed descendant")
			}
			if err := storage.PruneMutation(ctx, descendantTxid); err == nil {
				t.Fatal("descendant prune discarded its link to a reversible parent")
			}
			if _, err := storage.RollbackMutation(ctx, descendantTxid); err != nil {
				t.Fatal(err)
			}
			if err := storage.FinalizeRollback(ctx, descendantTxid); err != nil {
				t.Fatal(err)
			}
			if _, err := storage.RollbackMutation(ctx, parentTxid); err != nil {
				t.Fatalf("parent rollback after descendant cleanup: %v", err)
			}
		})
	}
}

func assertOutputBeforeImage(t *testing.T, storage TopicStorage, op *transaction.Outpoint, spend *chainhash.Hash, consumedBy, deps []byte) {
	t.Helper()
	rec, err := storage.GetOutput(context.Background(), op)
	if err != nil || rec == nil {
		t.Fatalf("output %s was not restored: output=%+v err=%v", op, rec, err)
	}
	if (spend == nil) != (rec.SpendTxid == nil) || spend != nil && !rec.SpendTxid.Equal(*spend) {
		t.Fatalf("output %s spend = %v, want %v", op, rec.SpendTxid, spend)
	}
	if !bytes.Equal(rec.ConsumedBy, consumedBy) || !bytes.Equal(rec.Deps, deps) {
		t.Fatalf("output %s before image mismatch: consumed_by=%x deps=%x", op, rec.ConsumedBy, rec.Deps)
	}
}

func TestTopicStorage_AppliedTx(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ts, cleanup := b.factory(t)
			defer cleanup()
			ctx := context.Background()

			txid := makeTxid(0xAA)

			exists, err := ts.HasAppliedTx(ctx, txid)
			if err != nil {
				t.Fatal(err)
			}
			if exists {
				t.Error("expected false before insert")
			}

			if err := ts.InsertAppliedTx(ctx, txid, 1.0); err != nil {
				t.Fatal(err)
			}

			exists, err = ts.HasAppliedTx(ctx, txid)
			if err != nil {
				t.Fatal(err)
			}
			if !exists {
				t.Error("expected true after insert")
			}

			// Duplicate insert should not error
			if err := ts.InsertAppliedTx(ctx, txid, 2.0); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTopicStorage_Events(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ts, cleanup := b.factory(t)
			defer cleanup()
			ctx := context.Background()

			op1 := makeOutpoint(0x01, 0)
			op2 := makeOutpoint(0x02, 0)

			// Insert outputs first (events join against outputs)
			if err := ts.InsertOutput(ctx, op1, makeTxid(0x01), 1, nil, nil, 1.0); err != nil {
				t.Fatal(err)
			}
			if err := ts.InsertOutput(ctx, op2, makeTxid(0x02), 1, nil, nil, 2.0); err != nil {
				t.Fatal(err)
			}

			if err := ts.SaveEvent(ctx, "name:test", op1, 1.0); err != nil {
				t.Fatal(err)
			}
			if err := ts.SaveEvent(ctx, "name:test", op2, 2.0); err != nil {
				t.Fatal(err)
			}
			if err := ts.SaveEvent(ctx, "name:other", op1, 1.0); err != nil {
				t.Fatal(err)
			}

			results, err := ts.FindByEvent(ctx, "name:test", &QueryOpts{})
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 2 {
				t.Errorf("events for name:test = %d, want 2", len(results))
			}

			// Reverse order
			results, err = ts.FindByEvent(ctx, "name:test", &QueryOpts{Reverse: true, Limit: 1})
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 1 {
				t.Fatal("expected 1 result")
			}
			if results[0].Score != 2.0 {
				t.Errorf("score = %f, want 2.0 (highest first)", results[0].Score)
			}

			// Delete event
			if err := ts.DeleteEvent(ctx, "name:test", op1); err != nil {
				t.Fatal(err)
			}
			results, err = ts.FindByEvent(ctx, "name:test", &QueryOpts{})
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 1 {
				t.Errorf("events after delete = %d, want 1", len(results))
			}
		})
	}
}

func TestTopicStorage_PeerInteractions(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ts, cleanup := b.factory(t)
			defer cleanup()
			ctx := context.Background()

			since, err := ts.GetLastInteraction(ctx, "peer1")
			if err != nil {
				t.Fatal(err)
			}
			if since != 0 {
				t.Errorf("since = %f, want 0", since)
			}

			if err := ts.UpdateLastInteraction(ctx, "peer1", 100.5); err != nil {
				t.Fatal(err)
			}

			since, err = ts.GetLastInteraction(ctx, "peer1")
			if err != nil {
				t.Fatal(err)
			}
			if since != 100.5 {
				t.Errorf("since = %f, want 100.5", since)
			}

			// Update should overwrite
			if err := ts.UpdateLastInteraction(ctx, "peer1", 200.0); err != nil {
				t.Fatal(err)
			}
			since, err = ts.GetLastInteraction(ctx, "peer1")
			if err != nil {
				t.Fatal(err)
			}
			if since != 200.0 {
				t.Errorf("since = %f, want 200.0", since)
			}
		})
	}
}

func TestPostgresFactory_TopicRegistry(t *testing.T) {
	if os.Getenv("SKIP_POSTGRES_TESTS") == "1" {
		t.Skip("SKIP_POSTGRES_TESTS=1")
	}

	ctx := context.Background()
	container, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("test_registry"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("5432/tcp")),
	)
	if err != nil {
		t.Skipf("failed to start postgres container: %v", err)
	}
	defer container.Terminate(ctx)

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}

	factory, err := NewPostgresFactory(connStr)
	if err != nil {
		t.Fatal(err)
	}
	defer factory.Close()

	// Same topic name should return same topicID
	ts1, err := factory.Topic("tm_bap")
	if err != nil {
		t.Fatal(err)
	}
	ts2, err := factory.Topic("tm_bap")
	if err != nil {
		t.Fatal(err)
	}

	if ts1.TopicID() != ts2.TopicID() {
		t.Errorf("topicID mismatch: %d vs %d", ts1.TopicID(), ts2.TopicID())
	}
	if ts1.TopicID() == 0 {
		t.Error("postgres topicID should not be 0")
	}

	// Different topics should get different IDs
	ts3, err := factory.Topic("tm_bsocial")
	if err != nil {
		t.Fatal(err)
	}
	if ts3.TopicID() == ts1.TopicID() {
		t.Error("different topics should get different IDs")
	}
}

func TestPostgresFactory_TxTopicIndex(t *testing.T) {
	if os.Getenv("SKIP_POSTGRES_TESTS") == "1" {
		t.Skip("SKIP_POSTGRES_TESTS=1")
	}

	ctx := context.Background()
	container, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("test_txindex"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("5432/tcp")),
	)
	if err != nil {
		t.Skipf("failed to start postgres container: %v", err)
	}
	defer container.Terminate(ctx)

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}

	factory, err := NewPostgresFactory(connStr)
	if err != nil {
		t.Fatal(err)
	}
	defer factory.Close()

	// Ensure topics exist
	if _, err := factory.Topic("tm_bap"); err != nil {
		t.Fatal(err)
	}
	if _, err := factory.Topic("tm_opns"); err != nil {
		t.Fatal(err)
	}

	idx := factory.TxTopicIndex()
	txid := makeTxid(0xBB)

	if err := idx.Record(ctx, txid, "tm_bap"); err != nil {
		t.Fatal(err)
	}
	if err := idx.Record(ctx, txid, "tm_opns"); err != nil {
		t.Fatal(err)
	}

	topics, err := idx.Topics(ctx, txid)
	if err != nil {
		t.Fatal(err)
	}
	if len(topics) != 2 {
		t.Errorf("topics = %d, want 2", len(topics))
	}

	if err := idx.Delete(ctx, txid); err != nil {
		t.Fatal(err)
	}
	topics, err = idx.Topics(ctx, txid)
	if err != nil {
		t.Fatal(err)
	}
	if len(topics) != 0 {
		t.Errorf("topics after delete = %d, want 0", len(topics))
	}
}

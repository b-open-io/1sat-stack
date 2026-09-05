package ecosystemalias

import (
	"encoding/json"
	"testing"

	overlaystorage "github.com/b-open-io/1sat-stack/pkg/overlay/storage"
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	overlaylookup "github.com/bsv-blockchain/go-sdk/overlay/lookup"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

func TestLookupAdmitAndQuery(t *testing.T) {
	store := lookupTestStore(t)
	svc := NewLookupService(store)
	owner := decodePrivateKey(t, "0000000000000000000000000000000000000000000000000000000000000002").PubKey().Compressed()
	script := topicSignedScript(t, "handcash", "handcash.io", owner)
	beef, txid := topicTestBeef(t, []*transaction.TransactionOutput{{Satoshis: 1, LockingScript: script}})
	op := &transaction.Outpoint{Txid: *txid, Index: 0}
	if err := store.InsertOutput(t.Context(), op, txid, 1, nil, nil, 1); err != nil {
		t.Fatal(err)
	}
	atomic, err := beef.AtomicBytes(txid)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.OutputAdmittedByTopic(t.Context(), &engine.OutputAdmittedByTopic{
		Topic:       TopicName,
		OutputIndex: 0,
		AtomicBEEF:  atomic,
	}); err != nil {
		t.Fatal(err)
	}

	got := lookupOutpoints(t, svc, `{"alias":"handcash"}`)
	if len(got) != 1 || got[0] != op.String() {
		t.Fatalf("alias lookup %v", got)
	}
	got = lookupOutpoints(t, svc, `{"domain":"handcash.io"}`)
	if len(got) != 1 || got[0] != op.String() {
		t.Fatalf("domain lookup %v", got)
	}
	got = lookupOutpoints(t, svc, `{"findAll":true}`)
	if len(got) != 1 {
		t.Fatalf("findAll %v", got)
	}
}

func TestLookupSkipsSpentAndPaginates(t *testing.T) {
	store := lookupTestStore(t)
	svc := NewLookupService(store)
	owner := decodePrivateKey(t, "0000000000000000000000000000000000000000000000000000000000000002").PubKey().Compressed()

	var ops []*transaction.Outpoint
	for i, alias := range []string{"aaa", "bbb", "ccc"} {
		script := topicSignedScript(t, alias, "handcash.io", owner)
		beef, txid := topicTestBeef(t, []*transaction.TransactionOutput{{Satoshis: 1, LockingScript: script}})
		op := &transaction.Outpoint{Txid: *txid, Index: 0}
		if err := store.InsertOutput(t.Context(), op, txid, 1, nil, nil, float64(i+1)); err != nil {
			t.Fatal(err)
		}
		atomic, err := beef.AtomicBytes(txid)
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.OutputAdmittedByTopic(t.Context(), &engine.OutputAdmittedByTopic{
			Topic:       TopicName,
			OutputIndex: 0,
			AtomicBEEF:  atomic,
		}); err != nil {
			t.Fatal(err)
		}
		ops = append(ops, op)
	}
	spend := ops[1].Txid
	if err := store.MarkSpent(t.Context(), ops[1], &spend); err != nil {
		t.Fatal(err)
	}

	got := lookupOutpoints(t, svc, `{"domain":"handcash.io"}`)
	if len(got) != 2 {
		t.Fatalf("unspent domain hits %d, want 2", len(got))
	}
	got = lookupOutpoints(t, svc, `{"domain":"handcash.io","limit":1}`)
	if len(got) != 1 {
		t.Fatalf("limit %v", got)
	}
	got = lookupOutpoints(t, svc, `{"domain":"handcash.io","skip":1,"limit":10}`)
	if len(got) != 1 {
		t.Fatalf("skip %v", got)
	}
}

func TestLookupRestampScoreOnBlockHeight(t *testing.T) {
	store := lookupTestStore(t)
	svc := NewLookupService(store)
	owner := decodePrivateKey(t, "0000000000000000000000000000000000000000000000000000000000000002").PubKey().Compressed()
	script := topicSignedScript(t, "handcash", "handcash.io", owner)
	beef, txid := topicTestBeef(t, []*transaction.TransactionOutput{{Satoshis: 1, LockingScript: script}})
	op := &transaction.Outpoint{Txid: *txid, Index: 0}
	if err := store.InsertOutput(t.Context(), op, txid, 1, nil, nil, 1); err != nil {
		t.Fatal(err)
	}
	atomic, err := beef.AtomicBytes(txid)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.OutputAdmittedByTopic(t.Context(), &engine.OutputAdmittedByTopic{
		Topic:       TopicName,
		OutputIndex: 0,
		AtomicBEEF:  atomic,
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.OutputBlockHeightUpdated(t.Context(), txid, 800000, 3); err != nil {
		t.Fatal(err)
	}
	var score float64
	err = store.DB().QueryRowContext(t.Context(),
		`SELECT score FROM events WHERE event = ?`, eventAliasPrefix+"handcash").Scan(&score)
	if err != nil {
		t.Fatal(err)
	}
	want := types.HeightScore(800000, 3)
	if score != want {
		t.Fatalf("event score %v want %v", score, want)
	}
}

func lookupTestStore(t *testing.T) overlaystorage.TopicStorage {
	t.Helper()
	factory, err := overlaystorage.NewSQLiteFactory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = factory.Close() })
	store, err := factory.Topic(TopicName)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func lookupOutpoints(t *testing.T, svc *LookupService, query string) []string {
	t.Helper()
	answer, err := svc.Lookup(t.Context(), &overlaylookup.LookupQuestion{
		Service: LookupName,
		Query:   json.RawMessage(query),
	})
	if err != nil {
		t.Fatal(err)
	}
	if answer.Type != overlaylookup.AnswerTypeFormula {
		t.Fatalf("type %s", answer.Type)
	}
	out := make([]string, 0, len(answer.Formulas))
	for _, f := range answer.Formulas {
		out = append(out, f.Outpoint.String())
	}
	return out
}

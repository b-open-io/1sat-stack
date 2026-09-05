package storage

import "testing"

func TestFindOutputUsesOnlyBoundLookupTopic(t *testing.T) {
	factory, err := NewSQLiteFactory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = factory.Close() })
	op := makeOutpoint(1, 0)
	for i, topic := range []string{"tm_a", "tm_b"} {
		store, err := factory.Topic(topic)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.InsertOutput(t.Context(), op, &op.Txid, uint64(i+1), nil, nil, 1); err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			if err := store.MarkSpent(t.Context(), op, makeTxid(2)); err != nil {
				t.Fatal(err)
			}
		}
	}
	adapter := NewEngineAdapter(factory.Factory(), nil, factory.TxTopicIndex())
	if _, err := adapter.FindOutput(t.Context(), op, nil, nil, false); err == nil {
		t.Fatal("unbound topic-less lookup must fail")
	}
	bound := "tm_a"
	adapter.LookupTopic = &bound
	got, err := adapter.FindOutput(t.Context(), op, nil, nil, false)
	if err != nil || got == nil || got.Topic != bound || got.Spent {
		t.Fatalf("bound lookup: %+v, %v", got, err)
	}
	other := "tm_b"
	got, err = adapter.FindOutput(t.Context(), op, &other, nil, false)
	if err != nil || got == nil || got.Topic != other || !got.Spent {
		t.Fatalf("explicit topic: %+v, %v", got, err)
	}
}

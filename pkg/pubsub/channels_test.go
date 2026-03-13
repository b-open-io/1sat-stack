package pubsub

import (
	"context"
	"testing"
	"time"
)

func TestChannelPubSub_ExactMatch(t *testing.T) {
	ps := NewChannelPubSub(nil)
	defer ps.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := ps.Subscribe(ctx, []string{"ordlock"})
	if err != nil {
		t.Fatal(err)
	}

	if err := ps.Publish(context.Background(), "ordlock", "abc123_0", 1.0); err != nil {
		t.Fatal(err)
	}

	select {
	case ev := <-ch:
		if ev.Topic != "ordlock" {
			t.Fatalf("expected topic ordlock, got %s", ev.Topic)
		}
		if ev.Member != "abc123_0" {
			t.Fatalf("expected member abc123_0, got %s", ev.Member)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestChannelPubSub_GlobPattern(t *testing.T) {
	ps := NewChannelPubSub(nil)
	defer ps.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := ps.Subscribe(ctx, []string{"bsv21:*"})
	if err != nil {
		t.Fatal(err)
	}

	if err := ps.Publish(context.Background(), "bsv21:abc123_0", "data1", 1.0); err != nil {
		t.Fatal(err)
	}

	select {
	case ev := <-ch:
		if ev.Topic != "bsv21:abc123_0" {
			t.Fatalf("expected topic bsv21:abc123_0, got %s", ev.Topic)
		}
		if ev.Member != "data1" {
			t.Fatalf("expected member data1, got %s", ev.Member)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for glob event")
	}
}

func TestChannelPubSub_GlobNoMatch(t *testing.T) {
	ps := NewChannelPubSub(nil)
	defer ps.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := ps.Subscribe(ctx, []string{"bsv21:*"})
	if err != nil {
		t.Fatal(err)
	}

	if err := ps.Publish(context.Background(), "bap:ID", "data1", 1.0); err != nil {
		t.Fatal(err)
	}

	select {
	case <-ch:
		t.Fatal("should not have received event for non-matching topic")
	case <-time.After(100 * time.Millisecond):
		// expected: no event received
	}
}

func TestChannelPubSub_MultiplePatterns(t *testing.T) {
	ps := NewChannelPubSub(nil)
	defer ps.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := ps.Subscribe(ctx, []string{"ordlock", "spend:ordlock"})
	if err != nil {
		t.Fatal(err)
	}

	if err := ps.Publish(context.Background(), "ordlock", "out1"); err != nil {
		t.Fatal(err)
	}
	if err := ps.Publish(context.Background(), "spend:ordlock", "out2"); err != nil {
		t.Fatal(err)
	}

	received := make(map[string]string)
	for i := 0; i < 2; i++ {
		select {
		case ev := <-ch:
			received[ev.Topic] = ev.Member
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for event %d", i+1)
		}
	}

	if received["ordlock"] != "out1" {
		t.Fatalf("expected ordlock->out1, got %s", received["ordlock"])
	}
	if received["spend:ordlock"] != "out2" {
		t.Fatalf("expected spend:ordlock->out2, got %s", received["spend:ordlock"])
	}
}

func TestChannelPubSub_UnsubscribeOnCancel(t *testing.T) {
	ps := NewChannelPubSub(nil)
	defer ps.Close()

	ctx, cancel := context.WithCancel(context.Background())

	ch, err := ps.Subscribe(ctx, []string{"test"})
	if err != nil {
		t.Fatal(err)
	}

	cancel()

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected channel to be closed")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for channel close")
	}
}

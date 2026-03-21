package store

import (
	"context"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// startTestServer creates an in-memory BadgerStore + RedconServer on a random port.
func startTestServer(t *testing.T) (*RedconServer, *redis.Client) {
	t.Helper()

	store, err := NewBadgerStoreFromConfig(&BadgerConfig{InMemory: true}, slog.Default())
	if err != nil {
		t.Fatalf("failed to create badger store: %v", err)
	}

	// Find a free port
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	addr := l.Addr().String()
	l.Close()

	server := NewRedconServer(store, addr, slog.Default())

	go func() {
		if err := server.ListenAndServe(); err != nil {
			// Expected on close
		}
	}()

	// Wait for server to start
	time.Sleep(50 * time.Millisecond)

	client := redis.NewClient(&redis.Options{Addr: addr})

	t.Cleanup(func() {
		client.Close()
		server.Close()
		store.Close()
	})

	return server, client
}

func TestRedconPing(t *testing.T) {
	_, client := startTestServer(t)
	ctx := context.Background()

	result, err := client.Ping(ctx).Result()
	if err != nil {
		t.Fatalf("ping failed: %v", err)
	}
	if result != "PONG" {
		t.Fatalf("expected PONG, got %s", result)
	}
}

func TestRedconKV(t *testing.T) {
	_, client := startTestServer(t)
	ctx := context.Background()

	// SET and GET
	if err := client.Set(ctx, "hello", "world", 0).Err(); err != nil {
		t.Fatalf("set failed: %v", err)
	}

	val, err := client.Get(ctx, "hello").Result()
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if val != "world" {
		t.Fatalf("expected 'world', got '%s'", val)
	}

	// GET missing key
	_, err = client.Get(ctx, "missing").Result()
	if err != redis.Nil {
		t.Fatalf("expected redis.Nil for missing key, got %v", err)
	}

	// DEL
	deleted, err := client.Del(ctx, "hello").Result()
	if err != nil {
		t.Fatalf("del failed: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted, got %d", deleted)
	}

	_, err = client.Get(ctx, "hello").Result()
	if err != redis.Nil {
		t.Fatalf("expected redis.Nil after delete, got %v", err)
	}
}

func TestRedconHash(t *testing.T) {
	_, client := startTestServer(t)
	ctx := context.Background()

	// HSET
	if err := client.HSet(ctx, "user:1", "name", "alice", "age", "30").Err(); err != nil {
		t.Fatalf("hset failed: %v", err)
	}

	// HGET
	val, err := client.HGet(ctx, "user:1", "name").Result()
	if err != nil {
		t.Fatalf("hget failed: %v", err)
	}
	if val != "alice" {
		t.Fatalf("expected 'alice', got '%s'", val)
	}

	// HGETALL
	all, err := client.HGetAll(ctx, "user:1").Result()
	if err != nil {
		t.Fatalf("hgetall failed: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(all))
	}
	if all["name"] != "alice" || all["age"] != "30" {
		t.Fatalf("unexpected hgetall result: %v", all)
	}

	// HDEL
	if err := client.HDel(ctx, "user:1", "age").Err(); err != nil {
		t.Fatalf("hdel failed: %v", err)
	}
	all, _ = client.HGetAll(ctx, "user:1").Result()
	if len(all) != 1 {
		t.Fatalf("expected 1 field after hdel, got %d", len(all))
	}

	// HMGET
	vals, err := client.HMGet(ctx, "user:1", "name", "missing").Result()
	if err != nil {
		t.Fatalf("hmget failed: %v", err)
	}
	if vals[0] != "alice" {
		t.Fatalf("expected 'alice', got %v", vals[0])
	}
	if vals[1] != nil {
		t.Fatalf("expected nil for missing field, got %v", vals[1])
	}
}

func TestRedconSet(t *testing.T) {
	_, client := startTestServer(t)
	ctx := context.Background()

	// SADD
	if err := client.SAdd(ctx, "tags", "go", "rust", "zig").Err(); err != nil {
		t.Fatalf("sadd failed: %v", err)
	}

	// SMEMBERS
	members, err := client.SMembers(ctx, "tags").Result()
	if err != nil {
		t.Fatalf("smembers failed: %v", err)
	}
	if len(members) != 3 {
		t.Fatalf("expected 3 members, got %d", len(members))
	}

	// SISMEMBER
	exists, err := client.SIsMember(ctx, "tags", "go").Result()
	if err != nil {
		t.Fatalf("sismember failed: %v", err)
	}
	if !exists {
		t.Fatal("expected 'go' to be a member")
	}

	exists, err = client.SIsMember(ctx, "tags", "python").Result()
	if err != nil {
		t.Fatalf("sismember failed: %v", err)
	}
	if exists {
		t.Fatal("expected 'python' to NOT be a member")
	}

	// SREM
	if err := client.SRem(ctx, "tags", "rust").Err(); err != nil {
		t.Fatalf("srem failed: %v", err)
	}
	members, _ = client.SMembers(ctx, "tags").Result()
	if len(members) != 2 {
		t.Fatalf("expected 2 members after srem, got %d", len(members))
	}
}

func TestRedconSortedSet(t *testing.T) {
	_, client := startTestServer(t)
	ctx := context.Background()

	// ZADD
	if err := client.ZAdd(ctx, "scores",
		redis.Z{Score: 100, Member: "alice"},
		redis.Z{Score: 200, Member: "bob"},
		redis.Z{Score: 150, Member: "charlie"},
	).Err(); err != nil {
		t.Fatalf("zadd failed: %v", err)
	}

	// ZSCORE
	score, err := client.ZScore(ctx, "scores", "bob").Result()
	if err != nil {
		t.Fatalf("zscore failed: %v", err)
	}
	if score != 200 {
		t.Fatalf("expected 200, got %f", score)
	}

	// ZCARD
	count, err := client.ZCard(ctx, "scores").Result()
	if err != nil {
		t.Fatalf("zcard failed: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected 3, got %d", count)
	}

	// ZRANGEBYSCORE
	vals, err := client.ZRangeByScoreWithScores(ctx, "scores", &redis.ZRangeBy{
		Min: "100",
		Max: "150",
	}).Result()
	if err != nil {
		t.Fatalf("zrangebyscore failed: %v", err)
	}
	if len(vals) != 2 {
		t.Fatalf("expected 2 results, got %d", len(vals))
	}
	if vals[0].Member != "alice" || vals[1].Member != "charlie" {
		t.Fatalf("unexpected order: %v", vals)
	}

	// ZREVRANGEBYSCORE
	vals, err = client.ZRevRangeByScoreWithScores(ctx, "scores", &redis.ZRangeBy{
		Min: "-inf",
		Max: "+inf",
	}).Result()
	if err != nil {
		t.Fatalf("zrevrangebyscore failed: %v", err)
	}
	if len(vals) != 3 {
		t.Fatalf("expected 3 results, got %d", len(vals))
	}
	if vals[0].Member != "bob" {
		t.Fatalf("expected bob first in reverse, got %s", vals[0].Member)
	}

	// ZINCRBY
	newScore, err := client.ZIncrBy(ctx, "scores", 50, "alice").Result()
	if err != nil {
		t.Fatalf("zincrby failed: %v", err)
	}
	if newScore != 150 {
		t.Fatalf("expected 150 after incr, got %f", newScore)
	}

	// ZREM
	if err := client.ZRem(ctx, "scores", "charlie").Err(); err != nil {
		t.Fatalf("zrem failed: %v", err)
	}
	count, _ = client.ZCard(ctx, "scores").Result()
	if count != 2 {
		t.Fatalf("expected 2 after zrem, got %d", count)
	}
}

func TestRedconBinaryValues(t *testing.T) {
	_, client := startTestServer(t)
	ctx := context.Background()

	// Verify binary data survives round-trip (important for txids, outpoints)
	binaryKey := "\x00\x01\x02\x03"
	binaryVal := make([]byte, 36) // outpoint-sized
	for i := range binaryVal {
		binaryVal[i] = byte(i)
	}

	if err := client.Set(ctx, binaryKey, binaryVal, 0).Err(); err != nil {
		t.Fatalf("set binary failed: %v", err)
	}

	got, err := client.Get(ctx, binaryKey).Bytes()
	if err != nil {
		t.Fatalf("get binary failed: %v", err)
	}
	if len(got) != 36 {
		t.Fatalf("expected 36 bytes, got %d", len(got))
	}
	for i, b := range got {
		if b != byte(i) {
			t.Fatalf("byte mismatch at %d: expected %d, got %d", i, i, b)
		}
	}
}

func TestRedconPubSub(t *testing.T) {
	server, client := startTestServer(t)
	ctx := context.Background()

	sub := client.Subscribe(ctx, "events")
	defer sub.Close()

	// Wait for subscription to be established
	time.Sleep(50 * time.Millisecond)

	// Publish in-process
	server.Publish("events", "hello")

	// Receive
	msg, err := sub.ReceiveTimeout(ctx, 2*time.Second)
	if err != nil {
		t.Fatalf("receive failed: %v", err)
	}

	switch m := msg.(type) {
	case *redis.Message:
		if m.Channel != "events" || m.Payload != "hello" {
			t.Fatalf("unexpected message: %v", m)
		}
	case *redis.Subscription:
		// This is the subscription confirmation, try to receive the actual message
		msg, err = sub.ReceiveTimeout(ctx, 2*time.Second)
		if err != nil {
			t.Fatalf("receive message failed: %v", err)
		}
		m2 := msg.(*redis.Message)
		if m2.Channel != "events" || m2.Payload != "hello" {
			t.Fatalf("unexpected message: %v", m2)
		}
	default:
		t.Fatalf("unexpected type: %T", msg)
	}
}

func TestRedconUnknownCommandFails(t *testing.T) {
	_, client := startTestServer(t)
	ctx := context.Background()

	// Unimplemented commands must return errors, not silently succeed
	err := client.Do(ctx, "FLUSHALL").Err()
	if err == nil {
		t.Fatal("expected error for unimplemented command FLUSHALL")
	}

	err = client.Do(ctx, "KEYS", "*").Err()
	if err == nil {
		t.Fatal("expected error for unimplemented command KEYS")
	}

	err = client.Do(ctx, "EVAL", "return 1", "0").Err()
	if err == nil {
		t.Fatal("expected error for unimplemented command EVAL")
	}
}

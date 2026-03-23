package channel_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/b-open-io/1sat-stack/pkg/channel"
	"github.com/b-open-io/1sat-stack/pkg/store"
	"github.com/redis/go-redis/v9"
)

func redisFromConn(conn net.Conn) *redis.Client {
	return redis.NewClient(&redis.Options{
		Dialer: func(_ context.Context, _, _ string) (net.Conn, error) {
			return conn, nil
		},
		PoolSize:       1,
		MaxActiveConns: 1,
	})
}

// TestDirectDial verifies a module can talk RESP through a net.Conn to the redcon server.
func TestDirectDial(t *testing.T) {
	ctx := context.Background()

	// Start in-memory Badger + redcon server.
	bs, err := store.NewBadgerStore("badger://?memory=true", nil)
	if err != nil {
		t.Fatalf("create badger store: %v", err)
	}
	defer bs.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	rs := store.NewRedconServer(bs, addr, nil)
	go rs.ListenAndServe()
	defer rs.Close()

	// Give the server a moment to start.
	time.Sleep(50 * time.Millisecond)

	// Module side: dial the server and wrap the connection.
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial redcon: %v", err)
	}
	defer conn.Close()

	client := redisFromConn(conn)
	defer client.Close()

	// Test basic operations.
	if err := client.Set(ctx, "hello", "world", 0).Err(); err != nil {
		t.Fatalf("SET: %v", err)
	}

	val, err := client.Get(ctx, "hello").Result()
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if val != "world" {
		t.Fatalf("GET returned %q, want %q", val, "world")
	}

	// Hash operations.
	if err := client.HSet(ctx, "myhash", "field1", "value1").Err(); err != nil {
		t.Fatalf("HSET: %v", err)
	}

	hval, err := client.HGet(ctx, "myhash", "field1").Result()
	if err != nil {
		t.Fatalf("HGET: %v", err)
	}
	if hval != "value1" {
		t.Fatalf("HGET returned %q, want %q", hval, "value1")
	}

	// Sorted set operations.
	if err := client.ZAdd(ctx, "myzset", redis.Z{Score: 1.0, Member: "a"}).Err(); err != nil {
		t.Fatalf("ZADD: %v", err)
	}
	if err := client.ZAdd(ctx, "myzset", redis.Z{Score: 2.0, Member: "b"}).Err(); err != nil {
		t.Fatalf("ZADD: %v", err)
	}

	card, err := client.ZCard(ctx, "myzset").Result()
	if err != nil {
		t.Fatalf("ZCARD: %v", err)
	}
	if card != 2 {
		t.Fatalf("ZCARD returned %d, want 2", card)
	}

}

// TestMuxRoundTrip verifies the full mux path: module → mux → pipe → HostBridge → redcon → response.
func TestMuxRoundTrip(t *testing.T) {
	ctx := context.Background()

	// Start in-memory Badger + redcon server.
	bs, err := store.NewBadgerStore("badger://?memory=true", nil)
	if err != nil {
		t.Fatalf("create badger store: %v", err)
	}
	defer bs.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	rs := store.NewRedconServer(bs, addr, nil)
	go rs.ListenAndServe()
	defer rs.Close()
	time.Sleep(50 * time.Millisecond)

	// Use Dial helper: creates pipe pair, starts HostBridge, returns module-side net.Conn.
	conn, err := channel.Dial(func() (net.Conn, error) {
		return net.Dial("tcp", addr)
	})
	if err != nil {
		t.Fatalf("channel.Dial: %v", err)
	}
	defer conn.Close()

	client := redisFromConn(conn)
	defer client.Close()

	// Test through the mux.
	if err := client.Set(ctx, "mux_key", "mux_value", 0).Err(); err != nil {
		t.Fatalf("SET through mux: %v", err)
	}

	val, err := client.Get(ctx, "mux_key").Result()
	if err != nil {
		t.Fatalf("GET through mux: %v", err)
	}
	if val != "mux_value" {
		t.Fatalf("GET through mux returned %q, want %q", val, "mux_value")
	}

	// Hash through mux.
	if err := client.HSet(ctx, "mux_hash", "f1", "v1").Err(); err != nil {
		t.Fatalf("HSET through mux: %v", err)
	}
	hval, err := client.HGet(ctx, "mux_hash", "f1").Result()
	if err != nil {
		t.Fatalf("HGET through mux: %v", err)
	}
	if hval != "v1" {
		t.Fatalf("HGET through mux returned %q, want %q", hval, "v1")
	}
}

package chaintracks

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bsv-blockchain/go-sdk/block"
	"github.com/bsv-blockchain/go-sdk/chainhash"
)

const (
	// A real mainnet header, as api.1sat.app serves it.
	tipHeight = 967536
	tipHash   = "000000000000000022c877e56a734d8fa5c8779db25295ec47bb793fc183bb2b"
	tipRoot   = "309f33add3d8364c5f32c453651cdc166f8d2209039ceaa41f4c19a4904ab017"
	tipPrev   = "000000000000000002f7ac6cc88234c1c1db19a8ae0f580bc6ba3ddb9a9d688d"
	otherRoot = "0d8df5a812f84ff796791f1c52cba551cd3ffbb5801375f2a0edc9cb572d9156"
)

var headerJSON = `{"version":816103424,"previousHash":"` + tipPrev + `","merkleRoot":"` + tipRoot +
	`","time":1789880417,"bits":405250361,"nonce":2026750992,"height":` + strconv.Itoa(tipHeight) + `,"hash":"` + tipHash + `"}`

// testService serves the chaintracks routes this package documents, and
// counts requests so caching can be observed.
type testService struct {
	requests atomic.Int64
	server   *httptest.Server
}

func newTestService(t *testing.T) *testService {
	t.Helper()
	s := &testService{}
	mux := http.NewServeMux()
	count := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			s.requests.Add(1)
			h(w, r)
		}
	}
	notFound := func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"Header not found"}`))
	}
	json := func(w http.ResponseWriter, body string) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
	mux.HandleFunc("GET /tip", count(func(w http.ResponseWriter, r *http.Request) {
		json(w, headerJSON)
	}))
	mux.HandleFunc("GET /height", count(func(w http.ResponseWriter, r *http.Request) {
		json(w, `{"height":`+strconv.Itoa(tipHeight)+`}`)
	}))
	mux.HandleFunc("GET /network", count(func(w http.ResponseWriter, r *http.Request) {
		json(w, `{"network":"main"}`)
	}))
	mux.HandleFunc("GET /header/height/{height}", count(func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("height") != strconv.Itoa(tipHeight) {
			notFound(w)
			return
		}
		json(w, headerJSON)
	}))
	mux.HandleFunc("GET /header/hash/{hash}", count(func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("hash") != tipHash {
			notFound(w)
			return
		}
		json(w, headerJSON)
	}))
	mux.HandleFunc("GET /headers", count(func(w http.ResponseWriter, r *http.Request) {
		count, _ := strconv.Atoi(r.URL.Query().Get("count"))
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(make([]byte, count*block.HeaderSize))
	}))
	mux.HandleFunc("GET /boom", count(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream exploded", http.StatusBadGateway)
	}))
	s.server = httptest.NewServer(mux)
	t.Cleanup(s.server.Close)
	return s
}

func (s *testService) client() *Client { return NewClient(s.server.URL, nil) }

func mustHash(t *testing.T, hex string) *chainhash.Hash {
	t.Helper()
	h, err := chainhash.NewHashFromHex(hex)
	if err != nil {
		t.Fatalf("hash %s: %v", hex, err)
	}
	return h
}

func TestClient(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		run  func(t *testing.T, c *Client)
	}{
		{
			name: "valid root",
			run: func(t *testing.T, c *Client) {
				ok, err := c.IsValidRootForHeight(ctx, mustHash(t, tipRoot), tipHeight)
				if err != nil {
					t.Fatalf("IsValidRootForHeight: %v", err)
				}
				if !ok {
					t.Fatal("the header's own merkle root was rejected")
				}
			},
		},
		{
			name: "wrong root",
			run: func(t *testing.T, c *Client) {
				ok, err := c.IsValidRootForHeight(ctx, mustHash(t, otherRoot), tipHeight)
				if err != nil {
					t.Fatalf("IsValidRootForHeight: %v", err)
				}
				if ok {
					t.Fatal("a different merkle root was accepted")
				}
			},
		},
		{
			name: "missing height is an error, not a verdict",
			run: func(t *testing.T, c *Client) {
				ok, err := c.IsValidRootForHeight(ctx, mustHash(t, tipRoot), 1)
				if !errors.Is(err, ErrNotFound) {
					t.Fatalf("err = %v, want ErrNotFound", err)
				}
				if ok {
					t.Fatal("a missing header must not validate")
				}
			},
		},
		{
			name: "tip decodes every header field",
			run: func(t *testing.T, c *Client) {
				tip, err := c.Tip(ctx)
				if err != nil {
					t.Fatalf("Tip: %v", err)
				}
				if tip.Height != tipHeight {
					t.Fatalf("height = %d, want %d", tip.Height, tipHeight)
				}
				if tip.Hash.String() != tipHash {
					t.Fatalf("hash = %s", tip.Hash)
				}
				if tip.MerkleRoot.String() != tipRoot {
					t.Fatalf("merkle root = %s", tip.MerkleRoot)
				}
				if tip.PrevHash.String() != tipPrev {
					t.Fatalf("previous hash = %s", tip.PrevHash)
				}
				if tip.Version != 816103424 || tip.Timestamp != 1789880417 || tip.Bits != 405250361 || tip.Nonce != 2026750992 {
					t.Fatalf("header fields did not decode: %+v", tip)
				}
			},
		},
		{
			name: "height and network",
			run: func(t *testing.T, c *Client) {
				h, err := c.Height(ctx)
				if err != nil || h != tipHeight {
					t.Fatalf("Height = %d, %v", h, err)
				}
				if got := c.GetHeight(ctx); got != tipHeight {
					t.Fatalf("GetHeight = %d", got)
				}
				cur, err := c.CurrentHeight(ctx)
				if err != nil || cur != tipHeight {
					t.Fatalf("CurrentHeight = %d, %v", cur, err)
				}
				net, err := c.Network(ctx)
				if err != nil || net != "main" {
					t.Fatalf("Network = %q, %v", net, err)
				}
			},
		},
		{
			name: "header by hash",
			run: func(t *testing.T, c *Client) {
				h, err := c.HeaderByHash(ctx, mustHash(t, tipHash))
				if err != nil {
					t.Fatalf("HeaderByHash: %v", err)
				}
				if h.Height != tipHeight {
					t.Fatalf("height = %d", h.Height)
				}
				if _, err := c.HeaderByHash(ctx, mustHash(t, otherRoot)); !errors.Is(err, ErrNotFound) {
					t.Fatalf("unknown hash: err = %v, want ErrNotFound", err)
				}
			},
		},
		{
			name: "raw headers",
			run: func(t *testing.T, c *Client) {
				raw, err := c.RawHeaders(ctx, tipHeight, 2)
				if err != nil {
					t.Fatalf("RawHeaders: %v", err)
				}
				if len(raw) != 2*block.HeaderSize {
					t.Fatalf("got %d bytes, want %d", len(raw), 2*block.HeaderSize)
				}
			},
		},
		{
			name: "a non-200 carries the status",
			run: func(t *testing.T, c *Client) {
				var out struct{}
				err := c.getJSON(ctx, "/boom", &out)
				if err == nil {
					t.Fatal("HTTP 502 should be an error")
				}
				if !strings.Contains(err.Error(), "502") {
					t.Fatalf("error should name the status, got %v", err)
				}
				if errors.Is(err, ErrNotFound) {
					t.Fatalf("502 is not ErrNotFound: %v", err)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.run(t, newTestService(t).client())
		})
	}
}

func TestClientCachesHeadersByHeight(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	c := svc.client()

	for range 3 {
		if _, err := c.HeaderByHeight(ctx, tipHeight); err != nil {
			t.Fatalf("HeaderByHeight: %v", err)
		}
	}
	if got := svc.requests.Load(); got != 1 {
		t.Fatalf("3 reads at one height made %d requests, want 1", got)
	}

	// A miss is not cached: the height may exist by the next call.
	for range 2 {
		if _, err := c.HeaderByHeight(ctx, 1); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	}
	if got := svc.requests.Load(); got != 3 {
		t.Fatalf("two misses made %d requests in total, want 3", got)
	}

	// The tip and the height move, so neither is cached.
	for range 2 {
		if _, err := c.Tip(ctx); err != nil {
			t.Fatalf("Tip: %v", err)
		}
		if _, err := c.Height(ctx); err != nil {
			t.Fatalf("Height: %v", err)
		}
	}
	if got := svc.requests.Load(); got != 7 {
		t.Fatalf("after 2 tips and 2 heights: %d requests, want 7", got)
	}
}

func TestNewClientTrimsBase(t *testing.T) {
	for _, in := range []string{
		"https://api.1sat.app/1sat/chaintracks",
		"https://api.1sat.app/1sat/chaintracks/",
		"  https://api.1sat.app/1sat/chaintracks  ",
	} {
		if got := NewClient(in, nil).BaseURL(); got != "https://api.1sat.app/1sat/chaintracks" {
			t.Fatalf("NewClient(%q).BaseURL() = %q", in, got)
		}
	}
}

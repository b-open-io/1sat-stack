package bsv21

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// newOwnerMimic mimics the index service owner HTTP routes:
// GET /sync?owner=<addr> (SSE, triggers background sync then closes) and
// GET /:owner/balance -> {"balance":N,"count":M}.
func newOwnerMimic(t *testing.T, balance uint64, count int) (*httptest.Server, *atomic.Pointer[string], *atomic.Pointer[string]) {
	t.Helper()
	syncedOwner := &atomic.Pointer[string]{}
	balanceOwner := &atomic.Pointer[string]{}

	mux := http.NewServeMux()
	mux.HandleFunc("/sync", func(w http.ResponseWriter, r *http.Request) {
		owner := r.URL.Query().Get("owner")
		syncedOwner.Store(&owner)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// Existing outputs would stream here; then the handler triggers its
		// background sync and closes with a done event.
		_, _ = io.WriteString(w, "event: done\ndata: {}\nretry: 60000\n\n")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/balance") {
			http.NotFound(w, r)
			return
		}
		owner := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/balance")
		balanceOwner.Store(&owner)
		_ = json.NewEncoder(w).Encode(map[string]any{"balance": balance, "count": count})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, syncedOwner, balanceOwner
}

func TestHTTPOwnerClientSync(t *testing.T) {
	srv, syncedOwner, _ := newOwnerMimic(t, 0, 0)
	client := NewHTTPOwnerClient(srv.URL, nil)

	const addr = "1SomeFeeAddressExample000000000000"
	if err := client.Sync(context.Background(), addr); err != nil {
		t.Fatalf("Sync = %v, want nil", err)
	}
	got := syncedOwner.Load()
	if got == nil || *got != addr {
		t.Fatalf("sync received owner = %v, want %s", got, addr)
	}
}

func TestHTTPOwnerClientBalance(t *testing.T) {
	srv, _, balanceOwner := newOwnerMimic(t, 4242, 3)
	client := NewHTTPOwnerClient(srv.URL, nil)

	const addr = "1SomeFeeAddressExample000000000000"
	credits, err := client.Balance(context.Background(), addr)
	if err != nil {
		t.Fatalf("Balance = %v, want nil err", err)
	}
	if credits != 4242 {
		t.Fatalf("Balance = %d, want 4242", credits)
	}
	got := balanceOwner.Load()
	if got == nil || *got != addr {
		t.Fatalf("balance received owner = %v, want %s", got, addr)
	}
}

// HTTPOwnerClient must satisfy the injected interfaces bsv21 depends on.
func TestHTTPOwnerClientSatisfiesInterfaces(t *testing.T) {
	var _ OwnerSyncer = (*HTTPOwnerClient)(nil)
	client := NewHTTPOwnerClient("http://example", nil)
	var _ BalanceLookup = client.Balance
}

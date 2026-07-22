package spends

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bsv-blockchain/go-sdk/transaction"
)

const (
	testOutpointA = "1111111111111111111111111111111111111111111111111111111111111111.0"
	testOutpointB = "2222222222222222222222222222222222222222222222222222222222222222.1"
	testSpendHex  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

// newSpendMimic returns an httptest.Server that mimics the txo spend endpoints
// (GET /:outpoint/spend, POST /spends) using the exact request/response
// encodings from pkg/txo/routes.go. known maps outpoint string → spend txid hex.
func newSpendMimic(t *testing.T, known map[string]string) *httptest.Server {
	t.Helper()
	type spendResp struct {
		SpendTxid *string `json:"spendTxid"`
	}
	mux := http.NewServeMux()

	mux.HandleFunc("/spends", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var ops []string
		if err := json.Unmarshal(body, &ops); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		out := make([]spendResp, len(ops))
		for i, op := range ops {
			if hex, ok := known[op]; ok {
				h := hex
				out[i].SpendTxid = &h
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/spend") {
			http.NotFound(w, r)
			return
		}
		op := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/spend")
		resp := spendResp{}
		if hex, ok := known[op]; ok {
			h := hex
			resp.SpendTxid = &h
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestHTTPSpendStorageGetSpend(t *testing.T) {
	srv := newSpendMimic(t, map[string]string{testOutpointA: testSpendHex})
	store := NewHTTPSpendStorage(srv.URL, nil)
	ctx := context.Background()

	opA, err := transaction.OutpointFromString(testOutpointA)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.GetSpend(ctx, opA)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected spend txid, got nil")
	}
	if got.String() != testSpendHex {
		t.Fatalf("GetSpend = %s, want %s", got.String(), testSpendHex)
	}

	// Unknown outpoint: real handler returns 200 with spendTxid null.
	opB, err := transaction.OutpointFromString(testOutpointB)
	if err != nil {
		t.Fatal(err)
	}
	got, err = store.GetSpend(ctx, opB)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("GetSpend for unspent = %v, want nil", got)
	}
}

func TestHTTPSpendStorageGetSpend404(t *testing.T) {
	// A server that 404s everything.
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	store := NewHTTPSpendStorage(srv.URL, nil)

	opA, _ := transaction.OutpointFromString(testOutpointA)
	got, err := store.GetSpend(context.Background(), opA)
	if err != nil {
		t.Fatalf("404 should be treated as no-spend, got err %v", err)
	}
	if got != nil {
		t.Fatalf("GetSpend on 404 = %v, want nil", got)
	}
}

func TestHTTPSpendStorageGetSpends(t *testing.T) {
	srv := newSpendMimic(t, map[string]string{testOutpointA: testSpendHex})
	store := NewHTTPSpendStorage(srv.URL, nil)
	ctx := context.Background()

	opA, _ := transaction.OutpointFromString(testOutpointA)
	opB, _ := transaction.OutpointFromString(testOutpointB)

	got, err := store.GetSpends(ctx, []*transaction.Outpoint{opA, opB})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("GetSpends len = %d, want 2", len(got))
	}
	if got[0] == nil || got[0].String() != testSpendHex {
		t.Fatalf("GetSpends[0] = %v, want %s", got[0], testSpendHex)
	}
	if got[1] != nil {
		t.Fatalf("GetSpends[1] = %v, want nil", got[1])
	}
}

func TestHTTPSpendStorageWritesAreNoOps(t *testing.T) {
	store := NewHTTPSpendStorage("http://127.0.0.1:0", nil)
	ctx := context.Background()
	opA, _ := transaction.OutpointFromString(testOutpointA)
	if err := store.PutSpend(ctx, opA, nil); err != nil {
		t.Fatalf("PutSpend = %v, want nil", err)
	}
	if err := store.DeleteSpend(ctx, opA); err != nil {
		t.Fatalf("DeleteSpend = %v, want nil", err)
	}
}

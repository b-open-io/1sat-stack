package arcadeclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetPolicy(t *testing.T) {
	want := PolicyResponse{
		Policy: Policy{
			MiningFee:               MiningFee{Satoshis: 100, Bytes: 1000},
			MaxTxSizePolicy:         10485760,
			MaxScriptSizePolicy:     500000,
			StandardFormatSupported: true,
		},
		Timestamp: "2026-09-09T19:59:13Z",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/policy" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(want)
	}))
	t.Cleanup(srv.Close)

	c := New(srv.URL, "", srv.Client(), nil)
	got, err := c.GetPolicy(context.Background())
	if err != nil {
		t.Fatalf("GetPolicy: %v", err)
	}
	if got.Policy.MiningFee.Satoshis != 100 || got.Policy.MiningFee.Bytes != 1000 {
		t.Fatalf("miningFee = %+v", got.Policy.MiningFee)
	}
	if got.Policy.MaxTxSizePolicy != 10485760 {
		t.Fatalf("max tx size = %d", got.Policy.MaxTxSizePolicy)
	}
}

func TestGetPolicyNonOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)

	c := New(srv.URL, "", srv.Client(), nil)
	_, err := c.GetPolicy(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

package arcadeclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSubmitBatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/txs" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Content-Type") != "application/octet-stream" {
			t.Errorf("content-type %s", r.Header.Get("Content-Type"))
		}
		body, _ := io.ReadAll(r.Body)
		if len(body) == 0 {
			t.Error("empty body")
		}
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(BatchSubmitResponse{Submitted: 2, Duplicates: 0, Total: 2})
	}))
	t.Cleanup(srv.Close)

	c := New(srv.URL, "", srv.Client(), nil)
	got, code, err := c.SubmitBatch(context.Background(), []byte{1, 2, 3}, SubmitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if code != http.StatusAccepted {
		t.Fatalf("code %d", code)
	}
	if got.Submitted != 2 || got.Total != 2 {
		t.Fatalf("got %+v", got)
	}
}

package ordfs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

func TestClientLoad(t *testing.T) {
	txidHex := "58b7558ea379f24266c7e2f5fe321992ad9a724fd7a87423ba412677179ccb25"
	txid, _ := chainhash.NewHashFromHex(txidHex)
	outpoint := &transaction.Outpoint{Txid: *txid, Index: 0}
	seq := -1

	tests := []struct {
		name     string
		req      *Request
		wantPath string
		status   int
		body     string
		wantErr  bool
		check    func(t *testing.T, resp *Response)
	}{
		{
			name:     "outpoint with latest seq",
			req:      &Request{Outpoint: outpoint, Seq: &seq, Map: true},
			wantPath: "/metadata/" + txidHex + "_0:-1",
			status:   http.StatusOK,
			body: `{"contentType":"application/op-ns","contentLength":5,"sequence":3,` +
				`"outpoint":"` + txidHex + `_1","origin":"` + txidHex + `_0",` +
				`"map":{"opns.idKey":"aa"}}`,
			check: func(t *testing.T, resp *Response) {
				if resp.Outpoint == nil || resp.Outpoint.Index != 1 {
					t.Errorf("unexpected outpoint: %v", resp.Outpoint)
				}
				if resp.Origin == nil || resp.Origin.Index != 0 {
					t.Errorf("unexpected origin: %v", resp.Origin)
				}
				if string(resp.Map) != `{"opns.idKey":"aa"}` {
					t.Errorf("unexpected map: %s", resp.Map)
				}
				if resp.Sequence != 3 {
					t.Errorf("unexpected sequence: %d", resp.Sequence)
				}
			},
		},
		{
			name:     "outpoint without seq",
			req:      &Request{Outpoint: outpoint},
			wantPath: "/metadata/" + txidHex + "_0",
			status:   http.StatusOK,
			body:     `{"contentType":"text/plain","sequence":0}`,
			check: func(t *testing.T, resp *Response) {
				if resp.ContentType != "text/plain" {
					t.Errorf("unexpected contentType: %s", resp.ContentType)
				}
			},
		},
		{
			name:     "not found",
			req:      &Request{Outpoint: outpoint},
			wantPath: "/metadata/" + txidHex + "_0",
			status:   http.StatusNotFound,
			body:     `{"error":"not found"}`,
			wantErr:  true,
		},
		{
			name:    "content request rejected",
			req:     &Request{Outpoint: outpoint, Content: true},
			wantErr: true,
		},
		{
			name:    "missing pointer",
			req:     &Request{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tt.wantPath {
					t.Errorf("unexpected path: got %s, want %s", r.URL.Path, tt.wantPath)
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			resp, err := NewClient(srv.URL, nil).Load(context.Background(), tt.req)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, resp)
			}
		})
	}
}

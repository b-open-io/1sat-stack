package beef

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bsv-blockchain/go-sdk/chainhash"
)

func TestHTTPBeefStorage(t *testing.T) {
	txidHex := "58b7558ea379f24266c7e2f5fe321992ad9a724fd7a87423ba412677179ccb25"
	txid, _ := chainhash.NewHashFromHex(txidHex)

	tests := []struct {
		name     string
		call     func(ctx context.Context, s *HTTPBeefStorage) ([]byte, error)
		wantPath string
		status   int
		body     []byte
		wantErr  error
	}{
		{
			name: "raw tx fetch",
			call: func(ctx context.Context, s *HTTPBeefStorage) ([]byte, error) {
				return s.GetRawTx(ctx, txid)
			},
			wantPath: "/" + txidHex + "/tx",
			status:   http.StatusOK,
			body:     []byte{0x01, 0x02},
		},
		{
			name: "proof fetch",
			call: func(ctx context.Context, s *HTTPBeefStorage) ([]byte, error) {
				return s.GetProof(ctx, txid)
			},
			wantPath: "/" + txidHex + "/proof",
			status:   http.StatusOK,
			body:     []byte{0x03},
		},
		{
			name: "not found maps to ErrNotFound",
			call: func(ctx context.Context, s *HTTPBeefStorage) ([]byte, error) {
				return s.GetRawTx(ctx, txid)
			},
			wantPath: "/" + txidHex + "/tx",
			status:   http.StatusNotFound,
			wantErr:  ErrNotFound,
		},
		{
			name: "invalid beef rejected",
			call: func(ctx context.Context, s *HTTPBeefStorage) ([]byte, error) {
				return s.Get(ctx, txid)
			},
			wantPath: "/" + txidHex,
			status:   http.StatusOK,
			body:     []byte("not beef"),
			wantErr:  errors.New("any"), // parse failure; exact error not asserted
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tt.wantPath {
					t.Errorf("unexpected path: got %s, want %s", r.URL.Path, tt.wantPath)
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write(tt.body)
			}))
			defer srv.Close()

			got, err := tt.call(context.Background(), NewHTTPBeefStorage(srv.URL, nil))
			if tt.wantErr != nil {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if errors.Is(tt.wantErr, ErrNotFound) && !errors.Is(err, ErrNotFound) {
					t.Fatalf("expected ErrNotFound, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(got) != string(tt.body) {
				t.Errorf("unexpected body: got %x, want %x", got, tt.body)
			}
		})
	}

	t.Run("put is a no-op", func(t *testing.T) {
		s := NewHTTPBeefStorage("http://unused", nil)
		if err := s.Put(context.Background(), txid, []byte{0x01}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

package beef

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// HTTPBeefStorage is a read-only provider backed by a remote 1sat-stack beef
// API (GET /:txid, /:txid/tx, /:txid/proof), e.g. https://api.1sat.app/1sat/beef.
type HTTPBeefStorage struct {
	baseURL string
	client  *http.Client
}

// NewHTTPBeefStorage creates an HTTP-backed BEEF storage. A nil client uses
// http.DefaultClient.
func NewHTTPBeefStorage(baseURL string, client *http.Client) *HTTPBeefStorage {
	if client == nil {
		client = http.DefaultClient
	}
	return &HTTPBeefStorage{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  client,
	}
}

func (t *HTTPBeefStorage) fetch(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("beef remote %s: unexpected status %d", path, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func (t *HTTPBeefStorage) Get(ctx context.Context, txid *chainhash.Hash) ([]byte, error) {
	beefBytes, err := t.fetch(ctx, "/"+txid.String())
	if err != nil {
		return nil, err
	}
	if _, _, _, err := transaction.ParseBeef(beefBytes); err != nil {
		return nil, err
	}
	return beefBytes, nil
}

func (t *HTTPBeefStorage) Put(ctx context.Context, txid *chainhash.Hash, beefBytes []byte) error {
	return nil // remote is read-only
}

func (t *HTTPBeefStorage) UpdateMerklePath(ctx context.Context, txid *chainhash.Hash) ([]byte, error) {
	return t.Get(ctx, txid)
}

func (t *HTTPBeefStorage) GetRawTx(ctx context.Context, txid *chainhash.Hash) ([]byte, error) {
	return t.fetch(ctx, "/"+txid.String()+"/tx")
}

func (t *HTTPBeefStorage) GetProof(ctx context.Context, txid *chainhash.Hash) ([]byte, error) {
	return t.fetch(ctx, "/"+txid.String()+"/proof")
}

func (t *HTTPBeefStorage) Close() error {
	return nil
}

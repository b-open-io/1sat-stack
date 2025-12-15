package beef

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

const MaxConcurrentRequests = 16

type JunglebusBeefStorage struct {
	junglebusURL string
	limiter      chan struct{}
}

func NewJunglebusBeefStorage(junglebusURL string) *JunglebusBeefStorage {
	if junglebusURL == "" {
		junglebusURL = os.Getenv("JUNGLEBUS")
	}

	if junglebusURL == "" {
		junglebusURL = "https://junglebus.gorillapool.io"
	}

	return &JunglebusBeefStorage{
		junglebusURL: junglebusURL,
		limiter:      make(chan struct{}, MaxConcurrentRequests),
	}
}

func (t *JunglebusBeefStorage) Get(ctx context.Context, txid *chainhash.Hash) ([]byte, error) {
	select {
	case t.limiter <- struct{}{}:
		defer func() { <-t.limiter }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	return t.fetchBeef(txid)
}

func (t *JunglebusBeefStorage) fetchBeef(txid *chainhash.Hash) ([]byte, error) {
	if t.junglebusURL == "" {
		return nil, ErrNotFound
	}

	txidStr := txid.String()
	url := fmt.Sprintf("%s/v1/transaction/beef/%s", t.junglebusURL, txidStr)
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return nil, ErrNotFound
	} else if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("http-err-%d-%s", resp.StatusCode, txidStr)
	}

	beefBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	_, _, _, err = transaction.ParseBeef(beefBytes)
	if err != nil {
		return nil, err
	}

	return beefBytes, nil
}

func (t *JunglebusBeefStorage) Put(ctx context.Context, txid *chainhash.Hash, beefBytes []byte) error {
	return nil // JungleBus is read-only
}

func (t *JunglebusBeefStorage) UpdateMerklePath(ctx context.Context, txid *chainhash.Hash) ([]byte, error) {
	return t.Get(ctx, txid)
}

func (t *JunglebusBeefStorage) GetRawTx(ctx context.Context, txid *chainhash.Hash) ([]byte, error) {
	select {
	case t.limiter <- struct{}{}:
		defer func() { <-t.limiter }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	if t.junglebusURL == "" {
		return nil, ErrNotFound
	}

	url := fmt.Sprintf("%s/v1/transaction/get/%s/bin", t.junglebusURL, txid.String())
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return nil, ErrNotFound
	} else if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("http-err-%d-%s", resp.StatusCode, txid.String())
	}

	return io.ReadAll(resp.Body)
}

func (t *JunglebusBeefStorage) GetProof(ctx context.Context, txid *chainhash.Hash) ([]byte, error) {
	select {
	case t.limiter <- struct{}{}:
		defer func() { <-t.limiter }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	if t.junglebusURL == "" {
		return nil, ErrNotFound
	}

	url := fmt.Sprintf("%s/v1/transaction/proof/%s/bin", t.junglebusURL, txid.String())
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return nil, ErrNotFound
	} else if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("http-err-%d-%s", resp.StatusCode, txid.String())
	}

	return io.ReadAll(resp.Body)
}

func (j *JunglebusBeefStorage) Close() error {
	return nil
}

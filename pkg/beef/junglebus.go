package beef

import (
	"context"
	"errors"

	"github.com/b-open-io/go-junglebus"
	"github.com/b-open-io/go-junglebus/transports"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

const MaxConcurrentRequests = 16

type JunglebusBeefStorage struct {
	client  *junglebus.Client
	limiter chan struct{}
}

// NewJunglebusBeefStorageWithClient creates a JungleBus storage using the provided client.
func NewJunglebusBeefStorageWithClient(client *junglebus.Client) *JunglebusBeefStorage {
	return &JunglebusBeefStorage{
		client:  client,
		limiter: make(chan struct{}, MaxConcurrentRequests),
	}
}

func (t *JunglebusBeefStorage) Get(ctx context.Context, txid *chainhash.Hash) ([]byte, error) {
	if t.client == nil {
		return nil, ErrNotFound
	}

	select {
	case t.limiter <- struct{}{}:
		defer func() { <-t.limiter }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	beefBytes, err := t.client.GetBeef(ctx, txid.String())
	if err != nil {
		if errors.Is(err, transports.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	// Validate the BEEF data
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
	if t.client == nil {
		return nil, ErrNotFound
	}

	select {
	case t.limiter <- struct{}{}:
		defer func() { <-t.limiter }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	rawTx, err := t.client.GetRawTransaction(ctx, txid.String())
	if err != nil {
		if errors.Is(err, transports.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	return rawTx, nil
}

func (t *JunglebusBeefStorage) GetProof(ctx context.Context, txid *chainhash.Hash) ([]byte, error) {
	if t.client == nil {
		return nil, ErrNotFound
	}

	select {
	case t.limiter <- struct{}{}:
		defer func() { <-t.limiter }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	proof, err := t.client.GetProof(ctx, txid.String())
	if err != nil {
		if errors.Is(err, transports.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	return proof, nil
}

func (j *JunglebusBeefStorage) Close() error {
	return nil
}

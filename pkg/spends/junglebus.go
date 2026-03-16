package spends

import (
	"context"

	"github.com/b-open-io/go-junglebus"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

type JunglebusSpendStorage struct {
	client *junglebus.Client
}

func NewJunglebusSpendStorage(client *junglebus.Client) *JunglebusSpendStorage {
	return &JunglebusSpendStorage{client: client}
}

func (j *JunglebusSpendStorage) GetSpend(ctx context.Context, outpoint *transaction.Outpoint) (*chainhash.Hash, error) {
	if j.client == nil {
		return nil, nil
	}

	spendBytes, err := j.client.GetSpend(ctx, outpoint.Txid.String(), outpoint.Index)
	if err != nil {
		return nil, err
	}
	if len(spendBytes) == 0 {
		return nil, nil
	}

	txid := &chainhash.Hash{}
	copy(txid[:], spendBytes)
	return txid, nil
}

func (j *JunglebusSpendStorage) PutSpend(ctx context.Context, outpoint *transaction.Outpoint, spendTxid *chainhash.Hash) error {
	return nil
}

func (j *JunglebusSpendStorage) Close() error {
	return nil
}

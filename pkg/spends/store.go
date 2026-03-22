package spends

import (
	"context"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/redis/go-redis/v9"
)

const spendHashKey = "spnd"

type StoreSpendStorage struct {
	client *redis.Client
}

func NewStoreSpendStorage(client *redis.Client) *StoreSpendStorage {
	return &StoreSpendStorage{client: client}
}

func (s *StoreSpendStorage) GetSpend(ctx context.Context, outpoint *transaction.Outpoint) (*chainhash.Hash, error) {
	spendBytes, err := s.client.HGet(ctx, spendHashKey, string(outpoint.Bytes())).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(spendBytes) != 32 {
		return nil, nil
	}
	txid := &chainhash.Hash{}
	copy(txid[:], spendBytes)
	return txid, nil
}

func (s *StoreSpendStorage) GetSpends(ctx context.Context, outpoints []*transaction.Outpoint) ([]*chainhash.Hash, error) {
	if len(outpoints) == 0 {
		return nil, nil
	}
	fields := make([]string, len(outpoints))
	for i, op := range outpoints {
		if op != nil {
			fields[i] = string(op.Bytes())
		}
	}
	values, err := s.client.HMGet(ctx, spendHashKey, fields...).Result()
	if err != nil {
		return nil, err
	}
	results := make([]*chainhash.Hash, len(outpoints))
	for i, v := range values {
		if v != nil {
			if b, ok := v.(string); ok && len(b) == 32 {
				txid := &chainhash.Hash{}
				copy(txid[:], b)
				results[i] = txid
			}
		}
	}
	return results, nil
}

func (s *StoreSpendStorage) PutSpend(ctx context.Context, outpoint *transaction.Outpoint, spendTxid *chainhash.Hash) error {
	return s.client.HSet(ctx, spendHashKey, string(outpoint.Bytes()), spendTxid[:]).Err()
}

func (s *StoreSpendStorage) DeleteSpend(ctx context.Context, outpoint *transaction.Outpoint) error {
	return s.client.HDel(ctx, spendHashKey, string(outpoint.Bytes())).Err()
}

func (s *StoreSpendStorage) Close() error {
	return nil
}

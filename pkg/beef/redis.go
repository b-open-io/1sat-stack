package beef

import (
	"context"
	"errors"
	"time"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/redis/go-redis/v9"
)

// Key prefixes for Redis storage
const (
	redisTxPrefix    = "bf:tx:"
	redisProofPrefix = "bf:pf:"
)

// RedisBeefStorage implements BaseBeefStorage using Redis
type RedisBeefStorage struct {
	client *redis.Client
	ttl    time.Duration // 0 means no expiration
}

// RedisBeefConfig holds Redis beef storage configuration
type RedisBeefConfig struct {
	URL string `mapstructure:"url"` // e.g., "redis://user:pass@localhost:6379/0"
	TTL string `mapstructure:"ttl"` // e.g., "24h", "0" or "" for no expiration
}

// NewRedisBeefStorage creates a new Redis-backed beef storage
func NewRedisBeefStorage(cfg *RedisBeefConfig) (*RedisBeefStorage, error) {
	opts, err := redis.ParseURL(cfg.URL)
	if err != nil {
		return nil, err
	}
	client := redis.NewClient(opts)

	// Test connection
	if err := client.Ping(context.Background()).Err(); err != nil {
		return nil, err
	}

	var ttl time.Duration
	if cfg.TTL != "" && cfg.TTL != "0" {
		var err error
		ttl, err = time.ParseDuration(cfg.TTL)
		if err != nil {
			return nil, err
		}
	}

	return &RedisBeefStorage{
		client: client,
		ttl:    ttl,
	}, nil
}

// txKey returns the Redis key for a transaction
func txKey(txid *chainhash.Hash) string {
	return redisTxPrefix + txid.String()
}

// proofKey returns the Redis key for a merkle proof
func proofKey(txid *chainhash.Hash) string {
	return redisProofPrefix + txid.String()
}

// Get retrieves BEEF bytes for a transaction
func (r *RedisBeefStorage) Get(ctx context.Context, txid *chainhash.Hash) ([]byte, error) {
	rawTx, err := r.client.Get(ctx, txKey(txid)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	// Get proof (may not exist)
	proof, err := r.client.Get(ctx, proofKey(txid)).Bytes()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}

	return assembleBEEF(txid, rawTx, proof)
}

// Put stores BEEF bytes for a transaction
func (r *RedisBeefStorage) Put(ctx context.Context, txid *chainhash.Hash, beefBytes []byte) error {
	rawTx, proof, err := splitBEEF(beefBytes)
	if err != nil {
		return err
	}

	// Store raw transaction
	if err := r.client.Set(ctx, txKey(txid), rawTx, r.ttl).Err(); err != nil {
		return err
	}

	// Store proof if present
	if len(proof) > 0 {
		if err := r.client.Set(ctx, proofKey(txid), proof, r.ttl).Err(); err != nil {
			return err
		}
	}

	return nil
}

// UpdateMerklePath is a no-op for Redis storage - proofs come from other sources
func (r *RedisBeefStorage) UpdateMerklePath(ctx context.Context, txid *chainhash.Hash) ([]byte, error) {
	return nil, nil
}

// GetRawTx retrieves just the raw transaction bytes
func (r *RedisBeefStorage) GetRawTx(ctx context.Context, txid *chainhash.Hash) ([]byte, error) {
	rawTx, err := r.client.Get(ctx, txKey(txid)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrNotFound
	}
	return rawTx, err
}

// GetProof retrieves just the merkle proof bytes
func (r *RedisBeefStorage) GetProof(ctx context.Context, txid *chainhash.Hash) ([]byte, error) {
	proof, err := r.client.Get(ctx, proofKey(txid)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrNotFound
	}
	return proof, err
}

// Close closes the Redis connection
func (r *RedisBeefStorage) Close() error {
	return r.client.Close()
}

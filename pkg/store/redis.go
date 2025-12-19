package store

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"strconv"

	"github.com/redis/go-redis/v9"
)

// RedisStore implements Store using Redis
type RedisStore struct {
	client *redis.Client
	logger *slog.Logger
}

// RedisConfig holds Redis-specific configuration
type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

// NewRedisStore creates a new Redis-backed Store
func NewRedisStore(cfg *RedisConfig, logger *slog.Logger) (*RedisStore, error) {
	if logger == nil {
		logger = slog.Default()
	}

	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	// Test connection
	if err := client.Ping(context.Background()).Err(); err != nil {
		return nil, err
	}

	logger.Info("connected to Redis", "addr", cfg.Addr, "db", cfg.DB)

	return &RedisStore{
		client: client,
		logger: logger,
	}, nil
}

func (s *RedisStore) Close() error {
	return s.client.Close()
}

// KV Operations

func (s *RedisStore) Get(ctx context.Context, key []byte) ([]byte, error) {
	val, err := s.client.Get(ctx, string(key)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrKeyNotFound
	}
	return val, err
}

func (s *RedisStore) Set(ctx context.Context, key, value []byte) error {
	return s.client.Set(ctx, string(key), value, 0).Err()
}

func (s *RedisStore) Del(ctx context.Context, key []byte) error {
	return s.client.Del(ctx, string(key)).Err()
}

func (s *RedisStore) Scan(ctx context.Context, prefix []byte, limit int) ([]KV, error) {
	var results []KV
	var cursor uint64
	pattern := string(prefix) + "*"

	for {
		keys, nextCursor, err := s.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, err
		}

		for _, k := range keys {
			if limit > 0 && len(results) >= limit {
				return results, nil
			}
			val, err := s.client.Get(ctx, k).Bytes()
			if err != nil && !errors.Is(err, redis.Nil) {
				return nil, err
			}
			results = append(results, KV{Key: []byte(k), Value: val})
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	if results == nil {
		results = []KV{}
	}
	return results, nil
}

// Set Operations

func (s *RedisStore) SAdd(ctx context.Context, key []byte, members ...[]byte) error {
	args := make([]interface{}, len(members))
	for i, m := range members {
		args[i] = string(m)
	}
	return s.client.SAdd(ctx, string(key), args...).Err()
}

func (s *RedisStore) SMembers(ctx context.Context, key []byte) ([][]byte, error) {
	vals, err := s.client.SMembers(ctx, string(key)).Result()
	if err != nil {
		return nil, err
	}
	members := make([][]byte, len(vals))
	for i, v := range vals {
		members[i] = []byte(v)
	}
	return members, nil
}

func (s *RedisStore) SRem(ctx context.Context, key []byte, members ...[]byte) error {
	args := make([]interface{}, len(members))
	for i, m := range members {
		args[i] = string(m)
	}
	return s.client.SRem(ctx, string(key), args...).Err()
}

func (s *RedisStore) SIsMember(ctx context.Context, key, member []byte) (bool, error) {
	return s.client.SIsMember(ctx, string(key), string(member)).Result()
}

// Hash Operations

func (s *RedisStore) HSet(ctx context.Context, key, field, value []byte) error {
	return s.client.HSet(ctx, string(key), string(field), value).Err()
}

func (s *RedisStore) HGet(ctx context.Context, key, field []byte) ([]byte, error) {
	val, err := s.client.HGet(ctx, string(key), string(field)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrKeyNotFound
	}
	return val, err
}

func (s *RedisStore) HGetAll(ctx context.Context, key []byte) (map[string][]byte, error) {
	vals, err := s.client.HGetAll(ctx, string(key)).Result()
	if err != nil {
		return nil, err
	}
	result := make(map[string][]byte, len(vals))
	for k, v := range vals {
		result[k] = []byte(v)
	}
	return result, nil
}

func (s *RedisStore) HDel(ctx context.Context, key []byte, fields ...[]byte) error {
	args := make([]string, len(fields))
	for i, f := range fields {
		args[i] = string(f)
	}
	return s.client.HDel(ctx, string(key), args...).Err()
}

func (s *RedisStore) HMSet(ctx context.Context, key []byte, fields map[string][]byte) error {
	if len(fields) == 0 {
		return nil
	}
	args := make([]interface{}, 0, len(fields)*2)
	for k, v := range fields {
		args = append(args, k, v)
	}
	return s.client.HMSet(ctx, string(key), args...).Err()
}

func (s *RedisStore) HMGet(ctx context.Context, key []byte, fields ...[]byte) ([][]byte, error) {
	if len(fields) == 0 {
		return [][]byte{}, nil
	}
	args := make([]string, len(fields))
	for i, f := range fields {
		args[i] = string(f)
	}
	vals, err := s.client.HMGet(ctx, string(key), args...).Result()
	if err != nil {
		return nil, err
	}
	result := make([][]byte, len(vals))
	for i, v := range vals {
		if v != nil {
			result[i] = []byte(v.(string))
		}
	}
	return result, nil
}

// Sorted Set Operations

func (s *RedisStore) ZAdd(ctx context.Context, key []byte, members ...ScoredMember) error {
	zMembers := make([]redis.Z, len(members))
	for i, m := range members {
		zMembers[i] = redis.Z{
			Score:  m.Score,
			Member: string(m.Member),
		}
	}
	return s.client.ZAdd(ctx, string(key), zMembers...).Err()
}

func (s *RedisStore) ZRem(ctx context.Context, key []byte, members ...[]byte) error {
	args := make([]interface{}, len(members))
	for i, m := range members {
		args[i] = string(m)
	}
	return s.client.ZRem(ctx, string(key), args...).Err()
}

func (s *RedisStore) ZRange(ctx context.Context, key []byte, scoreRange ScoreRange) ([]ScoredMember, error) {
	return s.zRangeInternal(ctx, key, scoreRange, false)
}

func (s *RedisStore) ZRevRange(ctx context.Context, key []byte, scoreRange ScoreRange) ([]ScoredMember, error) {
	return s.zRangeInternal(ctx, key, scoreRange, true)
}

func (s *RedisStore) zRangeInternal(ctx context.Context, key []byte, scoreRange ScoreRange, reverse bool) ([]ScoredMember, error) {
	min := "-inf"
	max := "+inf"

	if scoreRange.Min != nil {
		if scoreRange.MinExclusive {
			min = "(" + floatToString(*scoreRange.Min)
		} else {
			min = floatToString(*scoreRange.Min)
		}
	}

	if scoreRange.Max != nil {
		if scoreRange.MaxExclusive {
			max = "(" + floatToString(*scoreRange.Max)
		} else {
			max = floatToString(*scoreRange.Max)
		}
	}

	opt := &redis.ZRangeBy{
		Min:    min,
		Max:    max,
		Offset: scoreRange.Offset,
		Count:  scoreRange.Count,
	}

	var vals []redis.Z
	var err error

	if reverse {
		vals, err = s.client.ZRevRangeByScoreWithScores(ctx, string(key), opt).Result()
	} else {
		vals, err = s.client.ZRangeByScoreWithScores(ctx, string(key), opt).Result()
	}

	if err != nil {
		return nil, err
	}

	members := make([]ScoredMember, len(vals))
	for i, v := range vals {
		members[i] = ScoredMember{
			Member: []byte(v.Member.(string)),
			Score:  v.Score,
		}
	}
	return members, nil
}

func (s *RedisStore) ZScore(ctx context.Context, key, member []byte) (float64, error) {
	score, err := s.client.ZScore(ctx, string(key), string(member)).Result()
	if errors.Is(err, redis.Nil) {
		return 0, ErrKeyNotFound
	}
	return score, err
}

func (s *RedisStore) ZCard(ctx context.Context, key []byte) (int64, error) {
	return s.client.ZCard(ctx, string(key)).Result()
}

func (s *RedisStore) ZIncrBy(ctx context.Context, key, member []byte, increment float64) (float64, error) {
	return s.client.ZIncrBy(ctx, string(key), increment, string(member)).Result()
}

func (s *RedisStore) ZSum(ctx context.Context, key []byte) (float64, error) {
	// Redis doesn't have a native ZSUM, so we fetch all scores and sum
	vals, err := s.client.ZRangeWithScores(ctx, string(key), 0, -1).Result()
	if err != nil {
		return 0, err
	}
	var sum float64
	for _, v := range vals {
		sum += v.Score
	}
	return sum, nil
}

func (s *RedisStore) Search(ctx context.Context, cfg *SearchCfg) ([]ScoredMember, error) {
	return Search(ctx, s, cfg)
}

func (s *RedisStore) ZKeys(ctx context.Context, prefix []byte) ([]string, error) {
	var keys []string
	var cursor uint64
	pattern := string(prefix) + "*"

	for {
		result, nextCursor, err := s.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, err
		}

		// Filter to only include sorted set keys
		for _, k := range result {
			keyType, err := s.client.Type(ctx, k).Result()
			if err != nil {
				continue
			}
			if keyType == "zset" {
				keys = append(keys, k)
			}
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	return keys, nil
}

func floatToString(f float64) string {
	if math.IsInf(f, 1) {
		return "+inf"
	}
	if math.IsInf(f, -1) {
		return "-inf"
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

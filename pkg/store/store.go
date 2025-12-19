package store

import "context"

// ScoredMember represents a member with its score in sorted set operations
type ScoredMember struct {
	Member []byte
	Score  float64
	Key    []byte // Which key this result came from (useful for multi-key searches)
}

// JoinType defines how multiple keys are combined in searches
type JoinType int

const (
	JoinUnion      JoinType = iota // Any key matches (OR)
	JoinIntersect                  // All keys must match (AND)
	JoinDifference                 // First key minus others
)

// SearchCfg defines parameters for multi-key sorted set searches
type SearchCfg struct {
	Keys     [][]byte // Keys to search across (required)
	From     *float64 // Start score (nil = -inf)
	To       *float64 // End score (nil = +inf)
	Limit    uint32   // Max results (0 = 1000 default)
	JoinType JoinType // How to combine results from multiple keys
	Reverse  bool     // true = descending order
}

// ScoreRange defines range parameters for sorted set queries
type ScoreRange struct {
	Min          *float64 // nil = -inf
	Max          *float64 // nil = +inf
	MinExclusive bool     // true = exclude Min value (> instead of >=)
	MaxExclusive bool     // true = exclude Max value (< instead of <=)
	Offset       int64    // 0 = start from beginning
	Count        int64    // 0 = all (default), positive = limit
}

// KV represents a key-value pair for scan operations
type KV struct {
	Key   []byte
	Value []byte
}

// Store provides Redis-like operations for queues, caches, and configuration.
// This is the core storage interface used by all packages.
type Store interface {
	// KV Operations - for simple key-value storage
	Get(ctx context.Context, key []byte) ([]byte, error)
	Set(ctx context.Context, key, value []byte) error
	Del(ctx context.Context, key []byte) error
	Scan(ctx context.Context, prefix []byte, limit int) ([]KV, error)

	// Set Operations - for whitelists, topic management, etc.
	SAdd(ctx context.Context, key []byte, members ...[]byte) error
	SMembers(ctx context.Context, key []byte) ([][]byte, error)
	SRem(ctx context.Context, key []byte, members ...[]byte) error
	SIsMember(ctx context.Context, key, member []byte) (bool, error)

	// Hash Operations - for peer configurations, output data, etc.
	HSet(ctx context.Context, key, field, value []byte) error
	HGet(ctx context.Context, key, field []byte) ([]byte, error)
	HGetAll(ctx context.Context, key []byte) (map[string][]byte, error)
	HDel(ctx context.Context, key []byte, fields ...[]byte) error
	HMSet(ctx context.Context, key []byte, fields map[string][]byte) error
	HMGet(ctx context.Context, key []byte, fields ...[]byte) ([][]byte, error)

	// Sorted Set Operations - for queues, progress tracking, fee balances, etc.
	ZAdd(ctx context.Context, key []byte, members ...ScoredMember) error
	ZRem(ctx context.Context, key []byte, members ...[]byte) error
	ZRange(ctx context.Context, key []byte, scoreRange ScoreRange) ([]ScoredMember, error)
	ZRevRange(ctx context.Context, key []byte, scoreRange ScoreRange) ([]ScoredMember, error)
	ZScore(ctx context.Context, key, member []byte) (float64, error)
	ZCard(ctx context.Context, key []byte) (int64, error)
	ZIncrBy(ctx context.Context, key, member []byte, increment float64) (float64, error)
	ZSum(ctx context.Context, key []byte) (float64, error)

	// Search performs a multi-key sorted set search with cursor-based merge.
	Search(ctx context.Context, cfg *SearchCfg) ([]ScoredMember, error)

	// ZKeys returns all sorted set keys matching a prefix
	ZKeys(ctx context.Context, prefix []byte) ([]string, error)

	// Resource management
	Close() error
}

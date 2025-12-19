package store

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/dgraph-io/badger/v4"
)

// expandPath expands ~ to the user's home directory
func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	} else if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	return path
}

// Key prefixes for different data types
var (
	prefixSet        = []byte("set:")
	prefixHash       = []byte("hash:")
	prefixZSetScore  = []byte("zset:score:")
	prefixZSetMember = []byte("zset:member:")
)

// ErrKeyNotFound is returned when a key doesn't exist
var ErrKeyNotFound = errors.New("key not found")

// maxRetries is the number of times to retry a transaction on conflict
const maxRetries = 10

// BadgerStore implements Store using BadgerDB
type BadgerStore struct {
	db     *badger.DB
	logger *slog.Logger
}

// update wraps db.Update with retry logic for transaction conflicts.
func (s *BadgerStore) update(fn func(txn *badger.Txn) error) error {
	for i := 0; i < maxRetries; i++ {
		err := s.db.Update(fn)
		if err == nil {
			return nil
		}
		if errors.Is(err, badger.ErrConflict) {
			continue
		}
		return err
	}
	return badger.ErrConflict
}

// slogAdapter adapts slog.Logger to badger.Logger interface
type slogAdapter struct {
	logger *slog.Logger
}

func (s *slogAdapter) Errorf(format string, args ...interface{}) {
	s.logger.Error(fmt.Sprintf(format, args...))
}

func (s *slogAdapter) Warningf(format string, args ...interface{}) {
	s.logger.Warn(fmt.Sprintf(format, args...))
}

func (s *slogAdapter) Infof(format string, args ...interface{}) {
	s.logger.Info(fmt.Sprintf(format, args...))
}

func (s *slogAdapter) Debugf(format string, args ...interface{}) {
	s.logger.Debug(fmt.Sprintf(format, args...))
}

// NewBadgerStoreFromConfig creates a new BadgerDB-backed Store from config.
func NewBadgerStoreFromConfig(cfg *BadgerConfig, logger *slog.Logger) (*BadgerStore, error) {
	if logger == nil {
		logger = slog.Default()
	}

	path := expandPath(cfg.Path)

	var opts badger.Options
	if cfg.InMemory {
		opts = badger.DefaultOptions("").WithInMemory(true)
	} else {
		if path == "" {
			return nil, fmt.Errorf("path required for disk-based storage")
		}
		opts = badger.DefaultOptions(path)
	}

	opts = opts.WithLogger(&slogAdapter{logger: logger})

	logger.Info("opening BadgerDB", "path", path, "inMemory", cfg.InMemory)

	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open badger db: %w", err)
	}

	return &BadgerStore{
		db:     db,
		logger: logger,
	}, nil
}

// NewBadgerStore creates a new BadgerDB-backed Store.
// Deprecated: Use NewBadgerStoreFromConfig instead.
// Connection string format:
//   - badger:///path/to/db - disk-based storage
//   - badger://~/.1sat/store - disk-based storage with home directory expansion
//   - badger://?memory=true - in-memory storage
func NewBadgerStore(connString string, logger *slog.Logger) (*BadgerStore, error) {
	if logger == nil {
		logger = slog.Default()
	}

	u, err := url.Parse(connString)
	if err != nil {
		return nil, fmt.Errorf("invalid connection string: %w", err)
	}

	if u.Scheme != "badger" {
		return nil, fmt.Errorf("invalid scheme: expected 'badger', got '%s'", u.Scheme)
	}

	inMemory := u.Query().Get("memory") == "true"

	// Reconstruct path - URL parsing puts ~ in Host field
	// badger://~/.1sat/store -> Host="~", Path="/.1sat/store"
	path := u.Path
	if u.Host == "~" {
		path = "~" + path
	}
	path = expandPath(path)

	var opts badger.Options
	if inMemory {
		opts = badger.DefaultOptions("").WithInMemory(true)
	} else {
		if path == "" {
			return nil, fmt.Errorf("path required for disk-based storage")
		}
		opts = badger.DefaultOptions(path)
	}

	opts = opts.WithLogger(&slogAdapter{logger: logger})

	logger.Info("opening BadgerDB", "path", path, "inMemory", inMemory)

	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open badger db: %w", err)
	}

	return &BadgerStore{
		db:     db,
		logger: logger,
	}, nil
}

func (s *BadgerStore) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// Key construction helpers

func setKey(key, member []byte) []byte {
	buf := make([]byte, 0, len(prefixSet)+len(key)+1+len(member))
	buf = append(buf, prefixSet...)
	buf = append(buf, key...)
	buf = append(buf, ':')
	buf = append(buf, member...)
	return buf
}

func setPrefix(key []byte) []byte {
	buf := make([]byte, 0, len(prefixSet)+len(key)+1)
	buf = append(buf, prefixSet...)
	buf = append(buf, key...)
	buf = append(buf, ':')
	return buf
}

func hashKey(key, field []byte) []byte {
	buf := make([]byte, 0, len(prefixHash)+len(key)+1+len(field))
	buf = append(buf, prefixHash...)
	buf = append(buf, key...)
	buf = append(buf, ':')
	buf = append(buf, field...)
	return buf
}

func hashPrefix(key []byte) []byte {
	buf := make([]byte, 0, len(prefixHash)+len(key)+1)
	buf = append(buf, prefixHash...)
	buf = append(buf, key...)
	buf = append(buf, ':')
	return buf
}

func zsetScoreKey(key []byte, score float64, member []byte) []byte {
	buf := make([]byte, 0, len(prefixZSetScore)+len(key)+1+8+1+len(member))
	buf = append(buf, prefixZSetScore...)
	buf = append(buf, key...)
	buf = append(buf, ':')
	buf = append(buf, encodeFloat64(score)...)
	buf = append(buf, ':')
	buf = append(buf, member...)
	return buf
}

func zsetScorePrefix(key []byte) []byte {
	buf := make([]byte, 0, len(prefixZSetScore)+len(key)+1)
	buf = append(buf, prefixZSetScore...)
	buf = append(buf, key...)
	buf = append(buf, ':')
	return buf
}

func zsetMemberKey(key, member []byte) []byte {
	buf := make([]byte, 0, len(prefixZSetMember)+len(key)+1+len(member))
	buf = append(buf, prefixZSetMember...)
	buf = append(buf, key...)
	buf = append(buf, ':')
	buf = append(buf, member...)
	return buf
}

func zsetMemberPrefix(key []byte) []byte {
	buf := make([]byte, 0, len(prefixZSetMember)+len(key)+1)
	buf = append(buf, prefixZSetMember...)
	buf = append(buf, key...)
	buf = append(buf, ':')
	return buf
}

// Float64 sortable encoding

func encodeFloat64(f float64) []byte {
	bits := math.Float64bits(f)
	if f >= 0 {
		bits ^= 1 << 63
	} else {
		bits ^= 0xFFFFFFFFFFFFFFFF
	}
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, bits)
	return buf
}

func decodeFloat64(b []byte) float64 {
	if len(b) != 8 {
		return 0
	}
	bits := binary.BigEndian.Uint64(b)
	if bits&(1<<63) != 0 {
		bits ^= 1 << 63
	} else {
		bits ^= 0xFFFFFFFFFFFFFFFF
	}
	return math.Float64frombits(bits)
}

// Set Operations

func (s *BadgerStore) SAdd(ctx context.Context, key []byte, members ...[]byte) error {
	return s.update(func(txn *badger.Txn) error {
		for _, member := range members {
			if err := txn.Set(setKey(key, member), nil); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *BadgerStore) SMembers(ctx context.Context, key []byte) ([][]byte, error) {
	var members [][]byte
	prefix := setPrefix(key)

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = prefix
		opts.PrefetchValues = false

		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			k := item.Key()
			member := bytes.Clone(k[len(prefix):])
			members = append(members, member)
		}
		return nil
	})

	if err != nil {
		return nil, err
	}
	if members == nil {
		members = [][]byte{}
	}
	return members, nil
}

func (s *BadgerStore) SRem(ctx context.Context, key []byte, members ...[]byte) error {
	return s.update(func(txn *badger.Txn) error {
		for _, member := range members {
			if err := txn.Delete(setKey(key, member)); err != nil && !errors.Is(err, badger.ErrKeyNotFound) {
				return err
			}
		}
		return nil
	})
}

func (s *BadgerStore) SIsMember(ctx context.Context, key, member []byte) (bool, error) {
	var exists bool
	err := s.db.View(func(txn *badger.Txn) error {
		_, err := txn.Get(setKey(key, member))
		if err == nil {
			exists = true
			return nil
		}
		if errors.Is(err, badger.ErrKeyNotFound) {
			exists = false
			return nil
		}
		return err
	})
	return exists, err
}

// Hash Operations

func (s *BadgerStore) HSet(ctx context.Context, key, field, value []byte) error {
	return s.update(func(txn *badger.Txn) error {
		return txn.Set(hashKey(key, field), value)
	})
}

func (s *BadgerStore) HGet(ctx context.Context, key, field []byte) ([]byte, error) {
	var value []byte
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(hashKey(key, field))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			value = bytes.Clone(val)
			return nil
		})
	})
	if errors.Is(err, badger.ErrKeyNotFound) {
		return nil, ErrKeyNotFound
	}
	return value, err
}

func (s *BadgerStore) HGetAll(ctx context.Context, key []byte) (map[string][]byte, error) {
	result := make(map[string][]byte)
	prefix := hashPrefix(key)

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = prefix

		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			k := item.Key()
			field := string(k[len(prefix):])

			err := item.Value(func(val []byte) error {
				result[field] = bytes.Clone(val)
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})

	return result, err
}

func (s *BadgerStore) HDel(ctx context.Context, key []byte, fields ...[]byte) error {
	return s.update(func(txn *badger.Txn) error {
		for _, field := range fields {
			if err := txn.Delete(hashKey(key, field)); err != nil && !errors.Is(err, badger.ErrKeyNotFound) {
				return err
			}
		}
		return nil
	})
}

func (s *BadgerStore) HMSet(ctx context.Context, key []byte, fields map[string][]byte) error {
	if len(fields) == 0 {
		return nil
	}
	return s.update(func(txn *badger.Txn) error {
		for field, value := range fields {
			if err := txn.Set(hashKey(key, []byte(field)), value); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *BadgerStore) HMGet(ctx context.Context, key []byte, fields ...[]byte) ([][]byte, error) {
	if len(fields) == 0 {
		return [][]byte{}, nil
	}

	values := make([][]byte, len(fields))
	err := s.db.View(func(txn *badger.Txn) error {
		for i, field := range fields {
			item, err := txn.Get(hashKey(key, field))
			if err != nil {
				if errors.Is(err, badger.ErrKeyNotFound) {
					values[i] = nil
					continue
				}
				return err
			}
			err = item.Value(func(val []byte) error {
				values[i] = bytes.Clone(val)
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})

	return values, err
}

// Sorted Set Operations

func (s *BadgerStore) ZAdd(ctx context.Context, key []byte, members ...ScoredMember) error {
	return s.update(func(txn *badger.Txn) error {
		for _, m := range members {
			memberKey := zsetMemberKey(key, m.Member)

			item, err := txn.Get(memberKey)
			if err == nil {
				var oldScore float64
				err = item.Value(func(val []byte) error {
					oldScore = decodeFloat64(val)
					return nil
				})
				if err != nil {
					return err
				}
				oldScoreKey := zsetScoreKey(key, oldScore, m.Member)
				if err := txn.Delete(oldScoreKey); err != nil && !errors.Is(err, badger.ErrKeyNotFound) {
					return err
				}
			} else if !errors.Is(err, badger.ErrKeyNotFound) {
				return err
			}

			newScoreKey := zsetScoreKey(key, m.Score, m.Member)
			if err := txn.Set(newScoreKey, nil); err != nil {
				return err
			}

			if err := txn.Set(memberKey, encodeFloat64(m.Score)); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *BadgerStore) ZRem(ctx context.Context, key []byte, members ...[]byte) error {
	return s.update(func(txn *badger.Txn) error {
		for _, member := range members {
			memberKey := zsetMemberKey(key, member)

			item, err := txn.Get(memberKey)
			if errors.Is(err, badger.ErrKeyNotFound) {
				continue
			}
			if err != nil {
				return err
			}

			var oldScore float64
			err = item.Value(func(val []byte) error {
				oldScore = decodeFloat64(val)
				return nil
			})
			if err != nil {
				return err
			}

			oldScoreKey := zsetScoreKey(key, oldScore, member)
			if err := txn.Delete(oldScoreKey); err != nil && !errors.Is(err, badger.ErrKeyNotFound) {
				return err
			}
			if err := txn.Delete(memberKey); err != nil && !errors.Is(err, badger.ErrKeyNotFound) {
				return err
			}
		}
		return nil
	})
}

func (s *BadgerStore) ZRange(ctx context.Context, key []byte, scoreRange ScoreRange) ([]ScoredMember, error) {
	return s.zRangeInternal(ctx, key, scoreRange, false)
}

func (s *BadgerStore) ZRevRange(ctx context.Context, key []byte, scoreRange ScoreRange) ([]ScoredMember, error) {
	return s.zRangeInternal(ctx, key, scoreRange, true)
}

func (s *BadgerStore) zRangeInternal(ctx context.Context, key []byte, scoreRange ScoreRange, reverse bool) ([]ScoredMember, error) {
	var members []ScoredMember
	prefix := zsetScorePrefix(key)

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = prefix
		opts.PrefetchValues = false
		opts.Reverse = reverse

		it := txn.NewIterator(opts)
		defer it.Close()

		var skipped int64
		var collected int64

		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			k := item.Key()

			afterPrefix := k[len(prefix):]
			if len(afterPrefix) < 9 {
				continue
			}

			score := decodeFloat64(afterPrefix[:8])
			member := bytes.Clone(afterPrefix[9:])

			if scoreRange.Min != nil {
				if scoreRange.MinExclusive {
					if score <= *scoreRange.Min {
						continue
					}
				} else {
					if score < *scoreRange.Min {
						continue
					}
				}
			}

			if scoreRange.Max != nil {
				if scoreRange.MaxExclusive {
					if score >= *scoreRange.Max {
						continue
					}
				} else {
					if score > *scoreRange.Max {
						continue
					}
				}
			}

			if skipped < scoreRange.Offset {
				skipped++
				continue
			}

			if scoreRange.Count > 0 && collected >= scoreRange.Count {
				break
			}

			members = append(members, ScoredMember{
				Member: member,
				Score:  score,
			})
			collected++
		}
		return nil
	})

	if err != nil {
		return nil, err
	}
	if members == nil {
		members = []ScoredMember{}
	}
	return members, nil
}

func (s *BadgerStore) ZScore(ctx context.Context, key, member []byte) (float64, error) {
	var score float64
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(zsetMemberKey(key, member))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			score = decodeFloat64(val)
			return nil
		})
	})
	if errors.Is(err, badger.ErrKeyNotFound) {
		return 0, ErrKeyNotFound
	}
	return score, err
}

func (s *BadgerStore) ZCard(ctx context.Context, key []byte) (int64, error) {
	var count int64
	prefix := zsetMemberPrefix(key)

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = prefix
		opts.PrefetchValues = false

		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			count++
		}
		return nil
	})

	return count, err
}

func (s *BadgerStore) ZIncrBy(ctx context.Context, key, member []byte, increment float64) (float64, error) {
	var newScore float64

	err := s.update(func(txn *badger.Txn) error {
		memberKey := zsetMemberKey(key, member)
		var oldScore float64
		var hasOldScore bool

		item, err := txn.Get(memberKey)
		if err == nil {
			hasOldScore = true
			err = item.Value(func(val []byte) error {
				oldScore = decodeFloat64(val)
				return nil
			})
			if err != nil {
				return err
			}
		} else if !errors.Is(err, badger.ErrKeyNotFound) {
			return err
		}

		newScore = oldScore + increment

		if hasOldScore {
			oldScoreKey := zsetScoreKey(key, oldScore, member)
			if err := txn.Delete(oldScoreKey); err != nil && !errors.Is(err, badger.ErrKeyNotFound) {
				return err
			}
		}

		newScoreKey := zsetScoreKey(key, newScore, member)
		if err := txn.Set(newScoreKey, nil); err != nil {
			return err
		}

		if err := txn.Set(memberKey, encodeFloat64(newScore)); err != nil {
			return err
		}

		return nil
	})

	return newScore, err
}

func (s *BadgerStore) ZSum(ctx context.Context, key []byte) (float64, error) {
	var sum float64
	prefix := zsetMemberPrefix(key)

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = prefix

		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			err := item.Value(func(val []byte) error {
				sum += decodeFloat64(val)
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})

	return sum, err
}

func (s *BadgerStore) Search(ctx context.Context, cfg *SearchCfg) ([]ScoredMember, error) {
	return Search(ctx, s, cfg)
}

// ZKeys returns all sorted set keys matching a prefix.
// It scans for unique sorted set keys by looking at member entries.
// Members are binary (typically 32 or 36 bytes), so we find the key
// by looking for the last colon that precedes binary data.
func (s *BadgerStore) ZKeys(ctx context.Context, prefix []byte) ([]string, error) {
	seen := make(map[string]struct{})
	fullPrefix := append([]byte(nil), prefixZSetMember...)
	fullPrefix = append(fullPrefix, prefix...)

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = fullPrefix
		opts.PrefetchValues = false

		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			k := it.Item().Key()
			// Key format: zset:member:<key>:<member>
			// Strip prefix "zset:member:"
			afterPrefix := k[len(prefixZSetMember):]

			// Members are binary (32 or 36 bytes typically)
			// Find the key by looking for the last colon before binary data
			// We try common member sizes: 32 (hash) and 36 (outpoint)
			var key string
			for _, memberLen := range []int{32, 36} {
				if len(afterPrefix) > memberLen+1 {
					// Check if there's a colon separator at the right position
					sepIdx := len(afterPrefix) - memberLen - 1
					if afterPrefix[sepIdx] == ':' {
						key = string(afterPrefix[:sepIdx])
						break
					}
				}
			}

			// Fallback: find last colon (for other member sizes)
			if key == "" {
				lastColon := bytes.LastIndexByte(afterPrefix, ':')
				if lastColon > 0 {
					key = string(afterPrefix[:lastColon])
				}
			}

			if key != "" {
				seen[key] = struct{}{}
			}
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	return keys, nil
}

// KV Operations

// kvKey builds a simple KV key with prefix
func kvKey(key []byte) []byte {
	buf := make([]byte, 3+len(key))
	copy(buf, "kv:")
	copy(buf[3:], key)
	return buf
}

func (s *BadgerStore) Get(ctx context.Context, key []byte) ([]byte, error) {
	var value []byte
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(kvKey(key))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			value = bytes.Clone(val)
			return nil
		})
	})
	if errors.Is(err, badger.ErrKeyNotFound) {
		return nil, ErrKeyNotFound
	}
	return value, err
}

func (s *BadgerStore) Set(ctx context.Context, key, value []byte) error {
	return s.update(func(txn *badger.Txn) error {
		return txn.Set(kvKey(key), value)
	})
}

func (s *BadgerStore) Del(ctx context.Context, key []byte) error {
	return s.update(func(txn *badger.Txn) error {
		err := txn.Delete(kvKey(key))
		if errors.Is(err, badger.ErrKeyNotFound) {
			return nil
		}
		return err
	})
}

func (s *BadgerStore) Scan(ctx context.Context, prefix []byte, limit int) ([]KV, error) {
	var results []KV
	fullPrefix := kvKey(prefix)

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = fullPrefix

		it := txn.NewIterator(opts)
		defer it.Close()

		count := 0
		for it.Rewind(); it.Valid(); it.Next() {
			if limit > 0 && count >= limit {
				break
			}

			item := it.Item()
			k := item.Key()

			// Strip the "kv:" prefix from the key
			key := bytes.Clone(k[3:])

			var value []byte
			err := item.Value(func(val []byte) error {
				value = bytes.Clone(val)
				return nil
			})
			if err != nil {
				return err
			}

			results = append(results, KV{Key: key, Value: value})
			count++
		}
		return nil
	})

	if err != nil {
		return nil, err
	}
	if results == nil {
		results = []KV{}
	}
	return results, nil
}

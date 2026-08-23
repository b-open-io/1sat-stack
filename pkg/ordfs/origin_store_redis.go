package ordfs

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/redis/go-redis/v9"
)

// RedisOriginStore implements OriginStore backed by Redis sorted sets.
//
// Key layout:
//
//	org:<outpoint> → <origin>:<seq>                             (string)
//	seq:<origin>   → <outpoint>                                 (sorted set, scored by seq)
//	rev:<origin>   → <outpoint>:<contentLength>:<contentType>   (sorted set, scored by seq)
//	map:<origin>   → <outpoint>                                 (sorted set, scored by seq)
//	par:<origin>   → <outpoint>                                 (sorted set, scored by seq)
//
// Outpoints are encoded with transaction.Outpoint.OrdinalString.
//
// This schema is NOT wire-compatible with the legacy go-ordfs-server layout
// (parents:<origin> sets, a single "origins" hash, bare rev members). Pointing
// this store at a Redis populated by that server does not read its data; chains
// reindex lazily on first resolution.
type RedisOriginStore struct {
	client *redis.Client
}

var _ OriginStore = (*RedisOriginStore)(nil)

const (
	redisKeyOrg = "org:"
	redisKeySeq = "seq:"
	redisKeyRev = "rev:"
	redisKeyMap = "map:"
	redisKeyPar = "par:"
)

// NewRedisOriginStore creates a Redis-backed origin store.
func NewRedisOriginStore(ctx context.Context, redisURL string) (*RedisOriginStore, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse origin store redis url: %w", err)
	}
	client := redis.NewClient(opts)
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to origin store redis: %w", err)
	}
	return &RedisOriginStore{client: client}, nil
}

// encodeOrgMember encodes an origin mapping value: the origin outpoint and the
// sequence that outpoint holds in the chain. OrdinalString never contains a
// colon, so the separator is unambiguous.
func encodeOrgMember(origin *transaction.Outpoint, seq uint32) string {
	return fmt.Sprintf("%s:%d", origin.OrdinalString(), seq)
}

// decodeOrgMember decodes an origin mapping value into origin and sequence.
func decodeOrgMember(val string) (*OriginInfo, error) {
	originStr, seqStr, ok := strings.Cut(val, ":")
	if !ok {
		return nil, fmt.Errorf("malformed origin record %q", val)
	}
	origin, err := transaction.OutpointFromString(originStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse origin %q: %w", originStr, err)
	}
	seq, err := strconv.ParseUint(seqStr, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("failed to parse origin seq %q: %w", seqStr, err)
	}
	return &OriginInfo{Origin: origin, Seq: uint32(seq)}, nil
}

// encodeRevMember encodes a rev entry into a sorted set member.
func encodeRevMember(outpoint *transaction.Outpoint, contentLength uint32, contentType string) string {
	return fmt.Sprintf("%s:%d:%s", outpoint.OrdinalString(), contentLength, contentType)
}

// decodeRevMember decodes a rev sorted set member into a RevEntry.
func decodeRevMember(member string) (*RevEntry, error) {
	parts := strings.SplitN(member, ":", 3)
	if len(parts) != 3 {
		return nil, fmt.Errorf("malformed rev member %q", member)
	}
	outpoint, err := transaction.OutpointFromString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("failed to parse rev outpoint %q: %w", parts[0], err)
	}
	contentLength, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return nil, fmt.Errorf("failed to parse rev content length %q: %w", parts[1], err)
	}
	return &RevEntry{
		Outpoint:      outpoint,
		ContentLength: uint32(contentLength),
		ContentType:   parts[2],
	}, nil
}

func originSetKey(prefix string, origin *transaction.Outpoint) string {
	return prefix + origin.OrdinalString()
}

func scoreBound(seq uint32) string {
	return strconv.FormatUint(uint64(seq), 10)
}

func seqFromScore(score float64) (uint32, error) {
	if score < 0 || score > math.MaxUint32 {
		return 0, fmt.Errorf("sequence score %v out of uint32 range", score)
	}
	return uint32(score), nil
}

func zMember(z redis.Z) (string, error) {
	member, ok := z.Member.(string)
	if !ok {
		return "", fmt.Errorf("unexpected sorted set member type %T", z.Member)
	}
	return member, nil
}

func (s *RedisOriginStore) GetOrigin(ctx context.Context, outpoint *transaction.Outpoint) (*OriginInfo, error) {
	val, err := s.client.Get(ctx, redisKeyOrg+outpoint.OrdinalString()).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get origin for %s: %w", outpoint.OrdinalString(), err)
	}
	info, err := decodeOrgMember(val)
	if err != nil {
		return nil, fmt.Errorf("failed to parse origin record for %s: %w", outpoint.OrdinalString(), err)
	}
	return info, nil
}

func (s *RedisOriginStore) GetSeqAt(ctx context.Context, origin *transaction.Outpoint, seq uint32) (*transaction.Outpoint, error) {
	bound := scoreBound(seq)
	members, err := s.client.ZRangeByScore(ctx, originSetKey(redisKeySeq, origin), &redis.ZRangeBy{
		Min:   bound,
		Max:   bound,
		Count: 1,
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get seq %d for origin %s: %w", seq, origin.OrdinalString(), err)
	}
	if len(members) == 0 {
		return nil, nil
	}
	outpoint, err := transaction.OutpointFromString(members[0])
	if err != nil {
		return nil, fmt.Errorf("failed to parse seq member %q: %w", members[0], err)
	}
	return outpoint, nil
}

func (s *RedisOriginStore) GetLatestSeq(ctx context.Context, origin *transaction.Outpoint) (*transaction.Outpoint, uint32, error) {
	entries, err := s.client.ZRevRangeWithScores(ctx, originSetKey(redisKeySeq, origin), 0, 0).Result()
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get latest seq for origin %s: %w", origin.OrdinalString(), err)
	}
	if len(entries) == 0 {
		return nil, 0, nil
	}
	member, err := zMember(entries[0])
	if err != nil {
		return nil, 0, fmt.Errorf("failed to read latest seq for origin %s: %w", origin.OrdinalString(), err)
	}
	outpoint, err := transaction.OutpointFromString(member)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to parse seq member %q: %w", member, err)
	}
	seq, err := seqFromScore(entries[0].Score)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to read seq for member %q: %w", member, err)
	}
	return outpoint, seq, nil
}

// latestMemberBefore returns the member with the highest score at or before seq.
func (s *RedisOriginStore) latestMemberBefore(ctx context.Context, key string, seq uint32) (string, error) {
	members, err := s.client.ZRevRangeByScore(ctx, key, &redis.ZRangeBy{
		Min:   "0",
		Max:   scoreBound(seq),
		Count: 1,
	}).Result()
	if err != nil {
		return "", fmt.Errorf("failed to query %s at or before seq %d: %w", key, seq, err)
	}
	if len(members) == 0 {
		return "", nil
	}
	return members[0], nil
}

func (s *RedisOriginStore) getLatestBefore(ctx context.Context, prefix string, origin *transaction.Outpoint, seq uint32) (*transaction.Outpoint, error) {
	member, err := s.latestMemberBefore(ctx, originSetKey(prefix, origin), seq)
	if err != nil {
		return nil, err
	}
	if member == "" {
		return nil, nil
	}
	outpoint, err := transaction.OutpointFromString(member)
	if err != nil {
		return nil, fmt.Errorf("failed to parse member %q: %w", member, err)
	}
	return outpoint, nil
}

func (s *RedisOriginStore) GetLatestRevBefore(ctx context.Context, origin *transaction.Outpoint, seq uint32) (*RevEntry, error) {
	member, err := s.latestMemberBefore(ctx, originSetKey(redisKeyRev, origin), seq)
	if err != nil {
		return nil, err
	}
	if member == "" {
		return nil, nil
	}
	return decodeRevMember(member)
}

func (s *RedisOriginStore) GetLatestMapBefore(ctx context.Context, origin *transaction.Outpoint, seq uint32) (*transaction.Outpoint, error) {
	return s.getLatestBefore(ctx, redisKeyMap, origin, seq)
}

func (s *RedisOriginStore) GetLatestParentBefore(ctx context.Context, origin *transaction.Outpoint, seq uint32) (*transaction.Outpoint, error) {
	return s.getLatestBefore(ctx, redisKeyPar, origin, seq)
}

func (s *RedisOriginStore) GetAllMapUpTo(ctx context.Context, origin *transaction.Outpoint, seq uint32) ([]*transaction.Outpoint, error) {
	members, err := s.client.ZRangeByScore(ctx, originSetKey(redisKeyMap, origin), &redis.ZRangeBy{
		Min: "0",
		Max: scoreBound(seq),
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get map entries up to seq %d for origin %s: %w", seq, origin.OrdinalString(), err)
	}
	var results []*transaction.Outpoint
	for _, member := range members {
		outpoint, err := transaction.OutpointFromString(member)
		if err != nil {
			return nil, fmt.Errorf("failed to parse map member %q: %w", member, err)
		}
		results = append(results, outpoint)
	}
	return results, nil
}

func (s *RedisOriginStore) GetMapSeq(ctx context.Context, origin *transaction.Outpoint, outpoint *transaction.Outpoint) (uint32, error) {
	score, err := s.client.ZScore(ctx, originSetKey(redisKeyMap, origin), outpoint.OrdinalString()).Result()
	if errors.Is(err, redis.Nil) {
		return 0, fmt.Errorf("map entry not found for outpoint %s", outpoint.OrdinalString())
	}
	if err != nil {
		return 0, fmt.Errorf("failed to get map seq for outpoint %s: %w", outpoint.OrdinalString(), err)
	}
	seq, err := seqFromScore(score)
	if err != nil {
		return 0, fmt.Errorf("failed to read map seq for outpoint %s: %w", outpoint.OrdinalString(), err)
	}
	return seq, nil
}

// queueEntry queues the sorted set writes for one chain entry. Each sequence holds at most
// one member per set, so every score is cleared even when the replacement drops a flag.
func queueEntry(ctx context.Context, pipe redis.Pipeliner, origin string, entry *OriginEntry) {
	bound := scoreBound(entry.Seq)
	score := float64(entry.Seq)
	outpoint := entry.Outpoint.OrdinalString()

	clear := func(prefix string) {
		key := prefix + origin
		pipe.ZRemRangeByScore(ctx, key, bound, bound)
	}
	write := func(prefix, member string) {
		key := prefix + origin
		pipe.ZAdd(ctx, key, redis.Z{Score: score, Member: member})
	}

	clear(redisKeySeq)
	write(redisKeySeq, outpoint)
	clear(redisKeyRev)
	if entry.HasRev {
		write(redisKeyRev, encodeRevMember(entry.Outpoint, entry.ContentLength, entry.ContentType))
	}
	clear(redisKeyMap)
	if entry.HasMap {
		write(redisKeyMap, outpoint)
	}
	clear(redisKeyPar)
	if entry.HasPar {
		write(redisKeyPar, outpoint)
	}
}

func (s *RedisOriginStore) WriteBatch(ctx context.Context, batch *OriginBatch) error {
	origin := batch.Origin.OrdinalString()
	pipe := s.client.TxPipeline()

	for i := range batch.Entries {
		entry := &batch.Entries[i]
		pipe.Set(ctx, redisKeyOrg+entry.Outpoint.OrdinalString(), encodeOrgMember(batch.Origin, entry.Seq), 0)
		queueEntry(ctx, pipe, origin, entry)
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("failed to write batch for origin %s: %w", origin, err)
	}
	return nil
}

func (s *RedisOriginStore) AddEntry(ctx context.Context, origin *transaction.Outpoint, entry *OriginEntry) error {
	originStr := origin.OrdinalString()
	pipe := s.client.TxPipeline()

	pipe.Set(ctx, redisKeyOrg+entry.Outpoint.OrdinalString(), encodeOrgMember(origin, entry.Seq), 0)
	queueEntry(ctx, pipe, originStr, entry)

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("failed to add entry %s at seq %d: %w", entry.Outpoint.OrdinalString(), entry.Seq, err)
	}
	return nil
}

func (s *RedisOriginStore) Close() error {
	return s.client.Close()
}

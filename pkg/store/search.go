package store

import (
	"bytes"
	"context"
)

const pageSize = int64(100)

// cursor tracks position in a single key's sorted set during multi-key search
type cursor struct {
	key     []byte
	results []ScoredMember
	pos     int
	offset  int64
	done    bool
}

// Search implements multi-key sorted set search using ZRange/ZRevRange.
// This is a shared implementation that all backends can use.
func Search(ctx context.Context, s Store, cfg *SearchCfg) ([]ScoredMember, error) {
	if len(cfg.Keys) == 0 {
		return nil, nil
	}

	limit := int(cfg.Limit) // 0 = unlimited

	cursors := make([]*cursor, len(cfg.Keys))
	for i, key := range cfg.Keys {
		cursors[i] = &cursor{key: key}
		if err := fetchPage(ctx, s, cursors[i], cfg); err != nil {
			return nil, err
		}
	}

	seen := make(map[string]float64)
	var records []ScoredMember
	if limit > 0 {
		records = make([]ScoredMember, 0, limit)
	}

	for limit == 0 || len(records) < limit {
		member, score, key, ok, err := nextResult(ctx, s, cursors, cfg, seen)
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		seen[string(member)] = score
		records = append(records, ScoredMember{
			Member: member,
			Score:  score,
			Key:    key,
		})
	}

	return records, nil
}

func nextResult(ctx context.Context, s Store, cursors []*cursor, cfg *SearchCfg, seen map[string]float64) ([]byte, float64, []byte, bool, error) {
	for {
		var best *cursor
		var bestScore float64
		var bestMember []byte

		for _, c := range cursors {
			if c.pos >= len(c.results) {
				if c.done {
					continue
				}
				if err := fetchPage(ctx, s, c, cfg); err != nil {
					return nil, 0, nil, false, err
				}
				if c.pos >= len(c.results) {
					continue
				}
			}
			m := c.results[c.pos]
			if best == nil ||
				(cfg.Reverse && m.Score > bestScore) ||
				(!cfg.Reverse && m.Score < bestScore) {
				best = c
				bestScore = m.Score
				bestMember = m.Member
			}
		}

		if best == nil {
			return nil, 0, nil, false, nil
		}

		switch cfg.JoinType {
		case JoinIntersect:
			if !inAllCursors(cursors, bestMember) {
				for _, c := range cursors {
					if c.pos < len(c.results) && bytes.Equal(c.results[c.pos].Member, bestMember) {
						c.pos++
					}
				}
				continue
			}
			for _, c := range cursors {
				c.pos++
			}

		case JoinDifference:
			if len(cursors) > 1 && inAnyCursor(cursors[1:], bestMember) {
				best.pos++
				continue
			}
			best.pos++

		default: // JoinUnion
			best.pos++
		}

		lastScore, ok := seen[string(bestMember)]
		if !ok || lastScore != bestScore {
			return bestMember, bestScore, best.key, true, nil
		}
	}
}

func inAllCursors(cursors []*cursor, member []byte) bool {
	for _, c := range cursors {
		if c.pos >= len(c.results) || !bytes.Equal(c.results[c.pos].Member, member) {
			return false
		}
	}
	return true
}

func inAnyCursor(cursors []*cursor, member []byte) bool {
	for _, c := range cursors {
		if c.pos < len(c.results) && bytes.Equal(c.results[c.pos].Member, member) {
			return true
		}
	}
	return false
}

func fetchPage(ctx context.Context, s Store, c *cursor, cfg *SearchCfg) error {
	scoreRange := ScoreRange{
		Min:    cfg.From,
		Max:    cfg.To,
		Offset: c.offset,
		Count:  pageSize,
	}

	var err error
	if cfg.Reverse {
		c.results, err = s.ZRevRange(ctx, c.key, scoreRange)
	} else {
		c.results, err = s.ZRange(ctx, c.key, scoreRange)
	}
	if err != nil {
		return err
	}

	c.pos = 0
	c.offset += int64(len(c.results))
	if len(c.results) < int(pageSize) {
		c.done = true
	}
	return nil
}

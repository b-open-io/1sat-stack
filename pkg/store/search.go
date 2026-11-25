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

// nextResult merges the cursors in (score, member) order — the order every
// backend returns sorted set entries in, members breaking score ties
// lexicographically (reversed when cfg.Reverse). All outputs of a transaction
// share one score, so ties are common and matching must compare members, not
// just scores.
func nextResult(ctx context.Context, s Store, cursors []*cursor, cfg *SearchCfg, seen map[string]float64) ([]byte, float64, []byte, bool, error) {
	for {
		var best *cursor
		var head ScoredMember
		exhausted := 0

		for _, c := range cursors {
			if err := ensureHead(ctx, s, c, cfg); err != nil {
				return nil, 0, nil, false, err
			}
			if c.pos >= len(c.results) {
				exhausted++
				continue
			}
			if m := c.results[c.pos]; best == nil || compareEntries(m, head, cfg.Reverse) < 0 {
				best = c
				head = m
			}
		}

		if best == nil {
			return nil, 0, nil, false, nil
		}

		// Remaining entries can no longer produce results: an intersection
		// needs every key, a difference needs the first key.
		switch cfg.JoinType {
		case JoinIntersect:
			if exhausted > 0 {
				return nil, 0, nil, false, nil
			}
		case JoinDifference:
			if cursors[0].pos >= len(cursors[0].results) {
				return nil, 0, nil, false, nil
			}
		}

		// Consume the head entry from every cursor positioned on it.
		matched := 0
		inFirst := false
		for i, c := range cursors {
			if c.pos < len(c.results) && compareEntries(c.results[c.pos], head, cfg.Reverse) == 0 {
				c.pos++
				matched++
				if i == 0 {
					inFirst = true
				}
			}
		}

		var emit bool
		switch cfg.JoinType {
		case JoinIntersect:
			emit = matched == len(cursors)
		case JoinDifference:
			emit = inFirst && matched == 1
		default: // JoinUnion
			emit = true
		}
		if !emit {
			continue
		}

		lastScore, ok := seen[string(head.Member)]
		if !ok || lastScore != head.Score {
			return head.Member, head.Score, best.key, true, nil
		}
	}
}

// ensureHead loads the next page when a cursor has consumed its current one.
func ensureHead(ctx context.Context, s Store, c *cursor, cfg *SearchCfg) error {
	if c.pos < len(c.results) || c.done {
		return nil
	}
	return fetchPage(ctx, s, c, cfg)
}

// compareEntries orders a before b (negative) in iteration order.
func compareEntries(a, b ScoredMember, reverse bool) int {
	cmp := 0
	switch {
	case a.Score < b.Score:
		cmp = -1
	case a.Score > b.Score:
		cmp = 1
	default:
		cmp = bytes.Compare(a.Member, b.Member)
	}
	if reverse {
		return -cmp
	}
	return cmp
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

package store

import (
	"context"
	"fmt"
	"math/rand"
	"slices"
	"sort"
	"testing"
)

func newTestStore(t *testing.T) *BadgerStore {
	t.Helper()
	s, err := NewBadgerStoreFromConfig(&BadgerConfig{InMemory: true}, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func addAll(t *testing.T, s Store, key string, members ...ScoredMember) {
	t.Helper()
	if err := s.ZAdd(context.Background(), []byte(key), members...); err != nil {
		t.Fatalf("zadd %s: %v", key, err)
	}
}

func entry(member string, score float64) ScoredMember {
	return ScoredMember{Member: []byte(member), Score: score}
}

func searchMembers(t *testing.T, s Store, cfg *SearchCfg) []string {
	t.Helper()
	results, err := Search(context.Background(), s, cfg)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = string(r.Member)
	}
	return out
}

func keys(names ...string) [][]byte {
	out := make([][]byte, len(names))
	for i, n := range names {
		out[i] = []byte(n)
	}
	return out
}

// TestSearchJoinScoreTies covers outputs that share a score, as every output
// of one transaction does.
func TestSearchJoinScoreTies(t *testing.T) {
	s := newTestStore(t)
	// A, B, C come from one tx (score 100); D from a later tx.
	addAll(t, s, "own", entry("A", 100), entry("B", 100), entry("C", 100), entry("D", 200))
	addAll(t, s, "bsv20", entry("A", 100), entry("B", 100))
	addAll(t, s, "img", entry("C", 100), entry("D", 200))
	addAll(t, s, "txt", entry("B", 100))

	tests := []struct {
		name    string
		keys    []string
		join    JoinType
		reverse bool
		limit   uint32
		want    []string
	}{
		{name: "difference excludes every tied member", keys: []string{"own", "bsv20"}, join: JoinDifference, want: []string{"C", "D"}},
		{name: "difference reverse", keys: []string{"own", "bsv20"}, join: JoinDifference, reverse: true, want: []string{"D", "C"}},
		{name: "difference multiple subtrahends", keys: []string{"own", "bsv20", "img"}, join: JoinDifference, want: []string{}},
		{name: "intersect", keys: []string{"own", "img"}, join: JoinIntersect, want: []string{"C", "D"}},
		{name: "intersect key order independent", keys: []string{"img", "own"}, join: JoinIntersect, want: []string{"C", "D"}},
		{name: "intersect reverse", keys: []string{"img", "own"}, join: JoinIntersect, reverse: true, want: []string{"D", "C"}},
		{name: "intersect three keys", keys: []string{"txt", "own", "bsv20"}, join: JoinIntersect, want: []string{"B"}},
		{name: "intersect disjoint", keys: []string{"img", "bsv20"}, join: JoinIntersect, want: []string{}},
		{name: "union dedups tied members", keys: []string{"bsv20", "own"}, join: JoinUnion, want: []string{"A", "B", "C", "D"}},
		{name: "limit", keys: []string{"own", "bsv20"}, join: JoinDifference, limit: 1, want: []string{"C"}},
		{name: "missing key intersect", keys: []string{"own", "nope"}, join: JoinIntersect, want: []string{}},
		{name: "missing key difference", keys: []string{"own", "nope"}, join: JoinDifference, want: []string{"A", "B", "C", "D"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := searchMembers(t, s, &SearchCfg{
				Keys:     keys(tt.keys...),
				JoinType: tt.join,
				Reverse:  tt.reverse,
				Limit:    tt.limit,
			})
			if !slices.Equal(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// TestSearchJoinMatchesReference checks every join against a brute-force
// model on random data with heavy score ties that spans several pages.
func TestSearchJoinMatchesReference(t *testing.T) {
	s := newTestStore(t)
	rng := rand.New(rand.NewSource(1))

	// A member carries one score across every key, as an outpoint does in
	// the indexer.
	scores := map[string]float64{}
	sets := map[string]map[string]float64{"a": {}, "b": {}, "c": {}}
	for i := 0; i < 600; i++ {
		member := fmt.Sprintf("m%04d", i)
		score := float64(rng.Intn(40)) // ~15 members per score
		scores[member] = score
		for name, set := range sets {
			if rng.Intn(3) > 0 {
				set[member] = score
				addAll(t, s, name, entry(member, score))
			}
		}
	}
	scoreOf := func(m string) float64 { return scores[m] }

	reference := func(names []string, join JoinType, reverse bool) []string {
		var out []string
		for m := range sets[names[0]] {
			inAll, inRest := true, false
			for _, n := range names[1:] {
				_, ok := sets[n][m]
				inAll = inAll && ok
				inRest = inRest || ok
			}
			if (join == JoinIntersect && inAll) || (join == JoinDifference && !inRest) {
				out = append(out, m)
			}
		}
		if join == JoinUnion {
			seen := map[string]bool{}
			out = nil
			for _, n := range names {
				for m := range sets[n] {
					if !seen[m] {
						seen[m] = true
						out = append(out, m)
					}
				}
			}
		}
		sort.Slice(out, func(i, j int) bool {
			a, b := out[i], out[j]
			if reverse {
				a, b = b, a
			}
			if scoreOf(a) != scoreOf(b) {
				return scoreOf(a) < scoreOf(b)
			}
			return a < b
		})
		if out == nil {
			out = []string{}
		}
		return out
	}

	for _, names := range [][]string{{"a", "b"}, {"b", "a"}, {"a", "b", "c"}, {"c", "a", "b"}} {
		for _, join := range []JoinType{JoinUnion, JoinIntersect, JoinDifference} {
			for _, reverse := range []bool{false, true} {
				t.Run(fmt.Sprintf("%v/join=%d/rev=%v", names, join, reverse), func(t *testing.T) {
					got := searchMembers(t, s, &SearchCfg{Keys: keys(names...), JoinType: join, Reverse: reverse})
					want := reference(names, join, reverse)
					if !slices.Equal(got, want) {
						t.Fatalf("got %d results, want %d", len(got), len(want))
					}
				})
			}
		}
	}
}

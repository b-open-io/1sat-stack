package txo

import "github.com/b-open-io/1sat-stack/pkg/store"

// OutputSearchCfg extends store.SearchCfg with output-specific options.
// This is the shared search configuration for both 1sat-indexer and overlay.
type OutputSearchCfg struct {
	store.SearchCfg

	// Output filtering
	FilterSpent    bool // Exclude spent outputs from results
	ExcludeMined   bool // Exclude mined (confirmed) outputs
	ExcludeMempool bool // Exclude mempool (unconfirmed) outputs

	// Data loading options (from h:{outpoint} hash)
	IncludeTags []string // Which dt:{tag} fields to load (nil = none)

	// Data loading options (from BeefStore)
	IncludeOutput      bool // Load tx output (script + satoshis) from BEEF
	IncludeRawtx       bool // Include raw transaction/BEEF in results
	IncludeMerkleProof bool // Load merkle proof from BEEF
}

// NewOutputSearchCfg creates a new OutputSearchCfg with default values
func NewOutputSearchCfg() *OutputSearchCfg {
	return &OutputSearchCfg{
		SearchCfg: store.SearchCfg{
			JoinType: store.JoinUnion,
		},
	}
}

// WithKeys sets the search keys
func (c *OutputSearchCfg) WithKeys(keys ...[]byte) *OutputSearchCfg {
	c.Keys = keys
	return c
}

// WithStringKeys converts string keys to []byte keys
func (c *OutputSearchCfg) WithStringKeys(keys ...string) *OutputSearchCfg {
	c.Keys = make([][]byte, len(keys))
	for i, k := range keys {
		c.Keys[i] = []byte(k)
	}
	return c
}

// WithLimit sets the maximum number of results
func (c *OutputSearchCfg) WithLimit(limit uint32) *OutputSearchCfg {
	c.Limit = limit
	return c
}

// WithRange sets the score range
func (c *OutputSearchCfg) WithRange(from, to *float64) *OutputSearchCfg {
	c.From = from
	c.To = to
	return c
}

// WithReverse sets descending order
func (c *OutputSearchCfg) WithReverse(reverse bool) *OutputSearchCfg {
	c.Reverse = reverse
	return c
}

// WithJoinType sets the join type for multi-key searches
func (c *OutputSearchCfg) WithJoinType(joinType store.JoinType) *OutputSearchCfg {
	c.JoinType = joinType
	return c
}

// WithFilterSpent excludes spent outputs from results
func (c *OutputSearchCfg) WithFilterSpent(filter bool) *OutputSearchCfg {
	c.FilterSpent = filter
	return c
}

// WithTags specifies which tag data to load
func (c *OutputSearchCfg) WithTags(tags ...string) *OutputSearchCfg {
	c.IncludeTags = tags
	return c
}

// WithOutput includes output data (script + satoshis) from BEEF
func (c *OutputSearchCfg) WithOutput(include bool) *OutputSearchCfg {
	c.IncludeOutput = include
	return c
}

// WithMerkleProof includes merkle proof from BEEF
func (c *OutputSearchCfg) WithMerkleProof(include bool) *OutputSearchCfg {
	c.IncludeMerkleProof = include
	return c
}

// WithRawtx includes raw transaction/BEEF in results
func (c *OutputSearchCfg) WithRawtx(include bool) *OutputSearchCfg {
	c.IncludeRawtx = include
	return c
}

// StoreSearchCfg returns just the embedded store.SearchCfg
func (c *OutputSearchCfg) StoreSearchCfg() *store.SearchCfg {
	return &c.SearchCfg
}

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

	// Field loading flags - only load what's requested
	// Base response always includes: outpoint, score
	IncludeSats   bool     // Load satoshis
	IncludeSpend  bool     // Load spend txid
	IncludeEvents bool     // Load events array
	IncludeBlock  bool     // Load blockHeight and blockIdx
	IncludeTags   []string // Load specific data tags (nil = none)
}

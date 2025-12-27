package txo

import (
	"encoding/json"

	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// IndexedOutputResponse is the JSON response format for IndexedOutput.
// @Description Transaction output with indexing metadata
type IndexedOutputResponse struct {
	Outpoint    string         `json:"outpoint"`              // Always present: "txid_vout" format
	Score       float64        `json:"score"`                 // Always present
	Satoshis    *uint64        `json:"satoshis,omitempty"`    // Present if IncludeSats
	BlockHeight *uint32        `json:"blockHeight,omitempty"` // Present if IncludeBlock
	BlockIdx    *uint64        `json:"blockIdx,omitempty"`    // Present if IncludeBlock
	Spend       *string        `json:"spend,omitempty"`       // Present if IncludeSpend (nil = unspent, string = spend txid)
	Events      []string       `json:"events,omitempty"`      // Present if IncludeEvents
	Data        map[string]any `json:"data,omitempty"`        // Present if IncludeTags has entries
}

// IndexedOutput represents a transaction output with optional indexed data.
// Fields are populated based on OutputSearchCfg flags.
// Base fields (Outpoint, Score) are always populated.
type IndexedOutput struct {
	// Always populated
	Outpoint transaction.Outpoint `json:"-"` // Custom JSON marshalling via MarshalJSON
	Score    float64              `json:"-"`

	// Optional fields - nil means not requested
	Satoshis    *uint64         `json:"-"`
	BlockHeight *uint32         `json:"-"`
	BlockIdx    *uint64         `json:"-"`
	SpendTxid   *chainhash.Hash `json:"-"`
	Events      []string        `json:"-"` // nil = not requested, [] = no events
	Data        map[string]any  `json:"-"` // nil = not requested

	// Internal only - not serialized
	Owners []types.PKHash `json:"-"`
}

// AddOwner adds an owner to the output if not already present
func (o *IndexedOutput) AddOwner(owner types.PKHash) {
	for _, existing := range o.Owners {
		if existing == owner {
			return
		}
	}
	o.Owners = append(o.Owners, owner)
}

// AddOwnerFromAddress adds an owner from address string if not already present
func (o *IndexedOutput) AddOwnerFromAddress(addr string) error {
	owner, err := types.PKHashFromAddress(addr)
	if err != nil {
		return err
	}
	o.AddOwner(*owner)
	return nil
}

// AddOwnerFromBytes adds an owner from raw bytes if not already present
func (o *IndexedOutput) AddOwnerFromBytes(b []byte) error {
	owner := types.PKHashFromBytes(b)
	if owner == nil {
		return types.ErrInvalidPKHash
	}
	o.AddOwner(*owner)
	return nil
}

// AddEvent adds an event to the output if not already present
func (o *IndexedOutput) AddEvent(event string) {
	for _, existing := range o.Events {
		if existing == event {
			return
		}
	}
	o.Events = append(o.Events, event)
}

// IsSpent returns true if the output has been spent
func (o *IndexedOutput) IsSpent() bool {
	return o.SpendTxid != nil
}

// SetData sets tag-specific data for the output
func (o *IndexedOutput) SetData(tag string, data interface{}) {
	if o.Data == nil {
		o.Data = make(map[string]interface{})
	}
	o.Data[tag] = data
}

// GetData retrieves tag-specific data from the output
func (o *IndexedOutput) GetData(tag string) (interface{}, bool) {
	if o.Data == nil {
		return nil, false
	}
	data, ok := o.Data[tag]
	return data, ok
}

// MarshalJSON implements custom JSON marshaling using IndexedOutputResponse
func (o *IndexedOutput) MarshalJSON() ([]byte, error) {
	resp := IndexedOutputResponse{
		Outpoint:    o.Outpoint.OrdinalString(),
		Score:       o.Score,
		Satoshis:    o.Satoshis,
		BlockHeight: o.BlockHeight,
		BlockIdx:    o.BlockIdx,
		Events:      o.Events,
		Data:        o.Data,
	}

	if o.SpendTxid != nil {
		spendStr := o.SpendTxid.String()
		resp.Spend = &spendStr
	}

	return json.Marshal(resp)
}

// ToEngineOutput converts IndexedOutput to engine.Output for overlay compatibility
func (o *IndexedOutput) ToEngineOutput() *engine.Output {
	out := &engine.Output{
		Outpoint: o.Outpoint,
		Score:    o.Score,
	}

	if o.BlockHeight != nil {
		out.BlockHeight = *o.BlockHeight
	}
	if o.BlockIdx != nil {
		out.BlockIdx = *o.BlockIdx
	}
	if o.SpendTxid != nil {
		out.Spent = true
	}

	return out
}

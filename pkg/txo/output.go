package txo

import (
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
)

// IndexedOutput embeds engine.Output and adds indexing-specific fields.
// This is the shared output type used by both 1sat-indexer and overlay.
type IndexedOutput struct {
	engine.Output // Embed canonical type

	// Extended fields for indexing
	Satoshis  uint64          `json:"satoshis,omitempty"`
	Owners    []Owner         `json:"owners,omitempty"`
	Events    []string        `json:"events,omitempty"`
	Data      map[string]any  `json:"data,omitempty"`
	SpendTxid *chainhash.Hash `json:"spend,omitempty"`
}

// AddOwner adds an owner to the output if not already present
func (o *IndexedOutput) AddOwner(owner Owner) {
	for _, existing := range o.Owners {
		if existing == owner {
			return
		}
	}
	o.Owners = append(o.Owners, owner)
}

// AddOwnerFromAddress adds an owner from address string if not already present
func (o *IndexedOutput) AddOwnerFromAddress(addr string) error {
	owner, err := OwnerFromAddress(addr)
	if err != nil {
		return err
	}
	o.AddOwner(owner)
	return nil
}

// AddOwnerFromBytes adds an owner from raw bytes if not already present
func (o *IndexedOutput) AddOwnerFromBytes(b []byte) error {
	owner, err := OwnerFromBytes(b)
	if err != nil {
		return err
	}
	o.AddOwner(owner)
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

// HeightScore calculates the score for a block height and index
func HeightScore(height uint32, idx uint64) float64 {
	if height == 0 {
		return 0
	}
	return float64(uint64(height)*1000000000 + idx)
}

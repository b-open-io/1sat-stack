package types

import (
	"encoding/json"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// Outpoint wraps transaction.Outpoint with ordinal-format JSON marshalling
type Outpoint struct {
	transaction.Outpoint
}

// MarshalJSON serializes to ordinal format (txid_vout)
func (o Outpoint) MarshalJSON() ([]byte, error) {
	return json.Marshal(o.OrdinalString())
}

// NewOutpoint creates an Outpoint from a transaction.Outpoint
func NewOutpoint(op transaction.Outpoint) *Outpoint {
	return &Outpoint{Outpoint: op}
}

// NewOutpointFromHash creates an Outpoint from a chainhash and vout
func NewOutpointFromHash(txid *chainhash.Hash, vout uint32) *Outpoint {
	return &Outpoint{Outpoint: transaction.Outpoint{
		Txid:  *txid,
		Index: vout,
	}}
}

// NewOutpointFromString parses an outpoint string
func NewOutpointFromString(s string) (*Outpoint, error) {
	op, err := transaction.OutpointFromString(s)
	if err != nil {
		return nil, err
	}
	return &Outpoint{Outpoint: *op}, nil
}

// NewOutpointFromBytes creates an Outpoint from 36 bytes
func NewOutpointFromBytes(b []byte) *Outpoint {
	if len(b) != 36 {
		return nil
	}
	op := transaction.NewOutpointFromBytes(b)
	return &Outpoint{Outpoint: *op}
}

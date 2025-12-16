package types

import (
	"encoding/json"
	"errors"

	"github.com/bsv-blockchain/go-sdk/script"
)

// ErrInvalidPKHash indicates an invalid PKHash value
var ErrInvalidPKHash = errors.New("invalid PKHash: must be 20 bytes")

// PKHash is a RIPEMD-160 hash (20 bytes) representing a Bitcoin address
type PKHash [20]byte

// Address returns the Bitcoin address string
func (p PKHash) Address(network ...Network) string {
	mainnet := true
	if len(network) > 0 {
		mainnet = network[0] != Testnet
	}
	add, _ := script.NewAddressFromPublicKeyHash(p[:], mainnet)
	return add.AddressString
}

// MarshalJSON serializes to Bitcoin address string
func (p PKHash) MarshalJSON() ([]byte, error) {
	return json.Marshal(p.Address())
}

// UnmarshalJSON deserializes from Bitcoin address string
func (p *PKHash) UnmarshalJSON(data []byte) error {
	var addr string
	if err := json.Unmarshal(data, &addr); err != nil {
		return err
	}
	return p.FromAddress(addr)
}

// FromAddress parses a Bitcoin address string into PKHash
func (p *PKHash) FromAddress(addr string) error {
	add, err := script.NewAddressFromString(addr)
	if err != nil {
		return err
	}
	copy(p[:], add.PublicKeyHash)
	return nil
}

// Bytes returns the raw 20-byte slice
func (p PKHash) Bytes() []byte {
	return p[:]
}

// IsZero returns true if the PKHash is all zeros
func (p PKHash) IsZero() bool {
	for _, b := range p {
		if b != 0 {
			return false
		}
	}
	return true
}

// PKHashFromAddress creates PKHash from a Bitcoin address string
func PKHashFromAddress(addr string) (*PKHash, error) {
	var p PKHash
	err := p.FromAddress(addr)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// PKHashFromBytes creates PKHash from raw 20 bytes
func PKHashFromBytes(b []byte) *PKHash {
	if len(b) != 20 {
		return nil
	}
	var p PKHash
	copy(p[:], b)
	return &p
}

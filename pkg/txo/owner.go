package txo

import (
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/bsv-blockchain/go-sdk/script"
)

// Owner is a RIPEMD-160 hash (20 bytes) representing a Bitcoin address
type Owner [20]byte

// ErrInvalidOwner indicates an invalid owner value
var ErrInvalidOwner = errors.New("invalid owner: must be 20 bytes")

// OwnerFromAddress creates Owner from a Bitcoin address string
func OwnerFromAddress(addr string) (Owner, error) {
	a, err := script.NewAddressFromString(addr)
	if err != nil {
		return Owner{}, err
	}
	if len(a.PublicKeyHash) != 20 {
		return Owner{}, ErrInvalidOwner
	}
	var o Owner
	copy(o[:], a.PublicKeyHash)
	return o, nil
}

// OwnerFromBytes creates Owner from raw 20 bytes
func OwnerFromBytes(b []byte) (Owner, error) {
	if len(b) != 20 {
		return Owner{}, ErrInvalidOwner
	}
	var o Owner
	copy(o[:], b)
	return o, nil
}

// OwnerFromHex creates Owner from a hex string (40 chars)
func OwnerFromHex(h string) (Owner, error) {
	b, err := hex.DecodeString(h)
	if err != nil {
		return Owner{}, err
	}
	return OwnerFromBytes(b)
}

// String returns the Bitcoin address representation (mainnet)
func (o Owner) String() string {
	a, err := script.NewAddressFromPublicKeyHash(o[:], true)
	if err != nil {
		return ""
	}
	return a.AddressString
}

// Address returns the Bitcoin address with optional network
func (o Owner) Address(mainnet bool) string {
	a, err := script.NewAddressFromPublicKeyHash(o[:], mainnet)
	if err != nil {
		return ""
	}
	return a.AddressString
}

// Hex returns the hex-encoded owner bytes
func (o Owner) Hex() string {
	return hex.EncodeToString(o[:])
}

// Bytes returns the raw 20-byte slice
func (o Owner) Bytes() []byte {
	return o[:]
}

// IsZero returns true if the owner is all zeros
func (o Owner) IsZero() bool {
	for _, b := range o {
		if b != 0 {
			return false
		}
	}
	return true
}

// MarshalJSON encodes as Bitcoin address string (for API responses)
func (o Owner) MarshalJSON() ([]byte, error) {
	return json.Marshal(o.String())
}

// UnmarshalJSON decodes from Bitcoin address string
func (o *Owner) UnmarshalJSON(data []byte) error {
	var addr string
	if err := json.Unmarshal(data, &addr); err != nil {
		return err
	}
	owner, err := OwnerFromAddress(addr)
	if err != nil {
		return err
	}
	*o = owner
	return nil
}

package shrug

import (
	"fmt"

	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/fxamacker/cbor/v2"
)

// MetadataContentType is the inscription content type for shrug token
// display metadata.
const MetadataContentType = "application/shrug+cbor"

// Metadata is the display metadata document carried by an inscription on a
// token's deploy output. All fields are optional.
type Metadata struct {
	Symbol   *string
	Icon     *transaction.Outpoint
	Decimals *uint8 // 0-18
}

type metadataWire struct {
	Symbol   *string `cbor:"sym,omitempty"`
	Icon     []byte  `cbor:"icon,omitempty"`
	Decimals *uint8  `cbor:"dec,omitempty"`
}

var metadataEncMode cbor.EncMode

func init() {
	// RFC 8949 §4.2 core deterministic encoding: definite lengths,
	// bytewise-lexical map key order.
	var err error
	if metadataEncMode, err = cbor.CoreDetEncOptions().EncMode(); err != nil {
		panic(err)
	}
}

// Encode serializes the metadata as a deterministically-encoded CBOR map.
func (m *Metadata) Encode() ([]byte, error) {
	if m.Decimals != nil && *m.Decimals > 18 {
		return nil, fmt.Errorf("dec %d out of range 0-18", *m.Decimals)
	}
	wire := metadataWire{
		Symbol:   m.Symbol,
		Decimals: m.Decimals,
	}
	if m.Icon != nil {
		wire.Icon = m.Icon.Bytes()
	}
	return metadataEncMode.Marshal(wire)
}

// DecodeMetadata parses a shrug metadata document. Unknown map keys are
// ignored. Decoding is lenient about map key order; encoders must still
// produce deterministic output.
func DecodeMetadata(data []byte) (*Metadata, error) {
	var wire metadataWire
	if err := cbor.Unmarshal(data, &wire); err != nil {
		return nil, fmt.Errorf("invalid shrug metadata: %w", err)
	}
	if wire.Decimals != nil && *wire.Decimals > 18 {
		return nil, fmt.Errorf("dec %d out of range 0-18", *wire.Decimals)
	}
	m := &Metadata{
		Symbol:   wire.Symbol,
		Decimals: wire.Decimals,
	}
	if wire.Icon != nil {
		if len(wire.Icon) != 36 {
			return nil, fmt.Errorf("icon must be 36 bytes, got %d", len(wire.Icon))
		}
		m.Icon = transaction.NewOutpointFromBytes(wire.Icon)
	}
	return m, nil
}

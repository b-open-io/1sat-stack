package bitcom

import (
	"slices"
	"strconv"

	bsm "github.com/bsv-blockchain/go-sdk/compat/bsm"
	"github.com/bsv-blockchain/go-sdk/script"
)

// AIPPrefix is the bitcom protocol prefix for AIP
const AIPPrefix = "15PciHG22SNLQJXMoSUaWVi7WSqc7hCfva"

// AIP represents an AIP
type AIP struct {
	BitcomIndex  uint   `json:"ii,omitempty"` // Index of the AIP in the Bitcom transaction
	Algorithm    string `json:"algorithm"`
	Address      string `json:"address"`
	Signature    []byte `json:"signature"`
	FieldIndexes []int  `json:"fieldIndexes,omitempty"`
	Valid        bool   `json:"valid,omitempty"`
}

// DecodeAIP decodes the AIP data from the transaction script
func DecodeAIP(b *Bitcom) []*AIP {
	aips := []*AIP{}

	// Safety check for nil
	if b == nil || len(b.Protocols) == 0 {
		return aips
	}

	for protoIdx, proto := range b.Protocols {
		if proto.Protocol == AIPPrefix {
			scr := script.NewFromBytes(proto.Script)
			if scr == nil {
				continue
			}

			// Parse script into chunks
			chunks, err := scr.Chunks()
			if err != nil || len(chunks) < 3 { // Need at least algorithm, address, and signature
				continue
			}

			aip := &AIP{
				BitcomIndex: uint(protoIdx),
			}

			// Read ALGORITHM (first chunk)
			if len(chunks) > 0 {
				aip.Algorithm = string(chunks[0].Data)
			} else {
				continue
			}

			// Read ADDRESS (second chunk)
			if len(chunks) > 1 {
				aip.Address = string(chunks[1].Data)
			} else {
				continue
			}

			// Read SIGNATURE (third chunk)
			if len(chunks) > 2 {
				// aip.Signature = base64.StdEncoding.EncodeToString(chunks[2].Data)
				aip.Signature = chunks[2].Data
			} else {
				continue
			}

			// Read optional FIELD INDEXES (remaining chunks)
			// If present, these indicate which fields were signed
			for i := 3; i < len(chunks); i++ {
				index, err := strconv.Atoi(string(chunks[i].Data))
				if err != nil {
					break // Stop if we encounter non-numeric data
				}
				aip.FieldIndexes = append(aip.FieldIndexes, index)
			}

			validateAip(aip, b.Protocols[:protoIdx])

			aips = append(aips, aip)
		}
	}

	return aips
}

func validateAip(aip *AIP, protos []*BitcomProtocol) {
	data := make([]byte, 0)
	idx := 0
	data = append(data, script.OpRETURN)
	for _, p := range protos {
		data = append(data, p.Protocol...)
		if tape, err := script.DecodeScript(p.Script); err != nil {
			continue
		} else {
			for _, op := range tape {
				if (op.Op >= 0 && op.Op <= 0x4e) && (aip.FieldIndexes == nil || slices.Contains(aip.FieldIndexes, idx)) {
					data = append(data, op.Data...)
				}
				idx++
			}
		}
		data = append(data, '|')
	}
	// if sig, err := base64.StdEncoding.DecodeString(aip.Signature); err != nil {
	// 	return
	// } else if err := bsm.VerifyMessage(aip.Address, sig, data); err == nil {
	if err := bsm.VerifyMessage(aip.Address, aip.Signature, data); err == nil {
		aip.Valid = true
	}
}

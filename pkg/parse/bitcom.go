package parse

import (
	"github.com/bitcoin-sv/go-templates/template/bitcom"
	"github.com/bsv-blockchain/go-sdk/script"
)

const TagBitcom = "bitcom"
const TagB = "b"
const TagMAP = "map"
const TagAIP = "aip"
const TagBAP = "bap"
const TagSigma = "sigma"

// ParseBitcom parses bitcom protocol structure from the parse context.
// This should be called first - subsequent parsers (B, MAP, AIP, BAP, Sigma)
// read from the parsed bitcom data.
func ParseBitcom(ctx *ParseContext) *ParseResult {
	scr := script.NewFromBytes(ctx.LockingScript)
	bc := bitcom.Decode(scr)
	if bc == nil || len(bc.Protocols) == 0 {
		return nil
	}

	return &ParseResult{
		Tag:    TagBitcom,
		Data:   bc,
		Events: []string{},
	}
}

// ParseB parses B protocol data from previously parsed bitcom.
// Requires ParseBitcom to have been called first.
func ParseB(ctx *ParseContext) *ParseResult {
	bc := GetData[bitcom.Bitcom](ctx, TagBitcom)
	if bc == nil {
		return nil
	}

	for _, proto := range bc.Protocols {
		if proto.Protocol == bitcom.BPrefix {
			if b := bitcom.DecodeB(proto.Script); b != nil {
				result := &ParseResult{
					Tag:    TagB,
					Data:   b,
					Events: []string{},
				}
				if b.MediaType != "" {
					result.Events = append(result.Events, "type:"+string(b.MediaType))
				}
				return result
			}
		}
	}
	return nil
}

// ParseMAP parses MAP protocol data from previously parsed bitcom.
// Requires ParseBitcom to have been called first.
func ParseMAP(ctx *ParseContext) *ParseResult {
	bc := GetData[bitcom.Bitcom](ctx, TagBitcom)
	if bc == nil {
		return nil
	}

	for _, proto := range bc.Protocols {
		if proto.Protocol == bitcom.MapPrefix {
			if m := bitcom.DecodeMap(proto.Script); m != nil {
				result := &ParseResult{
					Tag:    TagMAP,
					Data:   m,
					Events: []string{},
				}
				if t, ok := m.Data["type"]; ok {
					result.Events = append(result.Events, "type:"+t)
				}
				if app, ok := m.Data["app"]; ok {
					result.Events = append(result.Events, "app:"+app)
				}
				return result
			}
		}
	}
	return nil
}

// ParseAIP parses AIP signatures from previously parsed bitcom.
// Requires ParseBitcom to have been called first.
func ParseAIP(ctx *ParseContext) *ParseResult {
	bc := GetData[bitcom.Bitcom](ctx, TagBitcom)
	if bc == nil {
		return nil
	}

	aips := bitcom.DecodeAIP(bc)
	if len(aips) == 0 {
		return nil
	}

	result := &ParseResult{
		Tag:    TagAIP,
		Data:   aips,
		Events: []string{},
	}
	for _, a := range aips {
		if a.Valid {
			result.Events = append(result.Events, "signer:"+a.Address)
		}
	}
	return result
}

// ParseBAP parses BAP protocol data from previously parsed bitcom.
// Requires ParseBitcom to have been called first.
func ParseBAP(ctx *ParseContext) *ParseResult {
	bc := GetData[bitcom.Bitcom](ctx, TagBitcom)
	if bc == nil {
		return nil
	}

	bap := bitcom.DecodeBAP(bc)
	if bap == nil {
		return nil
	}

	result := &ParseResult{
		Tag:    TagBAP,
		Data:   bap,
		Events: []string{"type:" + string(bap.Type)},
	}
	if bap.IDKey != "" {
		result.Events = append(result.Events, "id:"+bap.IDKey)
	}
	return result
}

// ParseSigma parses SIGMA signatures from previously parsed bitcom.
// Requires ParseBitcom to have been called first.
func ParseSigma(ctx *ParseContext) *ParseResult {
	bc := GetData[bitcom.Bitcom](ctx, TagBitcom)
	if bc == nil {
		return nil
	}

	sigmas := bitcom.DecodeSIGMA(bc)
	if len(sigmas) == 0 {
		return nil
	}

	result := &ParseResult{
		Tag:    TagSigma,
		Data:   sigmas,
		Events: []string{},
	}
	for _, s := range sigmas {
		if s.Valid {
			result.Events = append(result.Events, "signer:"+s.SignerAddress)
		}
	}
	return result
}

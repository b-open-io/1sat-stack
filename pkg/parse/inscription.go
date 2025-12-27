package parse

import (
	"strings"

	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bitcoin-sv/go-templates/template/inscription"
	"github.com/bitcoin-sv/go-templates/template/p2pkh"
	"github.com/bsv-blockchain/go-sdk/script"
)

const TagInscription = "insc"

// ParseInscription parses an inscription from the parse context.
// Returns nil if the output does not contain a valid inscription.
func ParseInscription(ctx *ParseContext) *ParseResult {
	// 1sat ordinals require exactly 1 satoshi
	if ctx.Satoshis != 1 {
		return nil
	}

	scr := script.NewFromBytes(ctx.LockingScript)
	insc := inscription.Decode(scr)
	if insc == nil {
		return nil
	}

	result := &ParseResult{
		Tag:    TagInscription,
		Data:   insc,
		Events: []string{},
	}

	// Add event for content type (base type without parameters)
	if insc.File.Type != "" {
		parts := strings.Split(insc.File.Type, ";")
		if len(parts) > 0 {
			result.Events = append(result.Events, "type:"+parts[0])
		}
	}

	// Parent reference
	if insc.Parent != nil {
		result.Events = append(result.Events, "parent:"+insc.Parent.String())
	}

	// Extract owner from suffix script (P2PKH after inscription)
	if len(insc.ScriptSuffix) > 0 {
		suffix := script.NewFromBytes(insc.ScriptSuffix)
		if addr := p2pkh.Decode(suffix, true); addr != nil {
			pkHash := types.PKHashFromBytes(addr.PublicKeyHash)
			if pkHash != nil {
				result.Owners = append(result.Owners, pkHash)
			}
		}
	}

	return result
}

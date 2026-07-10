package parse

import (
	"strings"

	"github.com/b-open-io/1sat-stack/pkg/template/inscription"
	"github.com/b-open-io/1sat-stack/pkg/template/p2pkh"
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bsv-blockchain/go-sdk/script"
)

const TagInscription = "insc"

// ParseInscription parses an inscription from the parse context.
// Returns nil if the output does not contain a valid inscription.
func ParseInscription(ctx *ParseContext) (*ParseResult, error) {
	// 1sat ordinals require exactly 1 satoshi
	if ctx.Satoshis != 1 {
		return nil, nil
	}

	scr := script.NewFromBytes(ctx.LockingScript)
	insc := inscription.Decode(scr)
	if insc == nil {
		return nil, nil
	}

	result := &ParseResult{
		Tag:    TagInscription,
		Data:   insc,
		Events: []string{"insc"},
	}

	// Add events for content type at two levels
	if insc.File.Type != "" {
		// Strip parameters (e.g., "text/plain; charset=utf-8" -> "text/plain")
		fullType := strings.Split(insc.File.Type, ";")[0]
		fullType = strings.TrimSpace(fullType)

		if fullType != "" {
			// Base type (e.g., "image" from "image/jpeg")
			baseParts := strings.Split(fullType, "/")
			if len(baseParts) > 0 && baseParts[0] != "" {
				result.Events = append(result.Events, "type:"+baseParts[0])
			}
			// Full type (e.g., "image/jpeg")
			result.Events = append(result.Events, "type:"+fullType)
		}
	}

	// Parent reference
	if insc.Parent != nil {
		result.Events = append(result.Events, "parent:"+insc.Parent.String())
	}

	// Check suffix for P2PKH owner. 1Sat ordinals place the P2PKH lock after
	// the inscription envelope, optionally preceded by OP_CODESEPARATOR and
	// followed by MAP metadata. Decode the 25-byte lock at the start of the
	// suffix rather than the whole suffix.
	if len(insc.ScriptSuffix) > 0 {
		suffix := insc.ScriptSuffix
		if suffix[0] == script.OpCODESEPARATOR {
			suffix = suffix[1:]
		}
		if len(suffix) >= 25 {
			if addr := p2pkh.Decode(script.NewFromBytes(suffix[:25]), true); addr != nil {
				if owner := types.PKHashFromBytes(addr.PublicKeyHash); owner != nil {
					result.Owners = append(result.Owners, owner)
				}
			}
		}
	}

	return result, nil
}

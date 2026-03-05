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

	// Check suffix for P2PKH owner (some inscriptions place P2PKH after OP_ENDIF)
	if len(insc.ScriptSuffix) > 0 {
		suffix := script.NewFromBytes(insc.ScriptSuffix)
		if addr := p2pkh.Decode(suffix, true); addr != nil {
			owner := types.PKHashFromBytes(addr.PublicKeyHash)
			if owner != nil {
				result.Owners = append(result.Owners, owner)
			}
		}
	}

	return result
}

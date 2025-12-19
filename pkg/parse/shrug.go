package parse

import (
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bitcoin-sv/go-templates/template/p2pkh"
	"github.com/bitcoin-sv/go-templates/template/shrug"
	"github.com/bsv-blockchain/go-sdk/script"
)

const TagShrug = "shrug"

// ParseShrug parses a shrug token from the parse context.
// Returns nil if the output does not contain a valid shrug.
func ParseShrug(ctx *ParseContext) *ParseResult {
	scr := script.NewFromBytes(ctx.LockingScript)
	s := shrug.Decode(scr)
	if s == nil {
		return nil
	}

	result := &ParseResult{
		Tag:    TagShrug,
		Data:   s,
		Events: []string{},
	}

	// Set ID based on whether this is a deploy or transfer
	if s.Id == nil {
		// Deploy - ID is the outpoint of this output
		if ctx.Outpoint != nil {
			result.Events = append(result.Events, "deploy")
			result.Events = append(result.Events, "id:"+ctx.Outpoint.String())
		}
	} else {
		result.Events = append(result.Events, "id:"+s.Id.String())
	}

	// Extract owner from suffix script
	if len(s.ScriptSuffix) > 0 {
		suffix := script.NewFromBytes(s.ScriptSuffix)
		if addr := p2pkh.Decode(suffix, true); addr != nil {
			pkHash := types.PKHashFromBytes(addr.PublicKeyHash)
			if pkHash != nil {
				result.Owners = append(result.Owners, pkHash)
				result.Events = append(result.Events, "own:"+addr.AddressString)
			}
		}
	}

	return result
}

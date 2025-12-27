package parse

import (
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bitcoin-sv/go-templates/template/cosign"
	"github.com/bsv-blockchain/go-sdk/script"
)

const TagCosign = "cosign"

// ParseCosign parses a cosign locking script from the parse context.
// Returns nil if the script is not a valid cosign.
func ParseCosign(ctx *ParseContext) *ParseResult {
	scr := script.NewFromBytes(ctx.LockingScript)
	c := cosign.Decode(scr)
	if c == nil {
		return nil
	}

	result := &ParseResult{
		Tag:    TagCosign,
		Data:   c,
		Events: []string{},
	}

	// Add owner
	pkHash, err := types.PKHashFromAddress(c.Address)
	if err == nil && pkHash != nil {
		result.Owners = append(result.Owners, pkHash)
	}

	return result
}

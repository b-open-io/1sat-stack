package parse

import (
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bitcoin-sv/go-templates/template/ordlock"
	"github.com/bsv-blockchain/go-sdk/script"
)

const TagOrdLock = "ordlock"

// ParseOrdLock parses an ordlock listing from the parse context.
// Returns nil if the output does not contain a valid ordlock.
func ParseOrdLock(ctx *ParseContext) *ParseResult {
	// OrdLock requires exactly 1 satoshi
	if ctx.Satoshis != 1 {
		return nil
	}

	scr := script.NewFromBytes(ctx.LockingScript)
	ol := ordlock.Decode(scr)
	if ol == nil {
		return nil
	}

	result := &ParseResult{
		Tag:    TagOrdLock,
		Data:   ol,
		Events: []string{"list"},
	}

	// Extract owner (seller) from ordlock
	if ol.Seller != nil {
		pkHash := types.PKHashFromBytes(ol.Seller.PublicKeyHash)
		if pkHash != nil {
			result.Owners = append(result.Owners, pkHash)
		}
	}

	return result
}

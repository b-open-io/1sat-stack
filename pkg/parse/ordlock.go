package parse

import (
	"github.com/b-open-io/1sat-stack/pkg/template/ordlock"
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bsv-blockchain/go-sdk/script"
)

const TagOrdLock = "ordlock"

// ParseOrdLock parses an ordlock listing from the parse context.
// Returns nil if the output does not contain a valid ordlock.
func ParseOrdLock(ctx *ParseContext) (*ParseResult, error) {
	// OrdLock requires exactly 1 satoshi
	if ctx.Satoshis != 1 {
		return nil, nil
	}

	scr := script.NewFromBytes(ctx.LockingScript)
	ol := ordlock.Decode(scr)
	if ol == nil {
		return nil, nil
	}

	// Deprecated v1 listings retain their data and cancellation owner for
	// address-based recovery without publishing a new public listing event.
	result := &ParseResult{
		Tag:  TagOrdLock,
		Data: ol,
	}

	// Extract owner (seller/cancel address) from ordlock
	if ol.Seller != nil {
		pkHash := types.PKHashFromBytes(ol.Seller.PublicKeyHash)
		if pkHash != nil {
			result.Owners = append(result.Owners, pkHash)
		}
	}

	return result, nil
}

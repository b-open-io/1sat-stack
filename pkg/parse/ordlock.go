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

	// v1 OrdLock is a deprecated, vulnerable contract. It is NOT published as a
	// public "ordlock" event, so it can't be enumerated via the public event
	// index. The owner (cancel address) IS still indexed so the sweep-address
	// recovery tool can find legacy listings by the user's own addresses.
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

package parse

import (
	"github.com/b-open-io/1sat-stack/pkg/template/ordlock"
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bsv-blockchain/go-sdk/script"
)

const (
	// TagOrdLock is the parse tag / per-output data key for DEPRECATED v1
	// listings. v1 is NOT published as a public event, so it cannot be
	// enumerated or fed to any market topic. The data and the owner (cancel
	// address) ARE still stored so the legacy sweep tool can find and cancel
	// a user's old listings by address.
	TagOrdLock = "ordlock"
	// TagOrdLockV2 is the parse tag, per-output data key AND public event for
	// OrdLock v2 (batch, SIGHASH_SINGLE) listings. The event is what the
	// event bridge routes into the tm_ordlock_v2 overlay topic; spends follow
	// automatically as spend:ordlock2.
	TagOrdLockV2 = "ordlock2"
)

// ParseOrdLock parses an OrdLock listing (v2 first, then deprecated v1) from
// the parse context. Returns nil if the output is not a listing.
func ParseOrdLock(ctx *ParseContext) (*ParseResult, error) {
	// A listing carries exactly 1 satoshi (the ordinal).
	if ctx.Satoshis != 1 {
		return nil, nil
	}

	scr := script.NewFromBytes(ctx.LockingScript)

	if ol := ordlock.DecodeV2(scr); ol != nil {
		result := &ParseResult{
			Tag:    TagOrdLockV2,
			Data:   ol,
			Events: []string{TagOrdLockV2},
		}
		addSeller(result, ol)
		return result, nil
	}

	ol := ordlock.Decode(scr)
	if ol == nil {
		return nil, nil
	}
	result := &ParseResult{
		Tag:  TagOrdLock,
		Data: ol,
		// deliberately no Events: see TagOrdLock
	}
	addSeller(result, ol)
	return result, nil
}

// addSeller indexes the seller/cancel address as an owner of the listing so
// address-based lookups (sweep recovery, wallet sync) can find it.
func addSeller(result *ParseResult, ol *ordlock.OrdLock) {
	if ol.Seller == nil {
		return
	}
	if pkHash := types.PKHashFromBytes(ol.Seller.PublicKeyHash); pkHash != nil {
		result.Owners = append(result.Owners, pkHash)
	}
}

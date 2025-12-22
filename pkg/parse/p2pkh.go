package parse

import (
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bitcoin-sv/go-templates/template/p2pkh"
	"github.com/bsv-blockchain/go-sdk/script"
)

const TagP2PKH = "p2pkh"

// ParseP2PKH parses a P2PKH locking script from the parse context.
// Returns nil if the script is not a valid P2PKH.
func ParseP2PKH(ctx *ParseContext) *ParseResult {
	scr := script.NewFromBytes(ctx.LockingScript)
	addr := p2pkh.Decode(scr, true)
	if addr == nil {
		return nil
	}

	pkHash := types.PKHashFromBytes(addr.PublicKeyHash)
	if pkHash == nil {
		return nil
	}

	return &ParseResult{
		Tag:    TagP2PKH,
		Data:   addr,
		Owners: []*types.PKHash{pkHash},
	}
}

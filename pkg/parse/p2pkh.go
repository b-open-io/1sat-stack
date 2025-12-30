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

	result := &ParseResult{
		Tag:    TagP2PKH,
		Data:   addr,
		Events: []string{},
		Owners: []*types.PKHash{pkHash},
	}

	// Add p2pkh event only for pure P2PKH scripts (exactly 25 bytes)
	// Composite scripts (inscriptions, etc.) still tracked by owner but not as p2pkh
	if len(ctx.LockingScript) == 25 {
		result.Events = append(result.Events, "p2pkh")
		// Add fund event for fundable UTXOs (non-dust, > 1 satoshi)
		if ctx.Satoshis > 1 {
			result.Events = append(result.Events, "fund")
		}
	}

	return result
}

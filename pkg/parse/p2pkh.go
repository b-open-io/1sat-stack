package parse

import (
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bitcoin-sv/go-templates/template/p2pkh"
	"github.com/bsv-blockchain/go-sdk/script"
)

const TagP2PKH = "p2pkh"

// ParseP2PKH extracts the owner from scripts with a P2PKH prefix.
// For pure P2PKH scripts (exactly 25 bytes), also emits a p2pkh:{address} event.
// For composite scripts with a P2PKH prefix, only populates the owner.
func ParseP2PKH(ctx *ParseContext) (*ParseResult, error) {
	if len(ctx.LockingScript) < 25 {
		return nil, nil
	}

	prefix := script.Script(ctx.LockingScript[:25])
	addr := p2pkh.Decode(&prefix, true)
	if addr == nil {
		return nil, nil
	}

	pkHash := types.PKHashFromBytes(addr.PublicKeyHash)
	if pkHash == nil {
		return nil, nil
	}

	result := &ParseResult{
		Tag:    TagP2PKH,
		Owners: []*types.PKHash{pkHash},
	}

	if len(ctx.LockingScript) == 25 {
		result.Events = []string{TagP2PKH + ":" + pkHash.Address()}
	}

	return result, nil
}

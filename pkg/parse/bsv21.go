package parse

import (
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bitcoin-sv/go-templates/template/bsv21"
	"github.com/bitcoin-sv/go-templates/template/p2pkh"
	"github.com/bsv-blockchain/go-sdk/script"
)

const TagBSV21 = "bsv21"

// BSV21 holds parsed BSV21 token data
type BSV21 struct {
	Id       string  `json:"id,omitempty"`
	Op       string  `json:"op"`
	Symbol   *string `json:"sym,omitempty"`
	Decimals *uint8  `json:"dec,omitempty"`
	Icon     *string `json:"icon,omitempty"`
	Amt      uint64  `json:"amt"`
}

// ParseBSV21 parses a BSV21 token from the parse context.
// Returns nil if the output does not contain a valid BSV21 token.
func ParseBSV21(ctx *ParseContext) *ParseResult {
	scr := script.NewFromBytes(ctx.LockingScript)
	b := bsv21.Decode(scr)
	if b == nil {
		return nil
	}

	result := &ParseResult{
		Tag:    TagBSV21,
		Events: []string{},
	}

	// Build BSV21 data
	bsvData := &BSV21{
		Op:       b.Op,
		Symbol:   b.Symbol,
		Decimals: b.Decimals,
		Icon:     b.Icon,
		Amt:      b.Amt,
	}

	// Set ID based on operation type
	switch b.Op {
	case string(bsv21.OpDeployMint), string(bsv21.OpDeployAuth):
		// For deploy operations, the ID is the outpoint of this output
		if ctx.Outpoint != nil {
			bsvData.Id = ctx.Outpoint.String()
		}
		result.Events = append(result.Events, "deploy")
	case string(bsv21.OpTransfer), string(bsv21.OpBurn), string(bsv21.OpMint), string(bsv21.OpAuth):
		bsvData.Id = b.Id
	}

	// Add ID event
	if bsvData.Id != "" {
		result.Events = append(result.Events, "id:"+bsvData.Id)
	}

	// Add symbol event for deploy operations
	if bsvData.Symbol != nil {
		result.Events = append(result.Events, "sym:"+*bsvData.Symbol)
	}

	result.Data = bsvData

	// Extract owner from inscription suffix
	if b.Insc != nil && len(b.Insc.ScriptSuffix) > 0 {
		suffix := script.NewFromBytes(b.Insc.ScriptSuffix)
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

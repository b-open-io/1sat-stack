package parse

import (
	"github.com/bitcoin-sv/go-templates/template/bsv21"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

const TagBSV21 = "bsv21"

// BSV21 holds parsed BSV21 token data
type BSV21 struct {
	Id       string  `json:"id"`
	Op       string  `json:"op"`
	Symbol   *string `json:"sym,omitempty"`
	Decimals *uint8  `json:"dec,omitempty,string"`
	Icon     *string `json:"icon,omitempty"`
	Amt      uint64  `json:"amt,string"`
}

// ParseBSV21 parses a BSV21 token from the parse context.
// Returns nil if the output does not contain a valid BSV21 token.
// Emits a "bsv21:{tokenId}" event for indexing. Validated token lookups
// (balance, history, unspent) are handled separately by the BSV21 overlay.
func ParseBSV21(ctx *ParseContext) (*ParseResult, error) {
	scr := script.NewFromBytes(ctx.LockingScript)
	b := bsv21.Decode(scr)
	if b == nil {
		return nil, nil
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
			bsvData.Id = ctx.Outpoint.OrdinalString()
		}
	case string(bsv21.OpTransfer), string(bsv21.OpBurn), string(bsv21.OpMint), string(bsv21.OpAuth):
		if _, err := transaction.OutpointFromString(b.Id); err != nil {
			return nil, nil
		}
		bsvData.Id = b.Id
	default:
		return nil, nil
	}

	var events []string
	if bsvData.Id != "" {
		events = append(events, "bsv21:"+bsvData.Id)
	}

	return &ParseResult{
		Tag:    TagBSV21,
		Data:   bsvData,
		Events: events,
	}, nil
}

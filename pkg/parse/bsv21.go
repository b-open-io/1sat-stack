package parse

import (
	"github.com/bitcoin-sv/go-templates/template/bsv21"
	"github.com/bsv-blockchain/go-sdk/script"
)

const TagBSV21 = "bsv21"

// BSV21 holds parsed BSV21 token data
type BSV21 struct {
	Id       string  `json:"id"`
	Op       string  `json:"op"`
	Symbol   *string `json:"sym,omitempty"`
	Decimals *uint8  `json:"dec,omitempty"`
	Icon     *string `json:"icon,omitempty"`
	Amt      uint64  `json:"amt"`
}

// ParseBSV21 parses a BSV21 token from the parse context.
// Returns nil if the output does not contain a valid BSV21 token.
// Note: This parser only extracts tag data. Events and owners come from
// suffix parsers (P2PKH, Inscription, Cosign). Validated token lookups
// (balance, history, unspent) are handled by the BSV21 lookup service.
func ParseBSV21(ctx *ParseContext) *ParseResult {
	scr := script.NewFromBytes(ctx.LockingScript)
	b := bsv21.Decode(scr)
	if b == nil {
		return nil
	}

	// Build BSV21 data (tag data only, no events)
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
	case string(bsv21.OpTransfer), string(bsv21.OpBurn), string(bsv21.OpMint), string(bsv21.OpAuth):
		bsvData.Id = b.Id
	}

	return &ParseResult{
		Tag:  TagBSV21,
		Data: bsvData,
		// No events - validated lookups handled by BSV21 lookup service
		// No owners - determined by suffix parser (P2PKH, Inscription, Cosign)
	}
}

package parse

import (
	"github.com/bitcoin-sv/go-templates/template/opns"
	"github.com/bsv-blockchain/go-sdk/script"
)

const TagOPNS = "opns"

func ParseOPNS(ctx *ParseContext) (*ParseResult, error) {
	scr := script.NewFromBytes(ctx.LockingScript)
	if opns.Decode(scr) == nil {
		return nil, nil
	}

	return &ParseResult{
		Tag:    TagOPNS,
		Events: []string{"opns:mine"},
	}, nil
}

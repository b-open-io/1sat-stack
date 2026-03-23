package parse

import (
	"github.com/bitcoin-sv/go-templates/template/opns"
	"github.com/bsv-blockchain/go-sdk/script"
)

const TagOPNS = "opns"

func ParseOPNS(ctx *ParseContext) (*ParseResult, error) {
	scr := script.NewFromBytes(ctx.LockingScript)
	decoded := opns.Decode(scr)
	if decoded == nil {
		return nil, nil
	}

	events := []string{"opns:mine"}
	if decoded.Domain != "" {
		events = append(events, "name:"+decoded.Domain)
	}

	return &ParseResult{
		Tag:    TagOPNS,
		Events: events,
	}, nil
}

package parse

import (
	"github.com/b-open-io/1sat-stack/pkg/template/gib"
	"github.com/bsv-blockchain/go-sdk/script"
)

// TagGib is the parse tag, per-output data key, and public event for gib
// commit heads. The event bridge routes `gib` (and `spend:gib`) into the
// tm_gib overlay topic; `gib:{origin}` gives a per-repository SSE feed.
const TagGib = gib.EventName

// ParseGib parses a gib commit head from the parse context. Returns nil if
// the output is not a head.
func ParseGib(ctx *ParseContext) (*ParseResult, error) {
	if ctx.Satoshis != 1 {
		return nil, nil
	}
	head, err := gib.Decode(script.NewFromBytes(ctx.LockingScript), ctx.Satoshis)
	if err != nil {
		return nil, nil
	}
	return &ParseResult{
		Tag:    TagGib,
		Data:   head,
		Events: []string{gib.EventName, gib.EventName + ":" + head.Origin},
	}, nil
}

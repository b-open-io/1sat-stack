package parse

import (
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bitcoin-sv/go-templates/template/lockup"
	"github.com/bsv-blockchain/go-sdk/script"
)

const TagLock = "lock"

// ParseLock parses a lock contract from the parse context.
// Returns nil if the script is not a valid lock contract.
func ParseLock(ctx *ParseContext) (*ParseResult, error) {
	scr := script.NewFromBytes(ctx.LockingScript)
	lock := lockup.Decode(scr, true)
	if lock == nil {
		return nil, nil
	}

	pkHash := types.PKHashFromBytes(lock.Address.PublicKeyHash)
	if pkHash == nil {
		return nil, nil
	}

	return &ParseResult{
		Tag:    TagLock,
		Data:   lock,
		Events: []string{"lock"},
		Owners: []*types.PKHash{pkHash},
	}, nil
}

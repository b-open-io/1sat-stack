package parse

const Tag1Sat = "1sat"

// Parse1Sat checks if the output contains exactly 1 satoshi.
// Returns a ParseResult with a "1sat" event if true, nil otherwise.
func Parse1Sat(ctx *ParseContext) (*ParseResult, error) {
	if ctx.Satoshis != 1 {
		return nil, nil
	}

	return &ParseResult{
		Tag:    Tag1Sat,
		Events: []string{Tag1Sat},
	}, nil
}

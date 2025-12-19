package parse

import (
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// ParseResult holds the output of parsing a single transaction output.
type ParseResult struct {
	Tag    string          // Identifier for this parse result (e.g., "p2pkh", "inscription")
	Data   any             // Parsed data structure
	Events []string        // Events to index for this output
	Owners []*types.PKHash // Owners (addresses) associated with this output
}

// ParseContext holds accumulated parse results and output metadata.
// Parsers can read from this to access data from previous parsing steps.
type ParseContext struct {
	Outpoint      *transaction.Outpoint // The outpoint being parsed
	LockingScript []byte                // Raw locking script
	Satoshis      uint64                // Output value

	// Accumulated results from parsers (keyed by tag)
	Results map[string]*ParseResult
}

// ParserFunc is a function that parses a script and returns a ParseResult
type ParserFunc func(ctx *ParseContext) *ParseResult

// All available parsers mapped by tag
var Parsers = map[string]ParserFunc{
	TagP2PKH:       ParseP2PKH,
	TagLock:        ParseLock,
	TagInscription: ParseInscription,
	TagBSV21:       ParseBSV21,
	TagOrdLock:     ParseOrdLock,
	TagCosign:      ParseCosign,
	TagShrug:       ParseShrug,
	TagBitcom:      ParseBitcom,
	TagB:           ParseB,
	TagMAP:         ParseMAP,
	TagAIP:         ParseAIP,
	TagBAP:         ParseBAP,
	TagSigma:       ParseSigma,
}

// DefaultTags is the recommended order of tags for comprehensive parsing
var DefaultTags = []string{
	TagP2PKH,       // Basic P2PKH address extraction
	TagLock,        // Lock protocol
	TagInscription, // 1Sat ordinals inscriptions
	TagBSV21,       // BSV21 fungible tokens
	TagOrdLock,     // Ordinal lock listings
	TagCosign,      // Cosign protocol
	TagShrug,       // Shrug tokens
	TagBitcom,      // Base bitcom parser (must come before B, MAP, etc.)
	TagB,           // B:// protocol
	TagMAP,         // MAP protocol
	TagAIP,         // AIP signatures
	TagBAP,         // BAP identity
	TagSigma,       // SIGMA signatures
}

// Parse runs the specified parsers on an output and returns the results.
// If tags is nil or empty, all default parsers are run.
func Parse(outpoint *transaction.Outpoint, lockingScript []byte, satoshis uint64, tags []string) map[string]*ParseResult {
	ctx := &ParseContext{
		Outpoint:      outpoint,
		LockingScript: lockingScript,
		Satoshis:      satoshis,
		Results:       make(map[string]*ParseResult),
	}

	// Use default tags if none specified
	if len(tags) == 0 {
		tags = DefaultTags
	}

	// Run each parser in order
	for _, tag := range tags {
		if parser, ok := Parsers[tag]; ok {
			if result := parser(ctx); result != nil {
				ctx.Results[result.Tag] = result
			}
		}
	}

	return ctx.Results
}

// GetData retrieves parsed data by tag from results, with type assertion.
func GetData[T any](ctx *ParseContext, tag string) *T {
	if result := ctx.Results[tag]; result != nil {
		if data, ok := result.Data.(*T); ok {
			return data
		}
	}
	return nil
}

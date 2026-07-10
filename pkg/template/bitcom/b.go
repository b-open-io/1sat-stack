package bitcom

import (
	"github.com/bsv-blockchain/go-sdk/script"
)

// B PROTOCOL - PREFIX DATA MEDIA_TYPE ENCODING FILENAME

// BPrefix is the bitcom protocol prefix for B
const BPrefix = "19HxigV4QyBv3tHpQVcUEQyq1pzZVdoAut"

// Media types
type MediaType string

const (
	MediaTypeTextPlain    MediaType = "text/plain"
	MediaTypeTextMarkdown MediaType = "text/markdown"
	MediaTypeTextHTML     MediaType = "text/html"
	MediaTypeImagePNG     MediaType = "image/png"
	MediaTypeImageJPEG    MediaType = "image/jpeg"
)

type Encoding string

var (
	EncodingUTF8  Encoding = "utf-8"
	EncodingBinay Encoding = "binary"
)

// B represents B protocol data
type B struct {
	MediaType MediaType `json:"mediaType"`
	Encoding  Encoding  `json:"encoding"`
	Data      []byte    `json:"data"`
	Filename  string    `json:"filename,omitempty"`
}

// DecodeB processes and extracts B protocol data from a transaction script.
// The function expects the script to contain protocol data in the format:
// DATA MEDIA_TYPE ENCODING [FILENAME]
// Where FILENAME is optional. Returns nil if the script is invalid or cannot be parsed.
func DecodeB(data any) *B {
	scr := ToScript(data)
	if scr == nil {
		return nil
	}

	pos := ZERO
	var op *script.ScriptChunk
	var err error

	b := &B{}

	// Protocol order: PREFIX DATA MEDIA_TYPE ENCODING FILENAME
	// Skip prefix as it's already checked

	// Read DATA
	if op, err = scr.ReadOp(&pos); err != nil {
		return nil
	}
	b.Data = op.Data

	// Read MEDIA_TYPE
	if op, err = scr.ReadOp(&pos); err != nil {
		return nil
	}
	b.MediaType = MediaType(op.Data)

	// Read ENCODING
	if op, err = scr.ReadOp(&pos); err != nil {
		return nil
	}
	b.Encoding = Encoding(op.Data)

	// Try to read optional FILENAME
	if op, err = scr.ReadOp(&pos); err == nil {
		// Successfully read filename
		b.Filename = string(op.Data)
	}

	return b
}

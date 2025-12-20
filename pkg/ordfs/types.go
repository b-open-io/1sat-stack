package ordfs

import (
	"encoding/json"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// Request represents a content request
type Request struct {
	Outpoint *transaction.Outpoint
	Txid     *chainhash.Hash
	Seq      *int
	Content  bool
	Map      bool
	Output   bool
	Parent   bool
}

// Response represents parsed content
type Response struct {
	Outpoint      *transaction.Outpoint `json:"outpoint,omitempty"`
	Origin        *transaction.Outpoint `json:"origin,omitempty"`
	ContentType   string                `json:"contentType,omitempty"`
	Content       []byte                `json:"content,omitempty"`
	ContentLength int                   `json:"contentLength,omitempty"`
	Map           json.RawMessage       `json:"map,omitempty"`
	Sequence      int                   `json:"sequence,omitempty"`
	Output        []byte                `json:"output,omitempty"`
	Parent        *transaction.Outpoint `json:"parent,omitempty"`
}

// Resolution holds the result of ordinal resolution
type Resolution struct {
	Origin   *transaction.Outpoint
	Current  *transaction.Outpoint
	Content  *transaction.Outpoint
	Map      *transaction.Outpoint
	Parent   *transaction.Outpoint
	Sequence int
}

// ChainEntry represents a single entry in the ordinal chain
type ChainEntry struct {
	Outpoint        *transaction.Outpoint
	RelativeSeq     int
	ContentOutpoint *transaction.Outpoint
	MapOutpoint     *transaction.Outpoint
	ParentOutpoint  *transaction.Outpoint
}

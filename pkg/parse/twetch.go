package parse

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"strconv"

	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bsv-blockchain/go-sdk/script"
)

// TagTwetch tags Twetch NFT outputs ("twonks"), such as the Muties collection.
const TagTwetch = "twetch"

// Twetch NFTs predate 1Sat ordinals and use their own locking script:
//
//	OP_HASH160 <20-byte hash> OP_EQUALVERIFY
//	OP_DUP OP_HASH160 <owner pkh> OP_EQUALVERIFY OP_CHECKSIG
//	OP_RETURN <JSON metadata>
//
// Spending one takes the owner's signature and public key plus the preimage of the
// hash. A transfer re-creates the same script with the new owner's pkh and the same
// metadata. Example: the 456 Muties in tx
// a7595e9f7400390d34f193a4011212ddebc7c2065716db525c399bb91856665b.
const (
	twetchHashLockLen = 23                     // a9 14 <20> 88
	twetchPrefixLen   = twetchHashLockLen + 25 // plus a P2PKH
)

// Twetch is the decoded metadata of a Twetch NFT output.
type Twetch struct {
	HashLock    string            `json:"hashLock"`
	Title       string            `json:"title,omitempty"`
	Description string            `json:"description,omitempty"`
	Image       string            `json:"image,omitempty"`
	Number      int               `json:"number,omitempty"`
	Series      int               `json:"series,omitempty"`
	Rarity      string            `json:"rarity,omitempty"`
	Rank        int               `json:"rank,omitempty"`
	GltfModel   string            `json:"gltfModel,omitempty"`
	Background  string            `json:"background,omitempty"`
	Attributes  []TwetchAttribute `json:"attributes,omitempty"`
}

// TwetchAttribute is one trait of a Twetch NFT.
type TwetchAttribute struct {
	TraitType string `json:"traitType"`
	Value     string `json:"value"`
	Rarity    string `json:"rarity,omitempty"`
}

// ParseTwetch recognizes a Twetch NFT output, attributes it to the owner in its P2PKH
// part, and decodes its JSON metadata. ParseP2PKH misses these outputs because the
// P2PKH is not at the start of the script.
func ParseTwetch(ctx *ParseContext) (*ParseResult, error) {
	s := ctx.LockingScript
	if len(s) <= twetchPrefixLen ||
		s[0] != script.OpHASH160 || s[1] != script.OpDATA20 || s[22] != script.OpEQUALVERIFY {
		return nil, nil
	}
	pkh := s[twetchHashLockLen : twetchHashLockLen+25]
	if pkh[0] != script.OpDUP || pkh[1] != script.OpHASH160 || pkh[2] != script.OpDATA20 ||
		pkh[23] != script.OpEQUALVERIFY || pkh[24] != script.OpCHECKSIG {
		return nil, nil
	}
	if s[twetchPrefixLen] != script.OpRETURN {
		return nil, nil
	}
	pos := twetchPrefixLen + 1
	op, err := script.NewFromBytes(s).ReadOp(&pos)
	if err != nil || len(op.Data) == 0 {
		return nil, nil
	}
	meta := decodeTwetchMeta(op.Data)
	if meta == nil {
		return nil, nil
	}
	meta.HashLock = hex.EncodeToString(s[2:22])

	owner := types.PKHashFromBytes(pkh[3:23])
	if owner == nil {
		return nil, nil
	}
	return &ParseResult{
		Tag:    TagTwetch,
		Data:   meta,
		Events: []string{TagTwetch},
		Owners: []*types.PKHash{owner},
	}, nil
}

// decodeTwetchMeta reads the metadata leniently: number fields may be numbers or
// strings, and unknown fields are ignored. It returns nil unless the data is a JSON
// object with a title or an image.
func decodeTwetchMeta(data []byte) *Twetch {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		return nil
	}
	meta := &Twetch{
		Title:       jsonString(raw["title"]),
		Description: jsonString(raw["description"]),
		Image:       jsonString(raw["image"]),
		Number:      jsonInt(raw["number"]),
		Series:      jsonInt(raw["series"]),
		Rarity:      jsonString(raw["rarity"]),
		Rank:        jsonInt(raw["rank"]),
		GltfModel:   jsonString(raw["gltfModel"]),
		Background:  jsonString(raw["background"]),
	}
	if meta.Title == "" && meta.Image == "" {
		return nil
	}
	if attrs, ok := raw["attributes"].([]any); ok {
		for _, a := range attrs {
			m, ok := a.(map[string]any)
			if !ok {
				continue
			}
			attr := TwetchAttribute{
				TraitType: jsonString(m["trait_type"]),
				Value:     jsonString(m["value"]),
				Rarity:    jsonString(m["rarity"]),
			}
			if attr.TraitType != "" || attr.Value != "" {
				meta.Attributes = append(meta.Attributes, attr)
			}
		}
	}
	return meta
}

func jsonString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	}
	return ""
}

func jsonInt(v any) int {
	switch t := v.(type) {
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return int(n)
		}
		if f, err := t.Float64(); err == nil {
			return int(f)
		}
	case string:
		if n, err := strconv.Atoi(t); err == nil {
			return n
		}
	}
	return 0
}

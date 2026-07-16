package parse

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/b-open-io/1sat-stack/pkg/template/bitcom"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// TagCollection is the parser tag for 1Sat collection MAP interpretation.
const TagCollection = "collection"

// ParseCollection interprets MAP subTypeData for collectionItem outputs.
// Requires ParseMAP to have run first.
//
// Emits:
//   - map:collectionId:{id} from subTypeData.collectionId
//     (same-tx "_N" normalized to an absolute outpoint)
func ParseCollection(ctx *ParseContext) (*ParseResult, error) {
	m := GetData[bitcom.Map](ctx, TagMAP)
	if m == nil || m.Data == nil {
		return nil, nil
	}
	if m.Data["subType"] != "collectionItem" {
		return nil, nil
	}

	collectionID := collectionIDFromSubTypeData(m.Data)
	if collectionID == "" {
		return nil, nil
	}
	collectionID = NormalizeCollectionID(collectionID, ctx.Outpoint)
	if collectionID == "" {
		return nil, nil
	}

	return &ParseResult{
		Tag:    TagCollection,
		Data:   collectionID,
		Events: []string{"map:collectionId:" + collectionID},
	}, nil
}

// collectionIDFromSubTypeData reads collectionId from MAP subTypeData JSON.
func collectionIDFromSubTypeData(data map[string]string) string {
	if data == nil {
		return ""
	}
	raw, ok := data["subTypeData"]
	if !ok || raw == "" {
		return ""
	}
	var sub struct {
		CollectionID string `json:"collectionId"`
	}
	if err := json.Unmarshal([]byte(raw), &sub); err != nil {
		return ""
	}
	return sub.CollectionID
}

// NormalizeCollectionID expands same-transaction relative collection IDs of the
// form "_N" to "{txid}_N" using the output being parsed. Absolute outpoints and
// other values are returned unchanged.
func NormalizeCollectionID(collectionID string, outpoint *transaction.Outpoint) string {
	if outpoint == nil || !strings.HasPrefix(collectionID, "_") {
		return collectionID
	}
	index, err := strconv.ParseUint(strings.TrimPrefix(collectionID, "_"), 10, 32)
	if err != nil {
		return collectionID
	}
	return (&transaction.Outpoint{
		Txid:  outpoint.Txid,
		Index: uint32(index),
	}).OrdinalString()
}

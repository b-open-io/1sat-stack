package parse

import (
	"encoding/json"

	"github.com/b-open-io/1sat-stack/pkg/template/bitcom"
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
	collectionID = NormalizeRelativeOutpoint(collectionID, ctx.Outpoint)
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



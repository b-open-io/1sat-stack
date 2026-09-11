package ordlock

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/b-open-io/1sat-stack/pkg/ordfs"
	"github.com/b-open-io/1sat-stack/pkg/template/inscription"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// enrichListingData fills content-type / name / origin from the inscription or
// ORDFS. Shared by the OrdLock v2 lookup (see lookup_v2.go). Returns nil for
// bsv-20 (token) content, which is not a marketplace listing.
//
// The deprecated v1 topic is no longer registered. Its template decoder remains
// in pkg/parse for owner indexing and cancellation through address sync.
func enrichListingData(ctx context.Context, ordfsClient *ordfs.Ordfs, outpoint *transaction.Outpoint, lockingScript *script.Script, ld *listingData) *listingData {
	insc := inscription.Decode(lockingScript)
	if insc != nil {
		ld.contentType = insc.File.Type

		if ld.contentType == "application/bsv-20" {
			return nil
		}

		if ld.contentType == "application/op-ns" {
			ld.name = string(insc.File.Content)
		}

		if insc.Parent != nil {
			ld.origin = insc.Parent
		}

		if ld.name == "" {
			if nameField, ok := insc.Fields["name"]; ok {
				ld.name = string(nameField)
			}
		}
	}

	if insc == nil && ordfsClient != nil {
		seq := 0
		resp, err := ordfsClient.Load(ctx, &ordfs.Request{
			Outpoint: outpoint,
			Seq:      &seq,
			Content:  true,
			Map:      true,
		})
		if err == nil && resp != nil {
			if resp.Origin != nil {
				ld.origin = resp.Origin
			}
			if resp.ContentType != "" {
				ld.contentType = strings.Split(resp.ContentType, ";")[0]
				ld.contentType = strings.TrimSpace(ld.contentType)
			}
			if ld.contentType == "application/bsv-20" {
				return nil
			}
			if ld.contentType == "application/op-ns" && len(resp.Content) > 0 {
				ld.name = string(resp.Content)
			}
			if ld.name == "" && resp.Map != nil {
				var mapData map[string]string
				if err := json.Unmarshal(resp.Map, &mapData); err == nil {
					if n, ok := mapData["name"]; ok && n != "" {
						ld.name = n
					}
				}
			}
		}
	}

	if ld.origin == nil {
		ld.origin = outpoint
	}

	return ld
}

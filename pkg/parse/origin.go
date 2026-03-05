package parse

import (
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	"github.com/b-open-io/1sat-stack/pkg/ordfs"
	"github.com/b-open-io/1sat-stack/pkg/types"
	"github.com/bitcoin-sv/go-templates/template/p2pkh"
	"github.com/bsv-blockchain/go-sdk/script"
)

const TagOrigin = "origin"

// ParseOrigin handles two responsibilities for 1-sat outputs:
// 1. Owner extraction from composite scripts (P2PKH prefix on scripts > 25 bytes)
// 2. Origin/type/name resolution via ORDFS for non-inscription outputs
func ParseOrigin(ctx *ParseContext) *ParseResult {
	if ctx.Results[Tag1Sat] == nil {
		return nil
	}

	var owners []*types.PKHash
	var events []string
	isInscription := ctx.Results[TagInscription] != nil

	// Extract owner from composite scripts where P2PKH is the first 25 bytes.
	// Pure P2PKH (exactly 25 bytes) is already handled by ParseP2PKH.
	if len(ctx.LockingScript) > 25 {
		prefix := script.Script(ctx.LockingScript[:25])
		if addr := p2pkh.Decode(&prefix, true); addr != nil {
			if owner := types.PKHashFromBytes(addr.PublicKeyHash); owner != nil {
				owners = append(owners, owner)
			}
		}
	}

	// Resolve origin metadata via ORDFS for non-inscription 1-sat outputs
	if !isInscription && ctx.Ordfs != nil && ctx.Ctx != nil {
		seq := 0
		resp, err := ctx.Ordfs.Load(ctx.Ctx, &ordfs.Request{
			Outpoint: ctx.Outpoint,
			Seq:      &seq,
			Content:  false,
			Map:      true,
		})
		if err != nil {
			if !errors.Is(err, ordfs.ErrNotFound) {
				slog.Warn("origin resolution failed",
					"outpoint", ctx.Outpoint.String(),
					"error", err,
				)
			}
		} else {
			if resp.Origin != nil {
				events = append(events, "origin:"+resp.Origin.String())
			}
			if resp.ContentType != "" {
				fullType := strings.Split(resp.ContentType, ";")[0]
				fullType = strings.TrimSpace(fullType)
				if fullType != "" {
					parts := strings.Split(fullType, "/")
					if len(parts) > 0 && parts[0] != "" {
						events = append(events, "type:"+parts[0])
					}
					events = append(events, "type:"+fullType)
				}
			}
			if resp.Map != nil {
				var mapData map[string]string
				if err := json.Unmarshal(resp.Map, &mapData); err == nil {
					if name, ok := mapData["name"]; ok && name != "" {
						events = append(events, "name:"+name)
					}
				}
			}
		}
	}

	if len(owners) == 0 && len(events) == 0 {
		return nil
	}

	return &ParseResult{
		Tag:    TagOrigin,
		Owners: owners,
		Events: events,
	}
}


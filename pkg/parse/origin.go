package parse

import (
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	"github.com/b-open-io/1sat-stack/pkg/ordfs"
)

const TagOrigin = "origin"

// ParseOrigin resolves origin metadata for 1-sat outputs that are not inscriptions.
// Requires Ctx and Ordfs to be set on ParseContext; skips silently if unavailable.
func ParseOrigin(ctx *ParseContext) *ParseResult {
	if ctx.Ordfs == nil || ctx.Ctx == nil {
		return nil
	}

	// Only fire on 1-sat outputs
	if ctx.Results[Tag1Sat] == nil {
		return nil
	}

	// Skip inscriptions — they already have type/origin from ParseInscription
	if ctx.Results[TagInscription] != nil {
		return nil
	}

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
		return nil
	}

	var events []string

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

	if len(events) == 0 {
		return nil
	}

	return &ParseResult{
		Tag:    TagOrigin,
		Events: events,
	}
}


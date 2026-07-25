package ordfs

import (
	"context"
	"errors"
	"fmt"

	"github.com/b-open-io/1sat-stack/pkg/httputil"
	"github.com/gofiber/fiber/v2"
)

// RegisterThumb registers the wildcard thumbnail endpoint.
// Mounted separately from Register for the same reason as RegisterContent:
// wildcard routes must come last.
func (r *Routes) RegisterThumb(router fiber.Router) {
	router.Get("/*", r.HandleThumb)
}

// HandleThumb serves a resized copy of an image inscription
// @Summary Get a thumbnail of inscription content
// @Description Render an image inscription at a bounded width. Widths snap up to the nearest supported size and results are cached by outpoint. Non-raster content is rejected; SVG is served unchanged by /content since it already scales.
// @Tags ordfs
// @Produce image/jpeg,image/png
// @Param path path string true "Outpoint (txid_vout) or txid, optionally with :seq"
// @Param w query int false "Target width in pixels (snaps up to the nearest supported width)"
// @Param q query int false "JPEG quality, 1-100 (rounded to the nearest 5)"
// @Success 200 {file} binary "Thumbnail"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 404 {object} map[string]string "Not found"
// @Failure 415 {object} map[string]string "Content is not a raster image"
// @Router /thumb/{path} [get]
func (r *Routes) HandleThumb(c *fiber.Ctx) error {
	path := c.Params("*")
	if path == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "path is required",
		})
	}

	pp, err := parsePointerPath(path)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if pp.FilePath != "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "thumbnails are not available for directory paths",
		})
	}

	outpoint, isTxid, err := resolvePointerToOutpoint(pp.Pointer)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	width := SnapThumbWidth(c.QueryInt("w", DefaultThumbWidth))
	quality := SnapThumbQuality(c.QueryInt("q", DefaultThumbQuality))

	// Serve from cache before touching the chain. Skipped for latest-sequence
	// lookups, whose target can move.
	isLatest := pp.Seq != nil && *pp.Seq == -1
	cache := r.ordfs.Cache()
	cacheKey := thumbCacheKey(outpoint.String(), width, quality)
	if cache != nil && !isLatest {
		if entry, err := cache.Get(c.Context(), cacheKey); err == nil && len(entry) > 0 {
			if format, payload, ok := decodeThumbEntry(entry); ok {
				return r.sendThumb(c, payload, format, outpoint.String(), isLatest)
			}
		}
	}

	var req *Request
	if isTxid {
		req = &Request{Txid: &outpoint.Txid, Seq: pp.Seq, Content: true}
	} else {
		req = &Request{Outpoint: outpoint, Seq: pp.Seq, Content: true}
	}

	loadCtx, loadCancel := context.WithTimeout(c.Context(), ResolveTimeout)
	defer loadCancel()
	resp, err := r.ordfs.Load(loadCtx, req)
	if err != nil {
		r.logger.Debug("failed to load content for thumbnail", "path", path, "error", err)
		if errors.Is(err, ErrNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "inscription not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	if !IsThumbnailable(resp.ContentType) {
		return c.Status(fiber.StatusUnsupportedMediaType).JSON(fiber.Map{
			"error":       fmt.Sprintf("cannot thumbnail content type %q", resp.ContentType),
			"contentType": resp.ContentType,
		})
	}

	payload, format, err := renderThumbnail(resp.Content, resp.ContentType, width, quality)
	if err != nil {
		if errors.Is(err, ErrNotThumbnailable) {
			return c.Status(fiber.StatusUnsupportedMediaType).JSON(fiber.Map{"error": err.Error()})
		}
		r.logger.Warn("thumbnail render failed", "path", path, "contentType", resp.ContentType, "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to render thumbnail"})
	}

	// A failed cache write costs a re-render, not a wrong response.
	if cache != nil && !isLatest {
		if err := cache.Set(c.Context(), cacheKey, encodeThumbEntry(format, payload)); err != nil {
			r.logger.Debug("failed to cache thumbnail", "key", cacheKey, "error", err)
		}
	}

	resolved := outpoint.String()
	if resp.Outpoint != nil {
		resolved = resp.Outpoint.String()
	}
	return r.sendThumb(c, payload, format, resolved, isLatest)
}

func (r *Routes) sendThumb(c *fiber.Ctx, payload []byte, format thumbFormat, outpoint string, isLatest bool) error {
	c.Set("Content-Type", format.contentType())
	c.Set("X-Outpoint", outpoint)
	if isLatest {
		httputil.SetNoStore(c)
	} else {
		httputil.SetImmutable(c)
	}
	return c.Send(payload)
}

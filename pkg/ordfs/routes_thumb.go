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
	// Only an explicit latest-sequence request is mutable; the chain can advance
	// under it. This governs the response header, not whether we may cache the
	// render — see below.
	isLatest := pp.Seq != nil && *pp.Seq == -1

	newRequest := func(withContent bool) *Request {
		if isTxid {
			return &Request{Txid: &outpoint.Txid, Seq: pp.Seq, Content: withContent}
		}
		return &Request{Outpoint: outpoint, Seq: pp.Seq, Content: withContent}
	}

	loadCtx, loadCancel := context.WithTimeout(c.Context(), ResolveTimeout)
	defer loadCancel()

	// Resolve first, without pulling content bytes. parseOutput fills in the
	// content type either way, so this is enough to reject non-images and to
	// learn which concrete outpoint the pointer landed on. Resolution is already
	// memoized by the parsed:/OriginStore layers, so this is cheap on repeat.
	head, err := r.ordfs.Load(loadCtx, newRequest(false))
	if err != nil {
		r.logger.Debug("failed to resolve thumbnail pointer", "path", path, "error", err)
		if errors.Is(err, ErrNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "inscription not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	if !IsThumbnailable(head.ContentType) {
		return c.Status(fiber.StatusUnsupportedMediaType).JSON(fiber.Map{
			"error":       fmt.Sprintf("cannot thumbnail content type %q", head.ContentType),
			"contentType": head.ContentType,
		})
	}

	// Key on the outpoint the pointer resolved to, never on the pointer itself.
	// The requested form carries a seq that selects different content from the
	// same outpoint, so keying on it would let /thumb/x_0:0 and /thumb/x_0:5
	// collide. Resolved outpoints are content addressed and immutable, which is
	// the same basis the parsed:/merged: caches use, and it means every pointer
	// that lands on one revision shares a single rendered thumbnail.
	resolved := outpoint.String()
	if head.Outpoint != nil {
		resolved = head.Outpoint.String()
	}

	cache := r.ordfs.Cache()
	cacheKey := thumbCacheKey(resolved, width, quality)
	if cache != nil {
		if entry, err := cache.Get(c.Context(), cacheKey); err == nil && len(entry) > 0 {
			if format, payload, ok := decodeThumbEntry(entry); ok {
				return r.sendThumb(c, payload, format, resolved, isLatest)
			}
		}
	}

	full, err := r.ordfs.Load(loadCtx, newRequest(true))
	if err != nil {
		r.logger.Debug("failed to load content for thumbnail", "path", path, "error", err)
		if errors.Is(err, ErrNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "inscription not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	payload, format, err := renderThumbnail(full.Content, full.ContentType, width, quality)
	if err != nil {
		if errors.Is(err, ErrNotThumbnailable) {
			return c.Status(fiber.StatusUnsupportedMediaType).JSON(fiber.Map{"error": err.Error()})
		}
		r.logger.Warn("thumbnail render failed", "path", path, "contentType", full.ContentType, "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to render thumbnail"})
	}

	// Cached even for seq=-1: the entry is keyed by the resolved outpoint, which
	// is immutable. Only the HTTP response must stay uncacheable, because a later
	// request for the latest may resolve somewhere else entirely.
	// A failed write costs a re-render, not a wrong answer.
	if cache != nil {
		if err := cache.Set(c.Context(), cacheKey, encodeThumbEntry(format, payload)); err != nil {
			r.logger.Debug("failed to cache thumbnail", "key", cacheKey, "error", err)
		}
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

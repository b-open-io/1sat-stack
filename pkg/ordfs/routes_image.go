package ordfs

import (
	"context"
	"errors"
	"fmt"
	"image"
	"log/slog"

	"github.com/b-open-io/1sat-stack/pkg/httputil"
	"github.com/gofiber/fiber/v2"
)

// RegisterImage registers the wildcard image-transform endpoint.
// Mounted separately from Register for the same reason as RegisterContent:
// wildcard routes must come last.
func (r *Routes) RegisterImage(router fiber.Router) {
	router.Get("/*", r.HandleImage)
}

// WarmImageEncoders performs a throwaway encode so the WebAssembly runtimes
// backing WebP and AVIF are compiled before the first real request. Without it,
// whichever request arrives first absorbs roughly a second of one-time
// initialisation. Safe to call in a goroutine; failures are advisory.
func WarmImageEncoders(logger *slog.Logger) {
	pixel := image.NewRGBA(image.Rect(0, 0, 1, 1))
	for _, f := range []OutputFormat{FormatWebP, FormatAVIF} {
		if _, err := encode(pixel, f, DefaultImageQuality); err != nil && logger != nil {
			logger.Debug("image encoder warmup failed", "format", f, "error", err)
		}
	}
}

// HandleImage serves a transformed copy of an image inscription
// @Summary Transform inscription content
// @Description Render an image inscription at a bounded size. Dimensions snap up to supported widths and results are cached by resolved outpoint. Non-raster content is rejected; SVG is served unchanged by /content since it already scales.
// @Tags ordfs
// @Produce image/jpeg,image/png,image/webp,image/avif
// @Param path path string true "Outpoint (txid_vout) or txid, optionally with :seq"
// @Param w query int false "Target width, snaps up to the nearest supported width"
// @Param h query int false "Target height, snaps up to the nearest supported width"
// @Param fit query string false "How the source maps onto the box" Enums(limit, fit, fill, pad, scale)
// @Param g query string false "Gravity for fill and pad" Enums(center, north, south, east, west, northeast, northwest, southeast, southwest)
// @Param f query string false "Output format; auto negotiates from Accept" Enums(auto, jpeg, png, webp, avif)
// @Param q query int false "Quality 1-100, rounded to the nearest 5"
// @Success 200 {file} binary "Transformed image"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 404 {object} map[string]string "Not found"
// @Failure 415 {object} map[string]string "Content is not a raster image"
// @Router /image/{path} [get]
func (r *Routes) HandleImage(c *fiber.Ctx) error {
	path := c.Params("*")
	if path == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "path is required"})
	}

	pp, err := parsePointerPath(path)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if pp.FilePath != "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "image transforms are not available for directory paths",
		})
	}

	outpoint, isTxid, err := resolvePointerToOutpoint(pp.Pointer)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	params := ParseTransformQuery(
		c.Query("w"), c.Query("h"), c.Query("fit"),
		c.Query("g"), c.Query("f"), c.Query("q"),
	)
	accept := c.Get("Accept")
	negotiated := params.Format == FormatAuto

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
		r.logger.Debug("failed to resolve image pointer", "path", path, "error", err)
		if errors.Is(err, ErrNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "inscription not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	if !IsTransformable(head.ContentType) {
		return c.Status(fiber.StatusUnsupportedMediaType).JSON(fiber.Map{
			"error":       fmt.Sprintf("cannot transform content type %q", head.ContentType),
			"contentType": head.ContentType,
		})
	}

	// Key on the outpoint the pointer resolved to, never on the pointer itself.
	// The requested form carries a seq that selects different content from the
	// same outpoint, so keying on it would let /image/x_0:0 and /image/x_0:5
	// collide. Resolved outpoints are content addressed and immutable, which is
	// the same basis the parsed:/merged: caches use, and it means every pointer
	// that lands on one revision shares a single rendered result.
	resolved := outpoint.String()
	if head.Outpoint != nil {
		resolved = head.Outpoint.String()
	}

	// Negotiation happens before the lookup so the key names the encoding
	// actually served. Alpha is unknown until decode, so assume none here; a
	// transparent source under f=auto resolves to PNG on the render path and is
	// cached and re-served under that key.
	lookup := params
	lookup.Format = NegotiateFormat(params.Format, accept, false)

	cache := r.ordfs.Cache()
	if cache != nil {
		if payload, err := cache.Get(c.Context(), imageCacheKey(resolved, lookup)); err == nil && len(payload) > 0 {
			return r.sendImage(c, payload, lookup.Format, resolved, isLatest, negotiated)
		}
	}

	full, err := r.ordfs.Load(loadCtx, newRequest(true))
	if err != nil {
		r.logger.Debug("failed to load content for image", "path", path, "error", err)
		if errors.Is(err, ErrNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "inscription not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	payload, format, err := Transform(full.Content, full.ContentType, params, accept)
	if err != nil {
		if errors.Is(err, ErrNotTransformable) {
			return c.Status(fiber.StatusUnsupportedMediaType).JSON(fiber.Map{"error": err.Error()})
		}
		r.logger.Warn("image transform failed", "path", path, "contentType", full.ContentType, "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to transform image"})
	}

	// Cached even for seq=-1: the entry is keyed by the resolved outpoint, which
	// is immutable. Only the HTTP response must stay uncacheable, because a later
	// request for the latest may resolve somewhere else entirely.
	// A failed write costs a re-render, not a wrong answer.
	if cache != nil {
		stored := params
		stored.Format = format
		if err := cache.Set(c.Context(), imageCacheKey(resolved, stored), payload); err != nil {
			r.logger.Debug("failed to cache image", "outpoint", resolved, "error", err)
		}
	}

	return r.sendImage(c, payload, format, resolved, isLatest, negotiated)
}

func (r *Routes) sendImage(c *fiber.Ctx, payload []byte, format OutputFormat, outpoint string, isLatest, negotiated bool) error {
	c.Set("Content-Type", format.ContentType())
	c.Set("X-Outpoint", outpoint)
	if negotiated {
		// The body depends on Accept, so shared caches must key on it too.
		c.Set("Vary", "Accept")
	}
	if isLatest {
		httputil.SetNoStore(c)
	} else {
		httputil.SetImmutable(c)
	}
	return c.Send(payload)
}

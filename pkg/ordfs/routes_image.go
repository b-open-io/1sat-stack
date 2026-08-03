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

// HandleImage serves a transformed copy of an image inscription at a concrete
// outpoint. Callers resolve ordinality via metadata/content first; this endpoint
// does not accept :seq. SVG is passed through unchanged; other non-rasters 415.
// @Summary Transform inscription content
// @Description Render a raster inscription at a bounded size, or pass SVG through. Path is a concrete outpoint (or bare txid) only — no :seq. Caching is CDN via immutable Cache-Control.
// @Tags ordfs
// @Produce image/jpeg,image/png,image/webp,image/avif,image/svg+xml
// @Param path path string true "Outpoint (txid_vout) or bare txid — no :seq"
// @Param w query int false "Target width, snaps up to the nearest supported width"
// @Param h query int false "Target height, snaps up to the nearest supported width"
// @Param fit query string false "How the source maps onto the box" Enums(limit, fit, fill, pad, scale)
// @Param g query string false "Gravity for fill and pad" Enums(center, north, south, east, west, northeast, northwest, southeast, southwest)
// @Param f query string false "Output format; auto negotiates from Accept" Enums(auto, jpeg, png, webp, avif)
// @Param q query int false "Quality 1-100, rounded to the nearest 5"
// @Success 200 {file} binary "Transformed image or passthrough SVG"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 404 {object} map[string]string "Not found"
// @Failure 415 {object} map[string]string "Content is not an image"
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
	if pp.Seq != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "image transforms require a concrete outpoint; resolve :seq via metadata or content first",
		})
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

	newRequest := func(withContent bool) *Request {
		if isTxid {
			return &Request{Txid: &outpoint.Txid, Content: withContent}
		}
		return &Request{Outpoint: outpoint, Content: withContent}
	}

	loadCtx, loadCancel := context.WithTimeout(c.Context(), ResolveTimeout)
	defer loadCancel()

	// Type check without pulling content bytes when the parse cache can answer.
	head, err := r.ordfs.Load(loadCtx, newRequest(false))
	if err != nil {
		r.logger.Debug("failed to resolve image pointer", "path", path, "error", err)
		if errors.Is(err, ErrNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "inscription not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	xOut := outpoint.String()
	if head.Outpoint != nil {
		xOut = head.Outpoint.String()
	}

	if IsSVG(head.ContentType) {
		full, err := r.ordfs.Load(loadCtx, newRequest(true))
		if err != nil {
			r.logger.Debug("failed to load svg content", "path", path, "error", err)
			if errors.Is(err, ErrNotFound) {
				return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "inscription not found"})
			}
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		return r.sendRawImage(c, full.Content, "image/svg+xml", xOut, false)
	}

	if !IsTransformable(head.ContentType) {
		return c.Status(fiber.StatusUnsupportedMediaType).JSON(fiber.Map{
			"error":       fmt.Sprintf("cannot transform content type %q", head.ContentType),
			"contentType": head.ContentType,
		})
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

	return r.sendRawImage(c, payload, format.ContentType(), xOut, negotiated)
}

func (r *Routes) sendRawImage(c *fiber.Ctx, payload []byte, contentType, outpoint string, negotiated bool) error {
	c.Set("Content-Type", contentType)
	c.Set("X-Outpoint", outpoint)
	if negotiated {
		c.Set("Vary", "Accept")
	}
	// Concrete outpoint only — body is content-addressed and permanently cacheable.
	httputil.SetImmutable(c)
	return c.Send(payload)
}

package ordfs

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"strings"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// Inscriptions are served at their original size — commonly several megabytes
// for a single image — and a grid of them is the dominant cost in any wallet or
// marketplace UI. Thumbnails are rendered on demand and cached by outpoint, so
// the expensive decode happens once per (image, width, quality) across all
// consumers rather than once per page view per visitor.

// ThumbWidths are the only widths a thumbnail is rendered at. Requests snap up
// to the nearest entry, which bounds the cache key space no matter what a
// client asks for.
var ThumbWidths = []int{16, 32, 48, 64, 96, 128, 192, 256, 384, 512, 640, 828, 1080, 1200, 1920}

const (
	// DefaultThumbWidth is used when no width is requested.
	DefaultThumbWidth = 384
	// DefaultThumbQuality is used when no quality is requested.
	DefaultThumbQuality = 75

	// maxThumbSourceBytes caps the encoded size accepted for decoding.
	maxThumbSourceBytes = 32 << 20
	// maxThumbSourcePixels caps decoded dimensions, guarding against
	// decompression bombs inscribed on chain.
	maxThumbSourcePixels = 100 << 20
)

// ErrNotThumbnailable indicates the content is not a raster image.
var ErrNotThumbnailable = errors.New("content is not a raster image")

// thumbFormat identifies the encoding of a cached thumbnail. Stored as the
// first byte of the cache entry so the content type survives a cache hit
// without a second lookup.
type thumbFormat byte

const (
	thumbJPEG thumbFormat = 1
	thumbPNG  thumbFormat = 2
)

func (f thumbFormat) contentType() string {
	if f == thumbPNG {
		return "image/png"
	}
	return "image/jpeg"
}

// IsThumbnailable reports whether a content type can be rendered as a raster
// thumbnail. SVG is excluded deliberately: it is already compact and scales
// losslessly, so rasterizing it would make things worse.
func IsThumbnailable(contentType string) bool {
	base := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	switch base {
	case "image/jpeg", "image/jpg", "image/png", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

// SnapThumbWidth rounds a requested width up to the nearest supported width.
func SnapThumbWidth(requested int) int {
	if requested <= 0 {
		return DefaultThumbWidth
	}
	for _, w := range ThumbWidths {
		if requested <= w {
			return w
		}
	}
	return ThumbWidths[len(ThumbWidths)-1]
}

// SnapThumbQuality clamps quality to 1-100 and rounds to the nearest 5, so
// arbitrary values cannot fragment the cache.
func SnapThumbQuality(requested int) int {
	if requested <= 0 {
		return DefaultThumbQuality
	}
	if requested > 100 {
		return 100
	}
	snapped := ((requested + 2) / 5) * 5
	if snapped < 5 {
		snapped = 5
	}
	if snapped > 100 {
		snapped = 100
	}
	return snapped
}

// renderThumbnail decodes src, scales it to fit within width while preserving
// aspect ratio, and re-encodes it. Images already narrower than the target are
// re-encoded at their original size rather than upscaled.
func renderThumbnail(src []byte, contentType string, width, quality int) ([]byte, thumbFormat, error) {
	if !IsThumbnailable(contentType) {
		return nil, 0, ErrNotThumbnailable
	}
	if len(src) > maxThumbSourceBytes {
		return nil, 0, fmt.Errorf("source image is %d bytes, over the %d byte limit", len(src), maxThumbSourceBytes)
	}

	cfg, _, err := image.DecodeConfig(bytes.NewReader(src))
	if err != nil {
		return nil, 0, fmt.Errorf("decode config: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, 0, errors.New("image has no dimensions")
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxThumbSourcePixels {
		return nil, 0, fmt.Errorf("image is %dx%d, over the pixel limit", cfg.Width, cfg.Height)
	}

	srcImg, _, err := image.Decode(bytes.NewReader(src))
	if err != nil {
		return nil, 0, fmt.Errorf("decode: %w", err)
	}

	bounds := srcImg.Bounds()
	dstW, dstH := bounds.Dx(), bounds.Dy()
	if dstW > width {
		dstH = int(float64(dstH) * float64(width) / float64(dstW))
		dstW = width
		if dstH < 1 {
			dstH = 1
		}
	}

	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	draw.CatmullRom.Scale(dst, dst.Bounds(), srcImg, bounds, draw.Src, nil)

	// Only genuinely transparent images pay PNG's size penalty. Most PNG
	// inscriptions are fully opaque, and JPEG is several times smaller for
	// them. The scan runs over the already-downscaled result, not the source.
	format := thumbJPEG
	if hasTransparency(dst) {
		format = thumbPNG
	}

	var out bytes.Buffer
	if format == thumbPNG {
		encoder := png.Encoder{CompressionLevel: png.BestCompression}
		if err := encoder.Encode(&out, dst); err != nil {
			return nil, 0, fmt.Errorf("encode png: %w", err)
		}
	} else if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: quality}); err != nil {
		return nil, 0, fmt.Errorf("encode jpeg: %w", err)
	}

	return out.Bytes(), format, nil
}

// hasTransparency reports whether any pixel is less than fully opaque.
func hasTransparency(img *image.RGBA) bool {
	// Alpha is the 4th byte of each RGBA pixel.
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] != 0xff {
			return true
		}
	}
	return false
}

// thumbCacheKey identifies a rendered thumbnail. The outpoint is content
// addressed, so the entry never needs invalidating.
func thumbCacheKey(outpoint string, width, quality int) string {
	return fmt.Sprintf("thumb:%s:%d:%d", outpoint, width, quality)
}

// encodeThumbEntry prefixes the payload with its format byte.
func encodeThumbEntry(format thumbFormat, payload []byte) []byte {
	return append([]byte{byte(format)}, payload...)
}

// decodeThumbEntry splits a cache entry back into format and payload.
func decodeThumbEntry(entry []byte) (thumbFormat, []byte, bool) {
	if len(entry) < 2 {
		return 0, nil, false
	}
	format := thumbFormat(entry[0])
	if format != thumbJPEG && format != thumbPNG {
		return 0, nil, false
	}
	return format, entry[1:], true
}

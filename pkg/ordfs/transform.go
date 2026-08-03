package ordfs

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"strconv"
	"strings"

	"github.com/gen2brain/avif"
	"github.com/gen2brain/webp"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// Inscriptions are served at their original size — commonly several megabytes
// for a single image — and a grid of them is the dominant cost in any wallet or
// marketplace UI. Derived images are rendered on demand and cached by resolved
// outpoint, so the expensive decode happens once per (image, transform) across
// all consumers rather than once per page view per visitor.
//
// The vocabulary follows Cloudinary's, which is the most widely understood in
// this space: a fit mode names how the source is mapped onto the requested box
// rather than leaving that behaviour implicit in the endpoint name.

// FitMode determines how a source image is mapped onto the requested box.
type FitMode string

const (
	// FitLimit fits inside the box preserving aspect ratio, never upscaling.
	FitLimit FitMode = "limit"
	// FitFit fits inside the box preserving aspect ratio, upscaling if needed.
	FitFit FitMode = "fit"
	// FitFill covers the box exactly, cropping the overflow at the gravity.
	FitFill FitMode = "fill"
	// FitPad fits inside the box and pads the remainder to the exact size.
	FitPad FitMode = "pad"
	// FitScale stretches to the exact box, ignoring aspect ratio.
	FitScale FitMode = "scale"
)

// Gravity anchors cropping for FitFill and padding for FitPad.
type Gravity string

const (
	GravityCenter    Gravity = "center"
	GravityNorth     Gravity = "north"
	GravitySouth     Gravity = "south"
	GravityEast      Gravity = "east"
	GravityWest      Gravity = "west"
	GravityNorthEast Gravity = "northeast"
	GravityNorthWest Gravity = "northwest"
	GravitySouthEast Gravity = "southeast"
	GravitySouthWest Gravity = "southwest"
)

// OutputFormat is the encoding of a derived image.
type OutputFormat string

const (
	// FormatAuto negotiates the best format the client accepts.
	FormatAuto OutputFormat = "auto"
	FormatJPEG OutputFormat = "jpeg"
	FormatPNG  OutputFormat = "png"
	FormatWebP OutputFormat = "webp"
	FormatAVIF OutputFormat = "avif"
)

// ContentType is the MIME type this format is served as.
func (f OutputFormat) ContentType() string {
	switch f {
	case FormatPNG:
		return "image/png"
	case FormatWebP:
		return "image/webp"
	case FormatAVIF:
		return "image/avif"
	default:
		return "image/jpeg"
	}
}

// ImageWidths are the only dimensions a derived image is rendered at. Requests
// snap up to the nearest entry, bounding the cache key space no matter what a
// client asks for. Every derived image is permanently cacheable because its
// source outpoint is content addressed, so an unbounded key space would be an
// unbounded storage commitment.
var ImageWidths = []int{16, 32, 48, 64, 96, 128, 192, 256, 384, 512, 640, 828, 1080, 1200, 1920}

const (
	// DefaultImageWidth applies when neither dimension is requested.
	DefaultImageWidth = 384
	// DefaultImageQuality applies when no quality is requested.
	DefaultImageQuality = 75

	// avifSpeed trades encode time for size. Returns collapse below 8 — speed 6
	// costs 15x the time of speed 10 for 11% fewer bytes — while speed 10 still
	// lands ~30% under WebP at 44ms for a 384px render.
	avifSpeed = 10
	// avifQualityScale maps the requested quality onto AVIF's scale, where a
	// given number is visually stronger than the same number in JPEG.
	avifQualityScale = 0.72

	maxSourceBytes  = 32 << 20
	maxSourcePixels = 100 << 20
)

// ErrNotTransformable indicates the content is not a raster image.
var ErrNotTransformable = errors.New("content is not a raster image")

// TransformParams is a fully resolved, snapped transform request.
type TransformParams struct {
	Width   int
	Height  int
	Fit     FitMode
	Gravity Gravity
	Format  OutputFormat
	Quality int
}

// IsTransformable reports whether a content type can be decoded as a raster
// image. SVG is excluded deliberately: it already scales losslessly and is
// small, so rasterizing it would be a regression.
func IsTransformable(contentType string) bool {
	switch baseContentType(contentType) {
	case "image/jpeg", "image/jpg", "image/png", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

func baseContentType(contentType string) string {
	return strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
}

// SnapDimension rounds a requested dimension up to the nearest supported width.
// Zero means "unconstrained on this axis".
func SnapDimension(requested int) int {
	if requested <= 0 {
		return 0
	}
	for _, w := range ImageWidths {
		if requested <= w {
			return w
		}
	}
	return ImageWidths[len(ImageWidths)-1]
}

// SnapQuality clamps quality to 1-100 and rounds to the nearest 5, so arbitrary
// values cannot fragment the cache.
func SnapQuality(requested int) int {
	if requested <= 0 {
		return DefaultImageQuality
	}
	if requested > 100 {
		return 100
	}
	snapped := ((requested + 2) / 5) * 5
	if snapped < 5 {
		return 5
	}
	if snapped > 100 {
		return 100
	}
	return snapped
}

// ParseFit maps a query value to a fit mode, falling back to limit.
func ParseFit(v string) FitMode {
	switch FitMode(strings.ToLower(strings.TrimSpace(v))) {
	case FitFit:
		return FitFit
	case FitFill:
		return FitFill
	case FitPad:
		return FitPad
	case FitScale:
		return FitScale
	default:
		return FitLimit
	}
}

// ParseGravity maps a query value to a gravity, falling back to center.
func ParseGravity(v string) Gravity {
	g := Gravity(strings.ToLower(strings.TrimSpace(v)))
	switch g {
	case GravityNorth, GravitySouth, GravityEast, GravityWest,
		GravityNorthEast, GravityNorthWest, GravitySouthEast, GravitySouthWest:
		return g
	default:
		return GravityCenter
	}
}

// ParseFormat maps a query value to an output format, falling back to auto.
func ParseFormat(v string) OutputFormat {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "jpeg", "jpg":
		return FormatJPEG
	case "png":
		return FormatPNG
	case "webp":
		return FormatWebP
	case "avif":
		return FormatAVIF
	default:
		return FormatAuto
	}
}

// NegotiateFormat resolves FormatAuto against what the client accepts,
// preferring the smallest encoding available. hasAlpha decides the fallback,
// since JPEG would flatten transparency onto an arbitrary background.
func NegotiateFormat(requested OutputFormat, accept string, hasAlpha bool) OutputFormat {
	if requested != FormatAuto {
		return requested
	}
	lower := strings.ToLower(accept)
	if strings.Contains(lower, "image/avif") {
		return FormatAVIF
	}
	if strings.Contains(lower, "image/webp") {
		return FormatWebP
	}
	if hasAlpha {
		return FormatPNG
	}
	return FormatJPEG
}

// ParseTransformQuery reads transform parameters from raw query values and
// snaps them. Format is left as requested; call NegotiateFormat once the source
// has been decoded and its alpha is known.
func ParseTransformQuery(w, h, fit, gravity, format, quality string) TransformParams {
	atoi := func(s string) int {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil {
			return 0
		}
		return n
	}

	p := TransformParams{
		Width:   SnapDimension(atoi(w)),
		Height:  SnapDimension(atoi(h)),
		Fit:     ParseFit(fit),
		Gravity: ParseGravity(gravity),
		Format:  ParseFormat(format),
		Quality: SnapQuality(atoi(quality)),
	}

	if p.Width == 0 && p.Height == 0 {
		p.Width = DefaultImageWidth
	}
	// Modes that produce an exact box need both dimensions to mean anything.
	// With only one supplied they degrade to a plain fit.
	if (p.Fit == FitFill || p.Fit == FitPad || p.Fit == FitScale) && (p.Width == 0 || p.Height == 0) {
		p.Fit = FitLimit
	}
	return p
}

// targetBox computes the output size and the source rectangle to sample from,
// in coordinates relative to the source image's origin.
func targetBox(srcW, srcH int, p TransformParams) (outW, outH int, srcRect image.Rectangle) {
	full := image.Rect(0, 0, srcW, srcH)

	boxW, boxH := p.Width, p.Height
	if boxW == 0 {
		boxW = srcW
	}
	if boxH == 0 {
		boxH = srcH
	}

	switch p.Fit {
	case FitScale:
		return boxW, boxH, full

	case FitFill:
		// Cover the box, then crop the overflow at the gravity.
		scale := max(float64(boxW)/float64(srcW), float64(boxH)/float64(srcH))
		cropW := min(max(int(float64(boxW)/scale), 1), srcW)
		cropH := min(max(int(float64(boxH)/scale), 1), srcH)
		x := gravityOffset(srcW-cropW, p.Gravity, true)
		y := gravityOffset(srcH-cropH, p.Gravity, false)
		return boxW, boxH, image.Rect(x, y, x+cropW, y+cropH)

	case FitPad, FitFit:
		// Same geometry; for pad the caller then pads out to the exact box.
		// Pass the raw dimensions so an omitted axis stays unconstrained.
		w, h := scaleWithin(srcW, srcH, p.Width, p.Height, true)
		return w, h, full

	default: // FitLimit
		w, h := scaleWithin(srcW, srcH, p.Width, p.Height, false)
		return w, h, full
	}
}

// scaleWithin fits srcW x srcH inside boxW x boxH preserving aspect ratio.
// A zero box dimension means unconstrained on that axis — treating it as the
// source size instead would cap the scale at 1 and stop fit from ever
// enlarging a small image. When allowUpscale is false the source is never
// enlarged regardless.
func scaleWithin(srcW, srcH, boxW, boxH int, allowUpscale bool) (int, int) {
	scale := math.Inf(1)
	if boxW > 0 {
		scale = math.Min(scale, float64(boxW)/float64(srcW))
	}
	if boxH > 0 {
		scale = math.Min(scale, float64(boxH)/float64(srcH))
	}
	if math.IsInf(scale, 1) {
		scale = 1
	}
	if !allowUpscale && scale > 1 {
		scale = 1
	}
	return max(int(float64(srcW)*scale), 1), max(int(float64(srcH)*scale), 1)
}

// gravityOffset positions a crop or pad of the given slack along one axis.
func gravityOffset(slack int, g Gravity, horizontal bool) int {
	if slack <= 0 {
		return 0
	}
	if horizontal {
		switch g {
		case GravityWest, GravityNorthWest, GravitySouthWest:
			return 0
		case GravityEast, GravityNorthEast, GravitySouthEast:
			return slack
		}
	} else {
		switch g {
		case GravityNorth, GravityNorthEast, GravityNorthWest:
			return 0
		case GravitySouth, GravitySouthEast, GravitySouthWest:
			return slack
		}
	}
	return slack / 2
}

// Transform decodes src, applies p, and encodes the result. accept is the
// client's Accept header, consulted only when p.Format is FormatAuto. Returns
// the encoded bytes and the format actually produced.
func Transform(src []byte, contentType string, p TransformParams, accept string) ([]byte, OutputFormat, error) {
	if !IsTransformable(contentType) {
		return nil, "", ErrNotTransformable
	}
	if len(src) > maxSourceBytes {
		return nil, "", fmt.Errorf("source image is %d bytes, over the %d byte limit", len(src), maxSourceBytes)
	}

	cfg, _, err := image.DecodeConfig(bytes.NewReader(src))
	if err != nil {
		return nil, "", fmt.Errorf("decode config: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, "", errors.New("image has no dimensions")
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxSourcePixels {
		return nil, "", fmt.Errorf("image is %dx%d, over the pixel limit", cfg.Width, cfg.Height)
	}

	srcImg, _, err := image.Decode(bytes.NewReader(src))
	if err != nil {
		return nil, "", fmt.Errorf("decode: %w", err)
	}

	bounds := srcImg.Bounds()
	outW, outH, srcRect := targetBox(bounds.Dx(), bounds.Dy(), p)

	scaled := image.NewRGBA(image.Rect(0, 0, outW, outH))
	draw.CatmullRom.Scale(scaled, scaled.Bounds(), srcImg, srcRect.Add(bounds.Min), draw.Src, nil)

	out := scaled
	if p.Fit == FitPad && (outW != p.Width || outH != p.Height) {
		out = padTo(scaled, p.Width, p.Height, p.Gravity)
	}

	format := NegotiateFormat(p.Format, accept, hasTransparency(out))
	encoded, err := encode(out, format, p.Quality)
	if err != nil {
		return nil, "", err
	}
	return encoded, format, nil
}

// padTo places a rendered image inside an exact box at the gravity, leaving the
// surrounding pixels transparent so the caller's own background shows through.
func padTo(src *image.RGBA, boxW, boxH int, g Gravity) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, boxW, boxH))
	x := gravityOffset(boxW-src.Bounds().Dx(), g, true)
	y := gravityOffset(boxH-src.Bounds().Dy(), g, false)
	draw.Draw(dst, src.Bounds().Add(image.Pt(x, y)), src, src.Bounds().Min, draw.Src)
	return dst
}

func encode(img *image.RGBA, format OutputFormat, quality int) ([]byte, error) {
	var buf bytes.Buffer
	switch format {
	case FormatPNG:
		enc := png.Encoder{CompressionLevel: png.BestCompression}
		if err := enc.Encode(&buf, img); err != nil {
			return nil, fmt.Errorf("encode png: %w", err)
		}
	case FormatWebP:
		if err := webp.Encode(&buf, img, webp.Options{Quality: quality}); err != nil {
			return nil, fmt.Errorf("encode webp: %w", err)
		}
	case FormatAVIF:
		if err := avif.Encode(&buf, img, avif.Options{
			Quality: int(float64(quality) * avifQualityScale),
			Speed:   avifSpeed,
		}); err != nil {
			return nil, fmt.Errorf("encode avif: %w", err)
		}
	default:
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
			return nil, fmt.Errorf("encode jpeg: %w", err)
		}
	}
	return buf.Bytes(), nil
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

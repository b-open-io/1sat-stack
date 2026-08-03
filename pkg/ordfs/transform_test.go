package ordfs

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func makePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func makeJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: 64, B: uint8(y % 256), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

func TestSnapDimension(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, 0},
		{-5, 0},
		{1, 16},
		{16, 16},
		{17, 32},
		{400, 512},
		{1920, 1920},
		{4000, 1920},
	}
	for _, tc := range cases {
		if got := SnapDimension(tc.in); got != tc.want {
			t.Errorf("SnapDimension(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestSnapQuality(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, DefaultImageQuality},
		{-1, DefaultImageQuality},
		{1, 5},
		{73, 75},
		{77, 75},
		{78, 80},
		{100, 100},
		{5000, 100},
	}
	for _, tc := range cases {
		if got := SnapQuality(tc.in); got != tc.want {
			t.Errorf("SnapQuality(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestIsTransformable(t *testing.T) {
	yes := []string{"image/png", "image/jpeg", "IMAGE/JPEG", "image/gif", "image/webp", "image/png; charset=binary"}
	for _, ct := range yes {
		if !IsTransformable(ct) {
			t.Errorf("IsTransformable(%q) = false, want true", ct)
		}
	}
	// SVG scales losslessly and is already small; rasterizing it would regress.
	no := []string{"image/svg+xml", "text/html", "application/json", "ord-fs/json", "video/mp4", ""}
	for _, ct := range no {
		if IsTransformable(ct) {
			t.Errorf("IsTransformable(%q) = true, want false", ct)
		}
	}
}

func makeTransparentPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			a := uint8(255)
			if x < w/2 {
				a = 0
			}
			img.Set(x, y, color.RGBA{R: 200, G: 100, B: 50, A: a})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func TestHasTransparency(t *testing.T) {
	opaque := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for i := 3; i < len(opaque.Pix); i += 4 {
		opaque.Pix[i] = 0xff
	}
	if hasTransparency(opaque) {
		t.Error("fully opaque image reported as transparent")
	}
	opaque.Pix[3] = 0xfe
	if !hasTransparency(opaque) {
		t.Error("image with a non-opaque pixel reported as opaque")
	}
}

// ---------------------------------------------------------------------------
// Fit modes
// ---------------------------------------------------------------------------

func decodeSize(t *testing.T, b []byte) (int, int) {
	t.Helper()
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("decode result: %v", err)
	}
	return cfg.Width, cfg.Height
}

func TestFitLimitPreservesRatioAndNeverUpscales(t *testing.T) {
	src := makeJPEG(t, 1200, 800)
	out, _, err := Transform(src, "image/jpeg", TransformParams{
		Width: 256, Fit: FitLimit, Format: FormatJPEG, Quality: 75,
	}, "")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if w, h := decodeSize(t, out); w != 256 || h != 170 {
		t.Errorf("got %dx%d, want 256x170 (3:2 preserved)", w, h)
	}

	// A source smaller than the box stays at its own size.
	small := makeJPEG(t, 64, 64)
	out, _, err = Transform(small, "image/jpeg", TransformParams{
		Width: 512, Fit: FitLimit, Format: FormatJPEG, Quality: 75,
	}, "")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if w, h := decodeSize(t, out); w != 64 || h != 64 {
		t.Errorf("got %dx%d, want 64x64 — limit must not upscale", w, h)
	}
}

func TestFitFitUpscalesWhereLimitWillNot(t *testing.T) {
	src := makeJPEG(t, 64, 64)
	out, _, err := Transform(src, "image/jpeg", TransformParams{
		Width: 256, Fit: FitFit, Format: FormatJPEG, Quality: 75,
	}, "")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if w, h := decodeSize(t, out); w != 256 || h != 256 {
		t.Errorf("got %dx%d, want 256x256 — fit may upscale", w, h)
	}
}

func TestFitFillCoversBoxExactly(t *testing.T) {
	src := makeJPEG(t, 1200, 800)
	out, _, err := Transform(src, "image/jpeg", TransformParams{
		Width: 256, Height: 256, Fit: FitFill, Gravity: GravityCenter,
		Format: FormatJPEG, Quality: 75,
	}, "")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if w, h := decodeSize(t, out); w != 256 || h != 256 {
		t.Errorf("got %dx%d, want exactly 256x256", w, h)
	}
}

func TestFitPadProducesExactBox(t *testing.T) {
	src := makeJPEG(t, 1200, 800)
	out, _, err := Transform(src, "image/png", TransformParams{
		Width: 256, Height: 256, Fit: FitPad, Gravity: GravityCenter,
		Format: FormatPNG, Quality: 75,
	}, "")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if w, h := decodeSize(t, out); w != 256 || h != 256 {
		t.Errorf("got %dx%d, want exactly 256x256", w, h)
	}
}

func TestFitScaleIgnoresAspectRatio(t *testing.T) {
	src := makeJPEG(t, 1200, 800)
	out, _, err := Transform(src, "image/jpeg", TransformParams{
		Width: 256, Height: 64, Fit: FitScale, Format: FormatJPEG, Quality: 75,
	}, "")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if w, h := decodeSize(t, out); w != 256 || h != 64 {
		t.Errorf("got %dx%d, want exactly 256x64", w, h)
	}
}

func TestGravityOffsetAnchors(t *testing.T) {
	if got := gravityOffset(100, GravityWest, true); got != 0 {
		t.Errorf("west = %d, want 0", got)
	}
	if got := gravityOffset(100, GravityEast, true); got != 100 {
		t.Errorf("east = %d, want 100", got)
	}
	if got := gravityOffset(100, GravityCenter, true); got != 50 {
		t.Errorf("center = %d, want 50", got)
	}
	if got := gravityOffset(100, GravityNorth, false); got != 0 {
		t.Errorf("north = %d, want 0", got)
	}
	if got := gravityOffset(100, GravitySouth, false); got != 100 {
		t.Errorf("south = %d, want 100", got)
	}
}

// ---------------------------------------------------------------------------
// Parsing and negotiation
// ---------------------------------------------------------------------------

func TestParseTransformQueryDefaults(t *testing.T) {
	p := ParseTransformQuery("", "", "", "", "", "")
	if p.Width != DefaultImageWidth {
		t.Errorf("width = %d, want %d", p.Width, DefaultImageWidth)
	}
	if p.Fit != FitLimit {
		t.Errorf("fit = %q, want limit", p.Fit)
	}
	if p.Gravity != GravityCenter {
		t.Errorf("gravity = %q, want center", p.Gravity)
	}
	if p.Format != FormatAuto {
		t.Errorf("format = %q, want auto", p.Format)
	}
	if p.Quality != DefaultImageQuality {
		t.Errorf("quality = %d, want %d", p.Quality, DefaultImageQuality)
	}
}

// Box modes need both dimensions; with one missing they cannot mean anything,
// so they degrade rather than silently producing a surprising crop.
func TestParseTransformQueryDegradesBoxModesWithoutBothDimensions(t *testing.T) {
	for _, fit := range []string{"fill", "pad", "scale"} {
		p := ParseTransformQuery("400", "", fit, "", "", "")
		if p.Fit != FitLimit {
			t.Errorf("fit=%s with no height = %q, want limit", fit, p.Fit)
		}
	}
	p := ParseTransformQuery("400", "400", "fill", "", "", "")
	if p.Fit != FitFill {
		t.Errorf("fill with both dimensions = %q, want fill", p.Fit)
	}
}

func TestParseTransformQuerySnapsDimensions(t *testing.T) {
	p := ParseTransformQuery("400", "300", "fill", "north", "webp", "73")
	if p.Width != 512 {
		t.Errorf("width = %d, want 512 (snapped up)", p.Width)
	}
	if p.Height != 384 {
		t.Errorf("height = %d, want 384 (snapped up)", p.Height)
	}
	if p.Quality != 75 {
		t.Errorf("quality = %d, want 75 (snapped)", p.Quality)
	}
	if p.Gravity != GravityNorth {
		t.Errorf("gravity = %q, want north", p.Gravity)
	}
	if p.Format != FormatWebP {
		t.Errorf("format = %q, want webp", p.Format)
	}
}

func TestNegotiateFormat(t *testing.T) {
	cases := []struct {
		requested OutputFormat
		accept    string
		hasAlpha  bool
		want      OutputFormat
	}{
		{FormatAuto, "image/avif,image/webp,*/*", false, FormatAVIF},
		{FormatAuto, "image/webp,*/*", false, FormatWebP},
		{FormatAuto, "*/*", false, FormatJPEG},
		{FormatAuto, "*/*", true, FormatPNG},
		{FormatAuto, "", true, FormatPNG},
		// An explicit request always wins over negotiation.
		{FormatJPEG, "image/avif", false, FormatJPEG},
		{FormatPNG, "image/avif", false, FormatPNG},
	}
	for _, tc := range cases {
		got := NegotiateFormat(tc.requested, tc.accept, tc.hasAlpha)
		if got != tc.want {
			t.Errorf("NegotiateFormat(%q, %q, %v) = %q, want %q",
				tc.requested, tc.accept, tc.hasAlpha, got, tc.want)
		}
	}
}

func TestTransformNegotiatesFromAccept(t *testing.T) {
	src := makeJPEG(t, 400, 400)
	for _, tc := range []struct {
		accept string
		want   OutputFormat
	}{
		{"image/avif,image/webp,*/*", FormatAVIF},
		{"image/webp,*/*", FormatWebP},
		{"*/*", FormatJPEG},
	} {
		_, got, err := Transform(src, "image/jpeg", TransformParams{
			Width: 128, Fit: FitLimit, Format: FormatAuto, Quality: 75,
		}, tc.accept)
		if err != nil {
			t.Fatalf("Transform(%q): %v", tc.accept, err)
		}
		if got != tc.want {
			t.Errorf("accept %q produced %q, want %q", tc.accept, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Gating and cache keys
// ---------------------------------------------------------------------------

func TestTransformRejectsNonRaster(t *testing.T) {
	if _, _, err := Transform([]byte("<svg/>"), "image/svg+xml", TransformParams{Width: 256}, ""); err == nil {
		t.Fatal("expected an error for svg")
	}
	if _, _, err := Transform([]byte("nope"), "text/html", TransformParams{Width: 256}, ""); err == nil {
		t.Fatal("expected an error for html")
	}
	if _, _, err := Transform([]byte("not an image"), "image/png", TransformParams{Width: 256}, ""); err == nil {
		t.Fatal("expected a decode error")
	}
}

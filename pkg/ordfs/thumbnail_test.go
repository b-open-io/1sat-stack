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

func TestSnapThumbWidth(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, DefaultThumbWidth},
		{-5, DefaultThumbWidth},
		{1, 16},
		{16, 16},
		{17, 32},
		{400, 512},
		{1920, 1920},
		{4000, 1920},
	}
	for _, tc := range cases {
		if got := SnapThumbWidth(tc.in); got != tc.want {
			t.Errorf("SnapThumbWidth(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestSnapThumbQuality(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, DefaultThumbQuality},
		{-1, DefaultThumbQuality},
		{1, 5},
		{73, 75},
		{77, 75},
		{78, 80},
		{100, 100},
		{5000, 100},
	}
	for _, tc := range cases {
		if got := SnapThumbQuality(tc.in); got != tc.want {
			t.Errorf("SnapThumbQuality(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestIsThumbnailable(t *testing.T) {
	yes := []string{"image/png", "image/jpeg", "IMAGE/JPEG", "image/gif", "image/webp", "image/png; charset=binary"}
	for _, ct := range yes {
		if !IsThumbnailable(ct) {
			t.Errorf("IsThumbnailable(%q) = false, want true", ct)
		}
	}
	// SVG scales losslessly and is already small; rasterizing it would regress.
	no := []string{"image/svg+xml", "text/html", "application/json", "ord-fs/json", "video/mp4", ""}
	for _, ct := range no {
		if IsThumbnailable(ct) {
			t.Errorf("IsThumbnailable(%q) = true, want false", ct)
		}
	}
}

func TestRenderThumbnailScalesDown(t *testing.T) {
	src := makeJPEG(t, 1200, 800)
	out, format, err := renderThumbnail(src, "image/jpeg", 256, 75)
	if err != nil {
		t.Fatalf("renderThumbnail: %v", err)
	}
	if format != thumbJPEG {
		t.Errorf("format = %v, want jpeg", format)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if cfg.Width != 256 {
		t.Errorf("width = %d, want 256", cfg.Width)
	}
	// 1200x800 -> 256 wide keeps the 3:2 ratio
	if cfg.Height != 170 {
		t.Errorf("height = %d, want 170", cfg.Height)
	}
	if len(out) >= len(src) {
		t.Errorf("thumbnail (%d bytes) is not smaller than source (%d bytes)", len(out), len(src))
	}
}

func TestRenderThumbnailDoesNotUpscale(t *testing.T) {
	src := makePNG(t, 64, 64)
	out, _, err := renderThumbnail(src, "image/png", 512, 75)
	if err != nil {
		t.Fatalf("renderThumbnail: %v", err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if cfg.Width != 64 || cfg.Height != 64 {
		t.Errorf("got %dx%d, want 64x64 — smaller sources must not be upscaled", cfg.Width, cfg.Height)
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

func TestRenderThumbnailKeepsPNGWhenTransparent(t *testing.T) {
	src := makeTransparentPNG(t, 400, 400)
	_, format, err := renderThumbnail(src, "image/png", 128, 75)
	if err != nil {
		t.Fatalf("renderThumbnail: %v", err)
	}
	if format != thumbPNG {
		t.Errorf("format = %v, want png so transparency is not flattened", format)
	}
}

// Most PNG inscriptions are fully opaque, where JPEG is several times smaller.
func TestRenderThumbnailUsesJPEGForOpaquePNG(t *testing.T) {
	src := makePNG(t, 400, 400)
	out, format, err := renderThumbnail(src, "image/png", 128, 75)
	if err != nil {
		t.Fatalf("renderThumbnail: %v", err)
	}
	if format != thumbJPEG {
		t.Errorf("format = %v, want jpeg for a fully opaque source", format)
	}
	pngOut, _, err := renderThumbnail(makeTransparentPNG(t, 400, 400), "image/png", 128, 75)
	if err != nil {
		t.Fatalf("renderThumbnail: %v", err)
	}
	if len(out) >= len(pngOut) {
		t.Logf("jpeg=%d png=%d", len(out), len(pngOut))
	}
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

func TestRenderThumbnailRejectsNonRaster(t *testing.T) {
	if _, _, err := renderThumbnail([]byte("<svg/>"), "image/svg+xml", 256, 75); err == nil {
		t.Fatal("expected an error for svg")
	}
	if _, _, err := renderThumbnail([]byte("nope"), "text/html", 256, 75); err == nil {
		t.Fatal("expected an error for html")
	}
}

func TestRenderThumbnailRejectsUndecodable(t *testing.T) {
	if _, _, err := renderThumbnail([]byte("not an image at all"), "image/png", 256, 75); err == nil {
		t.Fatal("expected a decode error")
	}
}

func TestThumbCacheEntryRoundTrip(t *testing.T) {
	payload := []byte{1, 2, 3, 4, 5}
	for _, format := range []thumbFormat{thumbJPEG, thumbPNG} {
		gotFormat, gotPayload, ok := decodeThumbEntry(encodeThumbEntry(format, payload))
		if !ok {
			t.Fatalf("decodeThumbEntry(%v) not ok", format)
		}
		if gotFormat != format {
			t.Errorf("format = %v, want %v", gotFormat, format)
		}
		if !bytes.Equal(gotPayload, payload) {
			t.Errorf("payload = %v, want %v", gotPayload, payload)
		}
	}
}

func TestDecodeThumbEntryRejectsGarbage(t *testing.T) {
	for _, entry := range [][]byte{nil, {}, {1}, {9, 1, 2, 3}} {
		if _, _, ok := decodeThumbEntry(entry); ok {
			t.Errorf("decodeThumbEntry(%v) = ok, want not ok", entry)
		}
	}
}

func TestThumbCacheKeyVariesByParams(t *testing.T) {
	base := thumbCacheKey("abc_0", 256, 75)
	if base == thumbCacheKey("abc_0", 384, 75) {
		t.Error("key must vary by width")
	}
	if base == thumbCacheKey("abc_0", 256, 90) {
		t.Error("key must vary by quality")
	}
	if base == thumbCacheKey("def_0", 256, 75) {
		t.Error("key must vary by outpoint")
	}
}

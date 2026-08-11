package ordfs

import (
	"bytes"
	"image/png"
	"testing"
)

// 2:1 aspect, 200x100 intrinsic.
var testSVG = []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="200" height="100" viewBox="0 0 200 100"><rect width="200" height="100" fill="red"/></svg>`)

func TestRasterizeSVGKeepsIntrinsicSizeWhenBoxIsSmaller(t *testing.T) {
	img, err := RasterizeSVG(testSVG, 100, 0)
	if err != nil {
		t.Fatalf("RasterizeSVG: %v", err)
	}
	if w, h := img.Bounds().Dx(), img.Bounds().Dy(); w != 200 || h != 100 {
		t.Errorf("got %dx%d, want intrinsic 200x100 — downscaling is the pipeline's job", w, h)
	}
}

func TestRasterizeSVGScalesUpPreservingAspect(t *testing.T) {
	img, err := RasterizeSVG(testSVG, 400, 400)
	if err != nil {
		t.Fatalf("RasterizeSVG: %v", err)
	}
	// Covering a 400x400 box from 200x100 needs a 4x scale on height.
	if w, h := img.Bounds().Dx(), img.Bounds().Dy(); w != 800 || h != 400 {
		t.Errorf("got %dx%d, want 800x400 (2:1 preserved, box covered)", w, h)
	}
}

func TestTransformSVGProducesRequestedRasterFormat(t *testing.T) {
	p := TransformParams{Width: 384, Height: 384, Fit: FitFill, Format: FormatPNG, Quality: 75}
	out, format, err := TransformSVG(testSVG, p, "")
	if err != nil {
		t.Fatalf("TransformSVG: %v", err)
	}
	if format != FormatPNG {
		t.Errorf("format = %q, want png", format)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("output is not decodable png: %v", err)
	}
	if w, h := img.Bounds().Dx(), img.Bounds().Dy(); w != 384 || h != 384 {
		t.Errorf("got %dx%d, want exact 384x384 for fill", w, h)
	}
}

func TestTransformSVGRejectsInvalidSVG(t *testing.T) {
	if _, _, err := TransformSVG([]byte("not svg at all"), TransformParams{
		Width: 64, Format: FormatPNG, Quality: 75,
	}, ""); err == nil {
		t.Fatal("expected error for invalid svg")
	}
}

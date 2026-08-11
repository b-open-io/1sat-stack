package ordfs

import (
	"bytes"
	"image/png"
	"os"
	"testing"
)

// Opt-in check against a real on-chain SVG inscription, since crafted test
// fixtures miss real-world SVG shapes (viewBox without width/height, etc.).
// Run: REAL_SVG=/path/to/inscription.svg go test ./pkg/ordfs -run TestRealSVGInscription -v
func TestRealSVGInscription(t *testing.T) {
	path := os.Getenv("REAL_SVG")
	if path == "" {
		t.Skip("REAL_SVG not set")
	}
	svg, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out, format, err := TransformSVG(svg, TransformParams{
		Width: 1200, Height: 640, Fit: FitFill, Format: FormatPNG, Quality: 75,
	}, "")
	if err != nil {
		t.Fatalf("TransformSVG: %v", err)
	}
	if format != FormatPNG {
		t.Fatalf("format = %q", format)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("not decodable png: %v", err)
	}
	t.Logf("rendered %dx%d png, %d bytes", img.Bounds().Dx(), img.Bounds().Dy(), len(out))
}

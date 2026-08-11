package ordfs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"math"
	"sync"

	resvg "github.com/kanrichan/resvg-go"
)

// svgWorker serialises access to the wazero-backed resvg instance. The wasm
// module holds mutable state, so renders cannot run concurrently on one
// renderer, and compiling the module is expensive enough to do only once.
type svgWorker struct {
	mu sync.Mutex
	// ctx is retained so the wasm runtime backing renderer stays alive.
	ctx      *resvg.Context
	renderer *resvg.Renderer
}

var (
	svgOnce    sync.Once
	svgShared  *svgWorker
	svgInitErr error
)

func sharedSVGWorker() (*svgWorker, error) {
	svgOnce.Do(func() {
		rctx, err := resvg.NewContext(context.Background())
		if err != nil {
			svgInitErr = fmt.Errorf("init resvg context: %w", err)
			return
		}
		renderer, err := rctx.NewRenderer()
		if err != nil {
			svgInitErr = fmt.Errorf("init resvg renderer: %w", err)
			return
		}
		// Without fonts, <text> elements render empty.
		_ = renderer.LoadSystemFonts()
		svgShared = &svgWorker{ctx: rctx, renderer: renderer}
	})
	return svgShared, svgInitErr
}

// RasterizeSVG renders svg to a raster at least as large as the requested box,
// so the transform pipeline never upscales a bitmap rendering of a vector. A
// zero dimension leaves that axis bound only by the SVG's own aspect ratio.
// resvg's RenderWithSize stretches to the exact size it is given, so aspect is
// preserved here by scaling the intrinsic dimensions with a single factor.
func RasterizeSVG(svg []byte, boxW, boxH int) (image.Image, error) {
	w, err := sharedSVGWorker()
	if err != nil {
		return nil, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	rendered, err := w.renderer.Render(svg)
	if err != nil {
		return nil, fmt.Errorf("render svg: %w", err)
	}
	img, err := png.Decode(bytes.NewReader(rendered))
	if err != nil {
		return nil, fmt.Errorf("decode rendered svg: %w", err)
	}

	iw, ih := img.Bounds().Dx(), img.Bounds().Dy()
	if iw == 0 || ih == 0 {
		return nil, errors.New("svg has no dimensions")
	}

	scale := 0.0
	if boxW > 0 {
		scale = math.Max(scale, float64(boxW)/float64(iw))
	}
	if boxH > 0 {
		scale = math.Max(scale, float64(boxH)/float64(ih))
	}
	if scale <= 1 {
		return img, nil
	}

	rw := uint32(math.Round(float64(iw) * scale))
	rh := uint32(math.Round(float64(ih) * scale))
	rendered, err = w.renderer.RenderWithSize(svg, rw, rh)
	if err != nil {
		return nil, fmt.Errorf("render svg at %dx%d: %w", rw, rh, err)
	}
	img, err = png.Decode(bytes.NewReader(rendered))
	if err != nil {
		return nil, fmt.Errorf("decode rendered svg: %w", err)
	}
	return img, nil
}

// TransformSVG rasterizes an SVG and applies p. Callers route here only when
// an explicit raster format was requested; FormatAuto keeps SVG passthrough.
func TransformSVG(svg []byte, p TransformParams, accept string) ([]byte, OutputFormat, error) {
	srcImg, err := RasterizeSVG(svg, p.Width, p.Height)
	if err != nil {
		return nil, "", err
	}
	return transformImage(srcImg, p, accept)
}

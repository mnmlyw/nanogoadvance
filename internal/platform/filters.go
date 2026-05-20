package platform

import (
	"fmt"
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
)

// VideoFilters configures the frontend's post-PPU filter pipeline.
// Mirrors NanoBoyAdvance's three orthogonal video options.
type VideoFilters struct {
	Color       ColorFilter
	LCDGhosting bool
	Spatial     SpatialFilter
}

// ColorFilter selects the color-correction shader (per-pixel transform).
type ColorFilter int

const (
	ColorNone ColorFilter = iota
	ColorAGB              // Pokefan531 color mangler — most "GBA-ish"
	ColorHigan            // higan emulator's GBA profile
)

// SpatialFilter selects how the (possibly color-corrected, possibly
// ghosted) 240x160 buffer is presented at the final window size.
type SpatialFilter int

const (
	SpatialNearest SpatialFilter = iota // ebiten FilterNearest
	SpatialLinear                       // ebiten FilterLinear
	SpatialSharp                        // sharp_bilinear shader
	SpatialLCD1x                        // lcd1x grid shader
	SpatialXBRZ                         // xBRZ 4x then linear-fit
)

// ParseColorFilter / ParseSpatialFilter — CLI flag parsers.
func ParseColorFilter(s string) (ColorFilter, error) {
	switch s {
	case "", "none":
		return ColorNone, nil
	case "agb", "gba":
		return ColorAGB, nil
	case "higan":
		return ColorHigan, nil
	}
	return ColorNone, fmt.Errorf("unknown color filter %q (want none|agb|higan)", s)
}

func ParseSpatialFilter(s string) (SpatialFilter, error) {
	switch s {
	case "", "nearest":
		return SpatialNearest, nil
	case "linear":
		return SpatialLinear, nil
	case "sharp":
		return SpatialSharp, nil
	case "lcd1x":
		return SpatialLCD1x, nil
	case "xbrz":
		return SpatialNearest, fmt.Errorf("xbrz not yet implemented in Kage; use sharp or linear")
	}
	return SpatialNearest, fmt.Errorf("unknown spatial filter %q (want nearest|linear|sharp|lcd1x)", s)
}

// filterPipeline holds the compiled shaders + intermediate render
// targets. Lazily allocates intermediates only for the active
// configuration.
type filterPipeline struct {
	cfg VideoFilters

	// Compiled Kage shaders. nil for disabled filters.
	colorShader *ebiten.Shader // color_agb / color_higan
	ghostShader *ebiten.Shader
	lcd1xShader *ebiten.Shader

	// Native-resolution intermediates (240x160).
	colorOut *ebiten.Image // color → input for next stage
	ghostOut *ebiten.Image // ghosting → input for spatial stage
	history  *ebiten.Image // previous frame, used by ghosting

	// scaledOut is allocated lazily to dst's pixel size for the spatial
	// shader pass — Ebiten's DrawRectShader requires bound images to
	// match the destination rectangle exactly.
	scaledOut *ebiten.Image
}

func newFilterPipeline(cfg VideoFilters) (*filterPipeline, error) {
	p := &filterPipeline{cfg: cfg}

	compile := func(src string) (*ebiten.Shader, error) {
		sh, err := ebiten.NewShader([]byte(src))
		return sh, err
	}

	var err error
	switch cfg.Color {
	case ColorAGB:
		if p.colorShader, err = compile(shaderColorAGB); err != nil {
			return nil, fmt.Errorf("color_agb shader: %w", err)
		}
	case ColorHigan:
		if p.colorShader, err = compile(shaderColorHigan); err != nil {
			return nil, fmt.Errorf("color_higan shader: %w", err)
		}
	}
	if cfg.LCDGhosting {
		if p.ghostShader, err = compile(shaderLCDGhosting); err != nil {
			return nil, fmt.Errorf("lcd_ghosting shader: %w", err)
		}
	}
	switch cfg.Spatial {
	case SpatialLCD1x:
		if p.lcd1xShader, err = compile(shaderLCD1x); err != nil {
			return nil, fmt.Errorf("lcd1x shader: %w", err)
		}
	case SpatialXBRZ:
		// Should be unreachable — ParseSpatialFilter rejects "xbrz".
		return nil, fmt.Errorf("xbrz spatial filter not yet implemented")
	}

	// Allocate intermediate images only where needed.
	if p.colorShader != nil {
		p.colorOut = ebiten.NewImage(width, height)
	}
	if p.ghostShader != nil {
		p.ghostOut = ebiten.NewImage(width, height)
		p.history = ebiten.NewImage(width, height)
		p.history.Fill(color.Black)
	}
	return p, nil
}

// apply runs the pipeline and draws the final result into dst.
func (p *filterPipeline) apply(srcTex *ebiten.Image, dst *ebiten.Image) {
	// Stage 1: color correction.
	src := srcTex
	if p.colorShader != nil {
		p.colorOut.Clear()
		op := &ebiten.DrawRectShaderOptions{}
		op.Images[0] = src
		p.colorOut.DrawRectShader(width, height, p.colorShader, op)
		src = p.colorOut
	}

	// Stage 2: LCD ghosting (interframe blend).
	if p.ghostShader != nil {
		p.ghostOut.Clear()
		op := &ebiten.DrawRectShaderOptions{}
		op.Images[0] = src
		op.Images[1] = p.history
		p.ghostOut.DrawRectShader(width, height, p.ghostShader, op)
		// History = the BLENDED output, so the next frame ghosts against
		// the already-ghosted frame. Matches NBA's `history = ghostOut`.
		p.history.Clear()
		p.history.DrawImage(p.ghostOut, nil)
		src = p.ghostOut
	}

	// Stage 3: spatial / scaling. Ebiten's DrawRectShader requires
	// bound images to match the destination size, so for shader-driven
	// filters we first pre-scale the source onto a screen-sized
	// intermediate, then run the shader same-to-same.
	bounds := dst.Bounds()
	outW, outH := bounds.Dx(), bounds.Dy()
	switch p.cfg.Spatial {
	case SpatialNearest:
		op := &ebiten.DrawImageOptions{}
		op.GeoM.Scale(float64(outW)/float64(width), float64(outH)/float64(height))
		dst.DrawImage(src, op)

	case SpatialLinear:
		op := &ebiten.DrawImageOptions{}
		op.GeoM.Scale(float64(outW)/float64(width), float64(outH)/float64(height))
		op.Filter = ebiten.FilterLinear
		dst.DrawImage(src, op)

	case SpatialSharp:
		// Sharp bilinear ⇄ sharp_bilinear.glsl. The shader is unnecessary
		// in Ebiten: nearest-scaled to the largest integer multiple of
		// 240x160 that fits the window, then linear-filtered to fit.
		// Same visual result without a shader pass.
		intScale := minInt(outW/width, outH/height)
		if intScale < 1 {
			intScale = 1
		}
		pre := p.scaledIntermediate(width*intScale, height*intScale)
		pre.Clear()
		op := &ebiten.DrawImageOptions{}
		op.GeoM.Scale(float64(intScale), float64(intScale))
		pre.DrawImage(src, op)
		op2 := &ebiten.DrawImageOptions{}
		op2.GeoM.Scale(float64(outW)/float64(width*intScale), float64(outH)/float64(height*intScale))
		op2.Filter = ebiten.FilterLinear
		dst.DrawImage(pre, op2)

	case SpatialLCD1x:
		// Pre-scale (nearest) onto a screen-sized intermediate, then run
		// lcd1x shader same-to-same to apply the grid pattern.
		pre := p.scaledIntermediate(outW, outH)
		pre.Clear()
		op := &ebiten.DrawImageOptions{}
		op.GeoM.Scale(float64(outW)/float64(width), float64(outH)/float64(height))
		pre.DrawImage(src, op)
		op2 := &ebiten.DrawRectShaderOptions{}
		op2.Images[0] = pre
		op2.Uniforms = map[string]any{"OutputSize": []float32{float32(outW), float32(outH)}}
		dst.DrawRectShader(outW, outH, p.lcd1xShader, op2)
	}
}

// scaledIntermediate returns an ebiten.Image of (w, h) pixels for use
// as a same-size intermediate before a shader pass. Lazily allocated
// and reallocated on size change.
func (p *filterPipeline) scaledIntermediate(w, h int) *ebiten.Image {
	if p.scaledOut == nil || p.scaledOut.Bounds().Dx() != w || p.scaledOut.Bounds().Dy() != h {
		p.scaledOut = ebiten.NewImage(w, h)
	}
	return p.scaledOut
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

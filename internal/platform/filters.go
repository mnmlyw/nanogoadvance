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
		return SpatialXBRZ, nil
	}
	return SpatialNearest, fmt.Errorf("unknown spatial filter %q (want nearest|linear|sharp|lcd1x|xbrz)", s)
}

// xBRZScale must stay in lockstep with the `scale` const in shaderXBRZ1.
const xBRZScale = 4

// filterPipeline holds the compiled shaders + intermediate render
// targets. Lazily allocates intermediates only for the active
// configuration.
type filterPipeline struct {
	cfg VideoFilters

	// Compiled Kage shaders. nil for disabled filters.
	colorShader *ebiten.Shader // color_agb / color_higan
	ghostShader *ebiten.Shader
	lcd1xShader *ebiten.Shader
	xbrz0Shader *ebiten.Shader // edge analysis → info map
	xbrz1Shader *ebiten.Shader // info map → 4x upscale

	// Native-resolution intermediates (240x160).
	colorOut *ebiten.Image // color → input for next stage
	ghostOut *ebiten.Image // ghosting → input for spatial stage
	history  *ebiten.Image // previous frame, used by ghosting

	// xBRZ intermediates.
	xbrzInfo  *ebiten.Image // 240x160, pass 0 output (info map)
	xbrzOut   *ebiten.Image // 4x scaled info map (input to pass 1)
	xbrzFinal *ebiten.Image // 4x final, pass 1 output

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
		if p.xbrz0Shader, err = compile(shaderXBRZ0); err != nil {
			return nil, fmt.Errorf("xbrz0 shader: %w", err)
		}
		if p.xbrz1Shader, err = compile(shaderXBRZ1); err != nil {
			return nil, fmt.Errorf("xbrz1 shader: %w", err)
		}
		p.xbrzInfo = ebiten.NewImage(width, height)
		p.xbrzOut = ebiten.NewImage(width*xBRZScale, height*xBRZScale)
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

	case SpatialXBRZ:
		// Pass 0: at native 240x160, generate the info map.
		p.xbrzInfo.Clear()
		op0 := &ebiten.DrawRectShaderOptions{}
		op0.Images[0] = src
		p.xbrzInfo.DrawRectShader(width, height, p.xbrz0Shader, op0)
		// Pass 1: at 4x output (960x640). Bind both the original source
		// AND the info map. Ebiten requires bound images at the same
		// size as each other and matching the dst rect — the original
		// source is 240x160 but xbrzInfo is also 240x160 (good). To
		// satisfy the dst-rect constraint we'd need src to be 4x too,
		// so we pre-scale src (nearest) onto a 4x intermediate.
		srcScaled := p.scaledIntermediate(width*xBRZScale, height*xBRZScale)
		srcScaled.Clear()
		opSrc := &ebiten.DrawImageOptions{}
		opSrc.GeoM.Scale(float64(xBRZScale), float64(xBRZScale))
		srcScaled.DrawImage(src, opSrc)
		// Info map also needs to be 4x to satisfy same-size rule. Scale
		// nearest so each native info-pixel becomes a 4x4 block — pass
		// 1 then samples one info-tap per output pixel, all 4x4 of which
		// share the same info as upstream's pass-1 lookup intent.
		infoScaled := p.xbrzOut // reuse — overwritten below by xbrz1
		infoScaled.Clear()
		opInfo := &ebiten.DrawImageOptions{}
		opInfo.GeoM.Scale(float64(xBRZScale), float64(xBRZScale))
		infoScaled.DrawImage(p.xbrzInfo, opInfo)
		// Allocate a separate target since xbrzOut is now infoScaled.
		// Lazy-allocate to avoid the lifecycle bug.
		finalOut := p.xbrzFinalIntermediate()
		finalOut.Clear()
		op1 := &ebiten.DrawRectShaderOptions{}
		op1.Images[0] = srcScaled
		op1.Images[1] = infoScaled
		op1.Uniforms = map[string]any{"OutputSize": []float32{float32(width * xBRZScale), float32(height * xBRZScale)}}
		finalOut.DrawRectShader(width*xBRZScale, height*xBRZScale, p.xbrz1Shader, op1)
		// Fit to window via linear filter.
		opFit := &ebiten.DrawImageOptions{}
		opFit.GeoM.Scale(float64(outW)/float64(width*xBRZScale), float64(outH)/float64(height*xBRZScale))
		opFit.Filter = ebiten.FilterLinear
		dst.DrawImage(finalOut, opFit)
	}
}

// xbrzFinalIntermediate — second 4x image (xbrzOut is consumed as a
// scaled info map during pass 1; the final output target is allocated
// separately to keep the pipeline obvious).
func (p *filterPipeline) xbrzFinalIntermediate() *ebiten.Image {
	if p.xbrzFinal == nil {
		p.xbrzFinal = ebiten.NewImage(width*xBRZScale, height*xBRZScale)
	}
	return p.xbrzFinal
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

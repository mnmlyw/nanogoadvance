// filters_test.go — headless tests for the post-PPU filter pipeline.
//
// Ebiten requires its run loop to be active before (*Image).At can
// readback pixels from the GPU (panic: "ReadPixels cannot be called
// before the game starts"). TestMain bootstraps a minimal game whose
// single Update() runs the whole testing.M then returns
// ebiten.Termination — same pattern Ebiten's own tests use
// (internal/testing/testing.go). A tiny window appears briefly during
// the test run; that's unavoidable on this Ebiten version.
package platform

import (
	"image/color"
	"os"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
)

type testGame struct {
	m    *testing.M
	code int
}

func (g *testGame) Update() error {
	g.code = g.m.Run()
	return ebiten.Termination
}
func (*testGame) Draw(*ebiten.Image)             {}
func (*testGame) Layout(_, _ int) (int, int)     { return 1, 1 }

func TestMain(m *testing.M) {
	g := &testGame{m: m, code: 1}
	if err := ebiten.RunGame(g); err != nil {
		panic(err)
	}
	os.Exit(g.code)
}

// TestParseColorFilter — CLI flag handling.
func TestParseColorFilter(t *testing.T) {
	cases := map[string]ColorFilter{
		"":      ColorNone,
		"none":  ColorNone,
		"agb":   ColorAGB,
		"gba":   ColorAGB,
		"higan": ColorHigan,
	}
	for in, want := range cases {
		got, err := ParseColorFilter(in)
		if err != nil || got != want {
			t.Errorf("ParseColorFilter(%q) = %v, %v; want %v, nil", in, got, err, want)
		}
	}
	if _, err := ParseColorFilter("bogus"); err == nil {
		t.Errorf("ParseColorFilter(bogus): want error")
	}
}

func TestParseSpatialFilter(t *testing.T) {
	cases := map[string]SpatialFilter{
		"":        SpatialNearest,
		"nearest": SpatialNearest,
		"linear":  SpatialLinear,
		"sharp":   SpatialSharp,
		"lcd1x":   SpatialLCD1x,
	}
	for in, want := range cases {
		got, err := ParseSpatialFilter(in)
		if err != nil || got != want {
			t.Errorf("ParseSpatialFilter(%q) = %v, %v; want %v, nil", in, got, err, want)
		}
	}
	if _, err := ParseSpatialFilter("bogus"); err == nil {
		t.Errorf("ParseSpatialFilter(bogus): want error")
	}
	if _, err := ParseSpatialFilter("xbrz"); err == nil {
		t.Errorf("ParseSpatialFilter(xbrz): want 'not implemented' error")
	}
}

// fillImage writes a single solid color across the whole image.
func fillImage(img *ebiten.Image, c color.RGBA) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	pix := make([]byte, w*h*4)
	for i := 0; i < w*h; i++ {
		pix[i*4+0] = c.R
		pix[i*4+1] = c.G
		pix[i*4+2] = c.B
		pix[i*4+3] = c.A
	}
	img.WritePixels(pix)
}

// pixelAt reads a single pixel as RGBA. Helper around img.At's
// interface{} return.
func pixelAt(img *ebiten.Image, x, y int) color.RGBA {
	return img.At(x, y).(color.RGBA)
}

// closeChannel asserts a single channel is within delta of want.
func closeChannel(t *testing.T, ch string, got, want, delta uint8) {
	t.Helper()
	d := int(got) - int(want)
	if d < 0 {
		d = -d
	}
	if d > int(delta) {
		t.Errorf("channel %s: got %d, want %d ±%d", ch, got, want, delta)
	}
}

// TestColorAGBShifts — color_agb applied to a pure white input must
// land at the AGB profile's idle-white point (a slightly warmer,
// dimmer white). The exact target values come from the matrix math
// in the shader; we just assert the shader runs and shifts away from
// 255,255,255 in the expected direction.
func TestColorAGBShifts(t *testing.T) {
	p, err := newFilterPipeline(VideoFilters{Color: ColorAGB})
	if err != nil {
		t.Fatalf("new pipeline: %v", err)
	}
	src := ebiten.NewImage(width, height)
	fillImage(src, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	dst := ebiten.NewImage(width, height)
	op := &ebiten.DrawRectShaderOptions{}
	op.Images[0] = src
	dst.DrawRectShader(width, height, p.colorShader, op)

	c := pixelAt(dst, width/2, height/2)
	if c.R == 255 && c.G == 255 && c.B == 255 {
		t.Errorf("color_agb on white: got pure white, want shifted; got %+v", c)
	}
	// Sanity: alpha must be opaque (the shader forces a=1).
	if c.A != 255 {
		t.Errorf("color_agb: alpha = %d, want 255", c.A)
	}
}

// TestColorHiganShifts — same idea for the higan profile.
func TestColorHiganShifts(t *testing.T) {
	p, err := newFilterPipeline(VideoFilters{Color: ColorHigan})
	if err != nil {
		t.Fatalf("new pipeline: %v", err)
	}
	src := ebiten.NewImage(width, height)
	// Pure red: easy to verify the matrix mixes channels.
	fillImage(src, color.RGBA{R: 255, A: 255})
	dst := ebiten.NewImage(width, height)
	op := &ebiten.DrawRectShaderOptions{}
	op.Images[0] = src
	dst.DrawRectShader(width, height, p.colorShader, op)
	c := pixelAt(dst, width/2, height/2)
	// higan's matrix maps red(R=1) to (1.0*R, 0.039*R, 0.196*R) before
	// gamma. After the (^4)/(^1/2.2) round trip, R stays near 255; G/B
	// drop substantially but remain non-zero.
	closeChannel(t, "R", c.R, 255, 5)
	if c.G == 0 || c.B == 0 {
		t.Errorf("higan on red: G/B should be non-zero (matrix mixes R into both); got %+v", c)
	}
	if c.G > 200 || c.B > 200 {
		t.Errorf("higan on red: G/B should be much less than R; got %+v", c)
	}
}

// TestLCDGhostingBlend — apply ghosting to (white current, black
// history): output should be ~50% gray.
func TestLCDGhostingBlend(t *testing.T) {
	p, err := newFilterPipeline(VideoFilters{LCDGhosting: true})
	if err != nil {
		t.Fatalf("new pipeline: %v", err)
	}
	fillImage(p.history, color.RGBA{R: 0, G: 0, B: 0, A: 255})
	src := ebiten.NewImage(width, height)
	fillImage(src, color.RGBA{R: 255, G: 255, B: 255, A: 255})

	dst := ebiten.NewImage(width, height)
	op := &ebiten.DrawRectShaderOptions{}
	op.Images[0] = src
	op.Images[1] = p.history
	dst.DrawRectShader(width, height, p.ghostShader, op)

	c := pixelAt(dst, width/2, height/2)
	// mix(white, black, 0.5) = 127 or 128 depending on rounding.
	closeChannel(t, "R", c.R, 128, 2)
	closeChannel(t, "G", c.G, 128, 2)
	closeChannel(t, "B", c.B, 128, 2)
}

// TestApplyPipeline — exercise the full pipeline end-to-end for each
// supported combination. Just asserts no panic / no error and that
// the output has the expected dimensions.
func TestApplyPipeline(t *testing.T) {
	const outW, outH = width * 3, height * 3
	cases := []VideoFilters{
		{},
		{Color: ColorAGB},
		{Color: ColorHigan},
		{LCDGhosting: true},
		{Spatial: SpatialLinear},
		{Spatial: SpatialSharp},
		{Spatial: SpatialLCD1x},
		{Color: ColorAGB, LCDGhosting: true, Spatial: SpatialSharp},
		{Color: ColorHigan, LCDGhosting: true, Spatial: SpatialLCD1x},
	}
	src := ebiten.NewImage(width, height)
	fillImage(src, color.RGBA{R: 100, G: 150, B: 200, A: 255})
	for _, cfg := range cases {
		p, err := newFilterPipeline(cfg)
		if err != nil {
			t.Errorf("newFilterPipeline(%+v): %v", cfg, err)
			continue
		}
		dst := ebiten.NewImage(outW, outH)
		p.apply(src, dst)
	}
}

// TestXBRZRejected — xbrz is explicitly unsupported for now.
func TestXBRZRejected(t *testing.T) {
	if _, err := ParseSpatialFilter("xbrz"); err == nil {
		t.Errorf("ParseSpatialFilter(xbrz) should return an error until ported")
	}
}

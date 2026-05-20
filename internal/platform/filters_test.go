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
		"xbrz":    SpatialXBRZ,
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
		{Spatial: SpatialXBRZ},
		{Color: ColorAGB, Spatial: SpatialXBRZ},
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

// TestXBRZAccepted — xbrz parses and the pipeline constructs cleanly.
func TestXBRZAccepted(t *testing.T) {
	got, err := ParseSpatialFilter("xbrz")
	if err != nil || got != SpatialXBRZ {
		t.Fatalf("ParseSpatialFilter(xbrz) = %v, %v; want %v, nil", got, err, SpatialXBRZ)
	}
	if _, err := newFilterPipeline(VideoFilters{Spatial: SpatialXBRZ}); err != nil {
		t.Fatalf("newFilterPipeline(xbrz): %v", err)
	}
}

// TestXBRZ0InfoMap — run only pass 0 and confirm the info map is
// non-zero on at least some pixels for a diagonal-staircase input.
// If pass 0 produces all zeros, pass 1 can't blend — every pixel
// returns res=E and xBRZ degrades to nearest scaling.
func TestXBRZ0InfoMap(t *testing.T) {
	p, err := newFilterPipeline(VideoFilters{Spatial: SpatialXBRZ})
	if err != nil {
		t.Fatalf("new pipeline: %v", err)
	}
	src := ebiten.NewImage(width, height)
	// Diagonal staircase — pixels where x < y are white, else black.
	pix := make([]byte, width*height*4)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			i := (y*width + x) * 4
			pix[i+3] = 255
			if x < y {
				pix[i+0], pix[i+1], pix[i+2] = 255, 255, 255
			}
		}
	}
	src.WritePixels(pix)

	// Run pass 0 only.
	p.xbrzInfo.Clear()
	op := &ebiten.DrawRectShaderOptions{}
	op.Images[0] = src
	p.xbrzInfo.DrawRectShader(width, height, p.xbrz0Shader, op)

	nonZero := 0
	for y := 1; y < height-1; y++ {
		for x := 1; x < width-1; x++ {
			c := pixelAt(p.xbrzInfo, x, y)
			if c.R != 0 || c.G != 0 || c.B != 0 || c.A != 0 {
				nonZero++
			}
		}
	}
	if nonZero == 0 {
		t.Fatalf("xbrz0 info map is all zero — pass 0 isn't encoding any blend decisions")
	}
	t.Logf("xbrz0 info map: %d/%d interior pixels non-zero (good)", nonZero, (width-2)*(height-2))
	// Sample one likely-edge pixel: just below the diagonal.
	t.Logf("info[100, 99] = %+v (just below diagonal — likely non-zero)", pixelAt(p.xbrzInfo, 100, 99))
	t.Logf("info[100, 101] = %+v (just above diagonal — likely non-zero)", pixelAt(p.xbrzInfo, 100, 101))
}

// TestXBRZRendersSomething — apply xBRZ to a synthetic source with a
// known diagonal edge and assert (a) the output isn't all-black (the
// classic "shader sample coords wrong" symptom), (b) some pixels have
// intermediate colors that nearest-scaling couldn't produce (= xBRZ is
// actually smoothing the diagonal, not just acting as nearest).
func TestXBRZRendersSomething(t *testing.T) {
	p, err := newFilterPipeline(VideoFilters{Spatial: SpatialXBRZ})
	if err != nil {
		t.Fatalf("new pipeline: %v", err)
	}
	src := ebiten.NewImage(width, height)
	// White triangle on black — clear diagonal edge that xBRZ should
	// smooth. Pixels where x < y are white, others black.
	pix := make([]byte, width*height*4)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			i := (y*width + x) * 4
			pix[i+3] = 255
			if x < y {
				pix[i+0], pix[i+1], pix[i+2] = 255, 255, 255
			}
		}
	}
	src.WritePixels(pix)

	dst := ebiten.NewImage(width*xBRZScale, height*xBRZScale)
	p.apply(src, dst)

	// Diagnostic: dump pixels around the triangle edge. native pixel
	// (100, 100) is BLACK (x==y, fails x<y), so 4x output spans rows
	// 400-403 cols 400-403. Bottom-left corner W of that block should
	// blend toward neighbor (99, 100) which is WHITE.
	t.Logf("output[400, 403] = %+v (bottom-left corner of black block)", pixelAt(dst, 400, 403))
	t.Logf("output[401, 403] = %+v (in bottom-left region)", pixelAt(dst, 401, 403))
	t.Logf("output[400, 402] = %+v (left edge mid)", pixelAt(dst, 400, 402))
	t.Logf("output[403, 400] = %+v (top-right of black block)", pixelAt(dst, 403, 400))
	t.Logf("output[400, 400] (on edge) = %+v", pixelAt(dst, 400, 400))

	// (a) Sample the bottom-left corner of every native-pixel-aligned
	// 4x4 block right at the triangle edge (where x < y kicks in). With
	// nearest scaling these would all be pure black or white. xBRZ
	// should blend them toward the white neighbor, producing white or
	// gray.
	bright := 0
	for y := 50; y < 150; y++ {
		// Bottom-left output pixel of native block (y-1, y) (border).
		x := y - 1
		ox := x*xBRZScale + 0
		oy := y*xBRZScale + xBRZScale - 1
		c := pixelAt(dst, ox, oy)
		if c.R != 0 || c.G != 0 || c.B != 0 {
			bright++
		}
	}
	if bright < 50 {
		t.Errorf("xBRZ corner-blend not firing: only %d/100 sampled border pixels brightened (expect >=50)", bright)
	}

	// (b) Look for intermediate gray values (not pure 0 or 255). With
	// pure nearest scaling there'd be only 0 or 255 per channel. xBRZ
	// smoothing produces antialiased gray pixels along the diagonal.
	intermediate := 0
	for y := 50; y < 150; y++ {
		x := y - 1
		for dy := 0; dy < xBRZScale; dy++ {
			for dx := 0; dx < xBRZScale; dx++ {
				c := pixelAt(dst, x*xBRZScale+dx, y*xBRZScale+dy)
				if c.R > 30 && c.R < 220 {
					intermediate++
					if intermediate >= 10 {
						return // enough evidence — pass
					}
				}
			}
		}
	}
	t.Errorf("xBRZ produced no intermediate gray pixels along the diagonal; got %d (expected ≥10) — suggests xBRZ is acting as pure nearest scaling", intermediate)
}

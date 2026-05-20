// Package platform is the host frontend — an ebitengine window that
// presents the PPU's framebuffer and pipes keyboard input into the core's
// keypad. One frame per tick.
package platform

import (
	"sync"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/audio"

	"github.com/mnmlyw/nanogoadvance/internal/core"
	"github.com/mnmlyw/nanogoadvance/internal/dsp"
	"github.com/mnmlyw/nanogoadvance/internal/keypad"
)

const (
	width          = 240
	height         = 160
	scale          = 3
	hostSampleRate = 44100
)

type Frontend struct {
	core *core.Core
	tex  *ebiten.Image
	pix  []byte
}

// hostBuffer holds resampled stereo samples at the host rate, ready to be
// pulled by ebiten's audio reader. ⇄ APU::buffer (StereoRingBuffer) in
// upstream APU.
type hostBuffer struct {
	mu   sync.Mutex
	data []dsp.StereoSample[float32]
	head int
	tail int
	cap  int
}

func newHostBuffer(capSamples int) *hostBuffer {
	return &hostBuffer{data: make([]dsp.StereoSample[float32], capSamples), cap: capSamples}
}

// Write satisfies dsp.WriteStream — called by the resampler.
func (h *hostBuffer) Write(s dsp.StereoSample[float32]) {
	h.mu.Lock()
	h.data[h.tail] = s
	h.tail = (h.tail + 1) % h.cap
	if h.tail == h.head {
		// Drop oldest on overflow.
		h.head = (h.head + 1) % h.cap
	}
	h.mu.Unlock()
}

func (h *hostBuffer) pop() (dsp.StereoSample[float32], bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.head == h.tail {
		return dsp.StereoSample[float32]{}, false
	}
	v := h.data[h.head]
	h.head = (h.head + 1) % h.cap
	return v, true
}

// apuResamplerAdapter wraps a dsp.Resampler so it satisfies the
// apu.StereoResampler interface (flat (l, r) float32 pair instead of
// dsp's StereoSample struct).
type apuResamplerAdapter struct {
	inner interface {
		Write(s dsp.StereoSample[float32])
		SetSampleRates(in, out float32)
	}
}

func (a apuResamplerAdapter) Write(l, r float32) {
	a.inner.Write(dsp.StereoSample[float32]{Left: l, Right: r})
}
func (a apuResamplerAdapter) SetSampleRates(in, out float32) {
	a.inner.SetSampleRates(in, out)
}

// hostBuffer satisfies both dsp.WriteStream (so the resampler writes to
// it) and apu.StereoSink (so the APU can drain it). Single bridge, no
// rate conversion at the consumer.
func (h *hostBuffer) Pop() (r, l float32, ok bool) {
	v, hit := h.pop()
	return v.Right, v.Left, hit
}

// Available returns the current number of unread samples. Used by the
// APU's underrun-fallback Peek loop.
func (h *hostBuffer) Available() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.tail >= h.head {
		return h.tail - h.head
	}
	return h.cap - h.head + h.tail
}

// Peek returns the sample at `offset` from the current head WITHOUT
// advancing the read pointer. Caller must ensure offset < Available().
func (h *hostBuffer) Peek(offset int) (r, l float32) {
	h.mu.Lock()
	defer h.mu.Unlock()
	v := h.data[(h.head+offset)%h.cap]
	return v.Right, v.Left
}

// audioReader is the io.Reader handed to ebiten audio.NewPlayer — drains
// the APU sink ring 1:1 (matches upstream's AudioCallback semantics).
type audioReader struct{ f *Frontend }

func (r *audioReader) Read(p []byte) (int, error) {
	return r.f.core.APU.ReadSamples(p), nil
}

func New(c *core.Core) *Frontend {
	// Allocate the host-rate ring (~1 second of stereo float at 44.1kHz).
	// Doubles as the apu.StereoSink and dsp.WriteStream target.
	hb := newHostBuffer(hostSampleRate)

	// Pick the resampler family that matches the active config. Default
	// (DefaultConfig) is Cubic, matching upstream.
	var inner interface {
		Write(s dsp.StereoSample[float32])
		SetSampleRates(in, out float32)
	}
	switch c.Config().Audio.Interpolation {
	case core.InterpolationCosine:
		inner = dsp.NewCosineResampler(hb)
	case core.InterpolationSinc64:
		inner = dsp.NewSincResampler(hb, 64)
	case core.InterpolationSinc128:
		inner = dsp.NewSincResampler(hb, 128)
	case core.InterpolationSinc256:
		inner = dsp.NewSincResampler(hb, 256)
	default: // InterpolationCubic
		inner = dsp.NewCubicResampler(hb)
	}
	resampler := apuResamplerAdapter{inner: inner}

	// Hand the resampler + sink to the APU; APU drives both from
	// StepMixer at its native rate.
	c.APU.SetOutput(resampler, hb, hostSampleRate)
	c.APU.Volume = float32(c.Config().Audio.Volume) / 100.0

	return &Frontend{
		core: c,
		tex:  ebiten.NewImage(width, height),
		pix:  make([]byte, width*height*4),
	}
}

func (f *Frontend) setupAudio() error {
	ctx := audio.NewContext(hostSampleRate)
	player, err := ctx.NewPlayer(&audioReader{f: f})
	if err != nil {
		return err
	}
	// Small buffer to keep latency low; we feed continuously.
	player.SetBufferSize(50 * 1000 * 1000) // 50ms — ebiten parses as time.Duration
	player.Play()
	return nil
}

// Standard GBA keyboard mapping (Z/X = A/B, arrow keys, etc.).
var keyMap = map[ebiten.Key]keypad.Key{
	ebiten.KeyZ:          keypad.KeyA,
	ebiten.KeyX:          keypad.KeyB,
	ebiten.KeyBackspace:  keypad.KeySelect,
	ebiten.KeyEnter:      keypad.KeyStart,
	ebiten.KeyArrowRight: keypad.KeyRight,
	ebiten.KeyArrowLeft:  keypad.KeyLeft,
	ebiten.KeyArrowUp:    keypad.KeyUp,
	ebiten.KeyArrowDown:  keypad.KeyDown,
	ebiten.KeyA:          keypad.KeyL,
	ebiten.KeyS:          keypad.KeyR,
}

func (f *Frontend) Update() error {
	for k, btn := range keyMap {
		f.core.Keypad.SetKeyStatus(btn, ebiten.IsKeyPressed(k))
	}
	// APU re-tunes the resampler internally when its native rate changes
	// (matches upstream's resolution_old tracking). No frontend action.
	f.core.RunFrame()

	// Copy the freshly-completed framebuffer (read the OTHER frame slot —
	// PPU writes to Frame, then ppu.cc swaps after VBlank ends).
	fb := f.core.PPU.Output[f.core.PPU.Frame^1]
	for i, p := range fb {
		f.pix[i*4+0] = uint8(p >> 16) // R
		f.pix[i*4+1] = uint8(p >> 8)  // G
		f.pix[i*4+2] = uint8(p)       // B
		f.pix[i*4+3] = 0xFF
	}
	f.tex.WritePixels(f.pix)
	return nil
}

func (f *Frontend) Draw(screen *ebiten.Image) {
	op := &ebiten.DrawImageOptions{}
	op.GeoM.Scale(scale, scale)
	screen.DrawImage(f.tex, op)
}

func (f *Frontend) Layout(_, _ int) (int, int) {
	return width * scale, height * scale
}

func (f *Frontend) Run() error {
	ebiten.SetWindowSize(width*scale, height*scale)
	ebiten.SetWindowTitle("nanogoadvance")
	ebiten.SetTPS(60)
	if err := f.setupAudio(); err != nil {
		return err
	}
	return ebiten.RunGame(f)
}

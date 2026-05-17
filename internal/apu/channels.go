// channels.go — port of src/nba/src/hw/apu/channel/*.
//
// Per-channel state + sample synthesis for the four GBA PSG channels:
//   1 (Quad+sweep), 2 (Quad), 3 (Wave), 4 (Noise)
// plus the BaseChannel frame sequencer helpers (length, envelope, sweep)
// and the 7-deep DMA FIFO.
package apu

import "github.com/mnmlyw/nanogoadvance/internal/scheduler"

// LengthCounter ⇄ length_counter.hh.
type LengthCounter struct {
	Length        int
	Enabled       bool
	defaultLength int
}

func (l *LengthCounter) Init(defaultLength int) { l.defaultLength = defaultLength; l.Reset() }
func (l *LengthCounter) Reset()                 { l.Enabled = false; l.Length = 0 }
func (l *LengthCounter) Restart() {
	if l.Length == 0 {
		l.Length = l.defaultLength
	}
}
func (l *LengthCounter) Tick() bool {
	if l.Enabled {
		l.Length--
		return l.Length > 0
	}
	return true
}

// EnvelopeDirection ⇄ Envelope::Direction.
const (
	EnvDecrement = 0
	EnvIncrement = 1
)

// Envelope ⇄ envelope.hh.
type Envelope struct {
	Active        bool
	Enabled       bool
	Direction     int
	InitialVolume int
	CurrentVolume int
	Divider       int
	step          int
}

func (e *Envelope) Reset() {
	e.Direction = EnvDecrement
	e.InitialVolume = 0
	e.Divider = 0
	e.Restart()
}

func (e *Envelope) Restart() {
	e.step = e.Divider
	e.CurrentVolume = e.InitialVolume
	e.Active = e.Enabled
}

func (e *Envelope) Tick() {
	if e.step == 1 {
		e.step = e.Divider
		if e.Active && e.Divider != 0 {
			if e.Direction == EnvIncrement {
				if e.CurrentVolume != 15 {
					e.CurrentVolume++
				} else {
					e.Active = false
				}
			} else {
				if e.CurrentVolume != 0 {
					e.CurrentVolume--
				} else {
					e.Active = false
				}
			}
		}
	} else {
		e.step = (e.step - 1) & 7
	}
}

// SweepDirection ⇄ Sweep::Direction.
const (
	SweepIncrement = 0
	SweepDecrement = 1
)

// Sweep ⇄ sweep.hh.
type Sweep struct {
	Active      bool
	Enabled     bool
	Direction   int
	CurrentFreq int
	ShadowFreq  int
	Divider     int
	Shift       int
	step        int
}

func (s *Sweep) Reset() {
	s.Direction = SweepIncrement
	s.Divider = 0
	s.Shift = 0
	s.Restart()
}

func (s *Sweep) Restart() {
	if s.Enabled {
		s.ShadowFreq = s.CurrentFreq
		s.step = s.Divider
		s.Active = s.Shift != 0 || s.Divider != 0
	}
}

func (s *Sweep) Tick() bool {
	if s.Active {
		s.step--
		if s.step == 0 {
			offset := s.ShadowFreq >> s.Shift
			s.step = s.Divider
			newFreq := s.ShadowFreq
			if s.Direction == SweepIncrement {
				newFreq += offset
			} else {
				newFreq -= offset
			}
			if newFreq >= 2048 {
				return false
			}
			if s.Shift != 0 {
				s.ShadowFreq = newFreq
				s.CurrentFreq = newFreq
			}
		}
	}
	return true
}

// BaseChannel ⇄ base_channel.hh — embedded frame-sequencer state.
const CyclesPerStep = 16777216 / 512

type BaseChannel struct {
	Length   LengthCounter
	Envelope Envelope
	Sweep    Sweep
	enabled  bool
	step     int
}

func (b *BaseChannel) Init(enableEnvelope, enableSweep bool, defaultLength int) {
	b.Length.Init(defaultLength)
	b.Envelope.Enabled = enableEnvelope
	b.Sweep.Enabled = enableSweep
	b.Reset()
}

func (b *BaseChannel) Reset() {
	b.Length.Reset()
	b.Envelope.Reset()
	b.Sweep.Reset()
	b.enabled = false
	b.step = 0
}

func (b *BaseChannel) IsEnabled() bool { return b.enabled }

func (b *BaseChannel) Tick() {
	if (b.step & 1) == 0 {
		b.enabled = b.enabled && b.Length.Tick()
	}
	if (b.step & 3) == 2 {
		b.enabled = b.enabled && b.Sweep.Tick()
	}
	if b.step == 7 {
		b.Envelope.Tick()
	}
	b.step = (b.step + 1) & 7
}

func (b *BaseChannel) Restart() {
	b.Length.Restart()
	b.Sweep.Restart()
	b.Envelope.Restart()
	b.enabled = true
	b.step = 0
}

func (b *BaseChannel) Disable() { b.enabled = false }

// ---------------------------------------------------------------------
// QuadChannel ⇄ quad_channel.{hh,cc}. Channels 1 and 2 (1 has sweep).
// ---------------------------------------------------------------------

type QuadChannel struct {
	BaseChannel
	scheduler *scheduler.Scheduler
	eventID   scheduler.EventID
	hasEvent  bool
	// genClass ⇄ EventClassAPUPSG1Generate or EventClassAPUPSG2Generate
	// — set by the constructor so each channel can re-schedule its own
	// Generate event under the right class.
	genClass scheduler.EventClass

	Sample    int8
	phase     int
	waveDuty  int
	dacEnable bool
}

// NewQuadChannel constructs a PSG1 (hasSweep=true) or PSG2 channel.
func NewQuadChannel(s *scheduler.Scheduler, hasSweep bool) *QuadChannel {
	q := &QuadChannel{scheduler: s}
	if hasSweep {
		q.genClass = scheduler.EventClassAPUPSG1Generate
	} else {
		q.genClass = scheduler.EventClassAPUPSG2Generate
	}
	s.Register(q.genClass, func(uint64) { q.Generate() })
	q.Init(true, hasSweep, 64)
	return q
}

var quadPattern = [4][8]int8{
	{+8, -8, -8, -8, -8, -8, -8, -8},
	{+8, +8, -8, -8, -8, -8, -8, -8},
	{+8, +8, +8, +8, -8, -8, -8, -8},
	{+8, +8, +8, +8, +8, +8, -8, -8},
}

func (q *QuadChannel) synthesisInterval(frequency int) int64 {
	return int64(128 * (2048 - frequency) / 8)
}

func (q *QuadChannel) Generate() {
	if !q.IsEnabled() {
		q.Sample = 0
		q.hasEvent = false
		return
	}
	if q.dacEnable {
		q.Sample = int8(int(quadPattern[q.waveDuty][q.phase]) * q.Envelope.CurrentVolume)
	} else {
		q.Sample = 0
	}
	q.phase = (q.phase + 1) % 8
	q.eventID = q.scheduler.AddClass(q.synthesisInterval(q.Sweep.CurrentFreq), q.genClass, 0, 0)
	q.hasEvent = true
}

func (q *QuadChannel) Read(offset int) uint8 {
	switch offset {
	case 0:
		return uint8(q.Sweep.Shift) | uint8(q.Sweep.Direction)<<3 | uint8(q.Sweep.Divider)<<4
	case 1:
		return 0
	case 2:
		return uint8(q.waveDuty) << 6
	case 3:
		return uint8(q.Envelope.Divider) | uint8(q.Envelope.Direction)<<3 | uint8(q.Envelope.InitialVolume)<<4
	case 4:
		return 0
	case 5:
		if q.Length.Enabled {
			return 0x40
		}
	}
	return 0
}

func (q *QuadChannel) Write(offset int, value uint8) {
	switch offset {
	case 0:
		q.Sweep.Shift = int(value & 7)
		q.Sweep.Direction = int((value >> 3) & 1)
		q.Sweep.Divider = int((value >> 4) & 7)
	case 1:
		// unused
	case 2:
		q.Length.Length = 64 - int(value&63)
		q.waveDuty = int((value >> 6) & 3)
	case 3:
		q.Envelope.Divider = int(value & 7)
		q.Envelope.Direction = int((value >> 3) & 1)
		q.Envelope.InitialVolume = int(value >> 4)
		q.dacEnable = (value >> 3) != 0
		if !q.dacEnable {
			q.Disable()
		}
	case 4:
		q.Sweep.CurrentFreq = (q.Sweep.CurrentFreq &^ 0xFF) | int(value)
	case 5:
		q.Sweep.CurrentFreq = (q.Sweep.CurrentFreq & 0xFF) | (int(value&7) << 8)
		q.Length.Enabled = value&0x40 != 0
		if q.dacEnable && value&0x80 != 0 {
			if !q.IsEnabled() {
				if q.hasEvent {
					q.scheduler.Cancel(q.eventID)
				}
				q.eventID = q.scheduler.AddClass(q.synthesisInterval(q.Sweep.CurrentFreq), q.genClass, 0, 0)
				q.hasEvent = true
			}
			q.phase = 0
			q.Restart()
		}
	}
}

// ---------------------------------------------------------------------
// WaveChannel ⇄ wave_channel.{hh,cc}.
// ---------------------------------------------------------------------

type WaveResetWaveRAM int

const (
	WaveResetNo  WaveResetWaveRAM = 0
	WaveResetYes WaveResetWaveRAM = 1
)

type WaveChannel struct {
	BaseChannel
	scheduler *scheduler.Scheduler
	eventID   scheduler.EventID
	hasEvent  bool

	Sample      int8
	playing     bool
	forceVolume bool
	volume      int
	frequency   int
	dimension   int
	waveBank    int
	WaveRAM     [2][16]uint8
	phase       int
}

func NewWaveChannel(s *scheduler.Scheduler) *WaveChannel {
	w := &WaveChannel{scheduler: s}
	s.Register(scheduler.EventClassAPUPSG3Generate, func(uint64) { w.Generate() })
	w.Init(false, false, 256)
	w.Reset(WaveResetYes)
	return w
}

func (w *WaveChannel) Reset(resetWaveRAM WaveResetWaveRAM) {
	w.BaseChannel.Reset()
	w.phase = 0
	w.Sample = 0
	w.playing = false
	w.forceVolume = false
	w.volume = 0
	w.frequency = 0
	w.dimension = 0
	w.waveBank = 0
	if resetWaveRAM == WaveResetYes {
		for i := range w.WaveRAM {
			for j := range w.WaveRAM[i] {
				w.WaveRAM[i][j] = 0
			}
		}
	}
	w.hasEvent = false
}

func (w *WaveChannel) IsEnabled() bool { return w.playing && w.BaseChannel.IsEnabled() }

func (w *WaveChannel) synthesisInterval(frequency int) int64 {
	return int64(8 * (2048 - frequency))
}

func (w *WaveChannel) Generate() {
	if !w.IsEnabled() {
		w.Sample = 0
		if w.BaseChannel.IsEnabled() {
			w.eventID = w.scheduler.AddClass(w.synthesisInterval(w.frequency), scheduler.EventClassAPUPSG3Generate, 0, 0)
			w.hasEvent = true
		} else {
			w.hasEvent = false
		}
		return
	}
	byte_ := w.WaveRAM[w.waveBank][w.phase/2]
	var s int8
	if w.phase%2 == 0 {
		s = int8(byte_ >> 4)
	} else {
		s = int8(byte_ & 15)
	}
	volTable := [4]int{0, 4, 2, 1}
	var mul int
	if w.forceVolume {
		mul = 3
	} else {
		mul = volTable[w.volume]
	}
	w.Sample = int8((int(s) - 8) * 4 * mul)
	w.phase++
	if w.phase == 32 {
		w.phase = 0
		if w.dimension != 0 {
			w.waveBank ^= 1
		}
	}
	w.eventID = w.scheduler.AddClass(w.synthesisInterval(w.frequency), scheduler.EventClassAPUPSG3Generate, 0, 0)
	w.hasEvent = true
}

func (w *WaveChannel) ReadSample(offset int) uint8 {
	return w.WaveRAM[w.waveBank^1][offset]
}

func (w *WaveChannel) WriteSample(offset int, value uint8) {
	w.WaveRAM[w.waveBank^1][offset] = value
}

func (w *WaveChannel) Read(offset int) uint8 {
	switch offset {
	case 0:
		v := uint8(w.dimension) << 5
		v |= uint8(w.waveBank) << 6
		if w.playing {
			v |= 0x80
		}
		return v
	case 3:
		v := uint8(w.volume) << 5
		if w.forceVolume {
			v |= 0x80
		}
		return v
	case 5:
		if w.Length.Enabled {
			return 0x40
		}
	}
	return 0
}

func (w *WaveChannel) Write(offset int, value uint8) {
	switch offset {
	case 0:
		w.dimension = int((value >> 5) & 1)
		w.waveBank = int((value >> 6) & 1)
		w.playing = value&0x80 != 0
	case 2:
		w.Length.Length = 256 - int(value)
	case 3:
		w.volume = int((value >> 5) & 3)
		w.forceVolume = value&0x80 != 0
	case 4:
		w.frequency = (w.frequency &^ 0xFF) | int(value)
	case 5:
		w.frequency = (w.frequency & 0xFF) | (int(value&7) << 8)
		w.Length.Enabled = value&0x40 != 0
		if w.playing && value&0x80 != 0 {
			if !w.BaseChannel.IsEnabled() {
				if w.hasEvent {
					w.scheduler.Cancel(w.eventID)
				}
				w.eventID = w.scheduler.AddClass(w.synthesisInterval(w.frequency), scheduler.EventClassAPUPSG3Generate, 0, 0)
				w.hasEvent = true
			}
			w.phase = 0
			if w.dimension != 0 {
				w.waveBank = 0
			}
			w.Restart()
		}
	}
}

// ---------------------------------------------------------------------
// NoiseChannel ⇄ noise_channel.{hh,cc}.
// ---------------------------------------------------------------------

type NoiseChannel struct {
	BaseChannel
	scheduler *scheduler.Scheduler
	eventID   scheduler.EventID
	hasEvent  bool

	bias *BIAS

	lfsr           uint16
	Sample         int8
	frequencyShift int
	frequencyRatio int
	width          int
	dacEnable      bool
	skipCount      int
}

func NewNoiseChannel(s *scheduler.Scheduler, bias *BIAS) *NoiseChannel {
	n := &NoiseChannel{scheduler: s, bias: bias}
	s.Register(scheduler.EventClassAPUPSG4Generate, func(uint64) { n.Generate() })
	n.Init(true, false, 64)
	return n
}

func (n *NoiseChannel) synthesisInterval(ratio, shift int) int64 {
	interval := int64(64 << shift)
	if ratio == 0 {
		interval /= 2
	} else {
		interval *= int64(ratio)
	}
	return interval
}

func (n *NoiseChannel) Generate() {
	if !n.IsEnabled() {
		n.Sample = 0
		n.hasEvent = false
		return
	}
	lfsrXor := [2]uint16{0x6000, 0x60}
	carry := int(n.lfsr & 1)
	n.lfsr >>= 1
	if carry != 0 {
		n.lfsr ^= lfsrXor[n.width]
		n.Sample = +8
	} else {
		n.Sample = -8
	}
	n.Sample = int8(int(n.Sample) * n.Envelope.CurrentVolume)
	if !n.dacEnable {
		n.Sample = 0
	}
	for i := 0; i < n.skipCount; i++ {
		carry = int(n.lfsr & 1)
		n.lfsr >>= 1
		if carry != 0 {
			n.lfsr ^= lfsrXor[n.width]
		}
	}
	noiseInterval := n.synthesisInterval(n.frequencyRatio, n.frequencyShift)
	mixerInterval := int64(n.bias.GetSampleInterval())
	if noiseInterval < mixerInterval {
		n.skipCount = int(mixerInterval/noiseInterval) - 1
		noiseInterval = mixerInterval
	} else {
		n.skipCount = 0
	}
	n.eventID = n.scheduler.AddClass(noiseInterval, scheduler.EventClassAPUPSG4Generate, 0, 0)
	n.hasEvent = true
}

func (n *NoiseChannel) Read(offset int) uint8 {
	switch offset {
	case 1:
		return uint8(n.Envelope.Divider) | uint8(n.Envelope.Direction)<<3 | uint8(n.Envelope.InitialVolume)<<4
	case 4:
		return uint8(n.frequencyRatio) | uint8(n.width)<<3 | uint8(n.frequencyShift)<<4
	case 5:
		if n.Length.Enabled {
			return 0x40
		}
	}
	return 0
}

func (n *NoiseChannel) Write(offset int, value uint8) {
	switch offset {
	case 0:
		n.Length.Length = 64 - int(value&63)
	case 1:
		n.Envelope.Divider = int(value & 7)
		n.Envelope.Direction = int((value >> 3) & 1)
		n.Envelope.InitialVolume = int(value >> 4)
		n.dacEnable = (value >> 3) != 0
		if !n.dacEnable {
			n.Disable()
		}
	case 4:
		n.frequencyRatio = int(value & 7)
		n.width = int((value >> 3) & 1)
		n.frequencyShift = int(value >> 4)
	case 5:
		n.Length.Enabled = value&0x40 != 0
		if n.dacEnable && value&0x80 != 0 {
			if !n.IsEnabled() {
				n.skipCount = 0
				if n.hasEvent {
					n.scheduler.Cancel(n.eventID)
				}
				n.eventID = n.scheduler.AddClass(n.synthesisInterval(n.frequencyRatio, n.frequencyShift), scheduler.EventClassAPUPSG4Generate, 0, 0)
				n.hasEvent = true
			}
			lfsrInit := [2]uint16{0x4000, 0x0040}
			n.lfsr = lfsrInit[n.width]
			n.Restart()
		}
	}
}

// ---------------------------------------------------------------------
// FIFO ⇄ fifo.hh. 7-deep ring buffer of 32-bit words for DMA-sound.
// ---------------------------------------------------------------------

const fifoLen = 7

type WordFIFO struct {
	data  [fifoLen]uint32
	rdPtr int
	wrPtr int
	count int
}

func (f *WordFIFO) Reset()      { *f = WordFIFO{} }
func (f *WordFIFO) Count() int  { return f.count }

func (f *WordFIFO) WriteWord(value uint32) {
	if f.count < fifoLen {
		f.data[f.wrPtr] = value
		f.wrPtr++
		if f.wrPtr == fifoLen {
			f.wrPtr = 0
		}
		f.count++
	} else {
		f.Reset()
	}
}

func (f *WordFIFO) WriteByte(offset uint32, value uint8) {
	shift := offset * 8
	f.WriteWord((f.data[f.wrPtr] &^ (0xFF << shift)) | (uint32(value) << shift))
}

func (f *WordFIFO) WriteHalf(offset uint32, value uint16) {
	// Upstream's parameter type is `u8 value` (apparent typo) — the
	// halfword is truncated to its low byte before being shifted in.
	// Match upstream verbatim so behavior stays bit-identical.
	shift := offset * 8
	v := uint32(uint8(value))
	f.WriteWord((f.data[f.wrPtr] &^ (0xFFFF << shift)) | (v << shift))
}

func (f *WordFIFO) ReadWord() uint32 {
	value := f.data[f.rdPtr]
	if f.count > 0 {
		f.rdPtr++
		if f.rdPtr == fifoLen {
			f.rdPtr = 0
		}
		f.count--
	}
	return value
}

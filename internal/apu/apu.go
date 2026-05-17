// Package apu — port of src/nba/src/hw/apu/.
//
// This file ports the top-level APU + SoundControl/BIAS registers
// (registers.{hh,cc}) and apu.{hh,cc}'s OnTimerOverflow / StepSequencer /
// StepMixer (without resampling — no audio device sink).
//
// Channel synthesis lives in channels.go (QuadChannel, WaveChannel,
// NoiseChannel, FIFO, plus length/envelope/sweep helpers).
package apu

import (
	"sync"

	"github.com/mnmlyw/nanogoadvance/internal/scheduler"
)

// Side ⇄ apu/registers.hh:Side.
const (
	SideLeft  = 0
	SideRight = 1
)

// PSG block of SoundControl.
type PSG struct {
	Volume int
	Master [2]int
	Enable [2][4]int
}

// DMASound channel (A or B).
type DMASound struct {
	Volume  int
	Enable  [2]int
	TimerID int
}

// SoundControl ⇄ SoundControl in registers.{hh,cc}.
type SoundControl struct {
	MasterEnable bool
	PSG          PSG
	DMA          [2]DMASound

	fifoReset func(idx int)
	psgReset  func(idx int)
}

func (s *SoundControl) Reset() {
	s.MasterEnable = false
	s.PSG = PSG{}
	s.DMA[0] = DMASound{}
	s.DMA[1] = DMASound{}
}

func (s *SoundControl) Read(address int) uint8 {
	switch address {
	case 0:
		return uint8(s.PSG.Master[SideRight]) | uint8(s.PSG.Master[SideLeft])<<4
	case 1:
		v := uint8(0)
		for i := range 4 {
			if s.PSG.Enable[SideRight][i] != 0 {
				v |= 1 << i
			}
			if s.PSG.Enable[SideLeft][i] != 0 {
				v |= 1 << (4 + i)
			}
		}
		return v
	case 2:
		return uint8(s.PSG.Volume) | uint8(s.DMA[0].Volume)<<2 | uint8(s.DMA[1].Volume)<<3
	case 3:
		v := uint8(0)
		if s.DMA[0].Enable[SideRight] != 0 {
			v |= 1
		}
		if s.DMA[0].Enable[SideLeft] != 0 {
			v |= 2
		}
		if s.DMA[0].TimerID != 0 {
			v |= 4
		}
		if s.DMA[1].Enable[SideRight] != 0 {
			v |= 16
		}
		if s.DMA[1].Enable[SideLeft] != 0 {
			v |= 32
		}
		if s.DMA[1].TimerID != 0 {
			v |= 64
		}
		return v
	case 4:
		v := uint8(0)
		if s.MasterEnable {
			v |= 128
		}
		return v
	}
	return 0
}

func (s *SoundControl) Write(address int, value uint8) {
	switch address {
	case 0:
		s.PSG.Master[SideRight] = int(value & 7)
		s.PSG.Master[SideLeft] = int((value >> 4) & 7)
	case 1:
		for i := range 4 {
			if value&(1<<i) != 0 {
				s.PSG.Enable[SideRight][i] = 1
			} else {
				s.PSG.Enable[SideRight][i] = 0
			}
			if value&(1<<(4+i)) != 0 {
				s.PSG.Enable[SideLeft][i] = 1
			} else {
				s.PSG.Enable[SideLeft][i] = 0
			}
		}
	case 2:
		s.PSG.Volume = int(value & 3)
		s.DMA[0].Volume = int((value >> 2) & 1)
		s.DMA[1].Volume = int((value >> 3) & 1)
	case 3:
		s.DMA[0].Enable[SideRight] = int(value & 1)
		s.DMA[0].Enable[SideLeft] = int((value >> 1) & 1)
		s.DMA[0].TimerID = int((value >> 2) & 1)
		s.DMA[1].Enable[SideRight] = int((value >> 4) & 1)
		s.DMA[1].Enable[SideLeft] = int((value >> 5) & 1)
		s.DMA[1].TimerID = int((value >> 6) & 1)
		if value&0x08 != 0 && s.fifoReset != nil {
			s.fifoReset(0)
		}
		if value&0x80 != 0 && s.fifoReset != nil {
			s.fifoReset(1)
		}
	case 4:
		oldMasterEnable := s.MasterEnable
		s.MasterEnable = value&128 != 0
		if oldMasterEnable && !s.MasterEnable {
			s.Write(0, 0)
			s.Write(1, 0)
			if s.psgReset != nil {
				s.psgReset(0)
				s.psgReset(1)
				s.psgReset(2)
				s.psgReset(3)
			}
			if s.fifoReset != nil {
				s.fifoReset(0)
				s.fifoReset(1)
			}
		}
	}
}

func (s *SoundControl) ReadWord() uint32 {
	return uint32(s.Read(0)) |
		uint32(s.Read(1))<<8 |
		uint32(s.Read(2))<<16 |
		uint32(s.Read(3))<<24
}

func (s *SoundControl) WriteWord(value uint32) {
	s.Write(0, uint8(value))
	s.Write(1, uint8(value>>8))
	s.Write(2, uint8(value>>16))
	s.Write(3, uint8(value>>24))
}

// BIAS ⇄ BIAS in registers.{hh,cc}.
type BIAS struct {
	Level      int
	Resolution int
}

func (b *BIAS) Reset() { b.Level = 0x200; b.Resolution = 0 }

func (b *BIAS) Read(address int) uint8 {
	switch address {
	case 0:
		return uint8(b.Level)
	case 1:
		return uint8((b.Level>>8)&3) | uint8(b.Resolution)<<6
	}
	return 0
}

func (b *BIAS) Write(address int, value uint8) {
	switch address {
	case 0:
		b.Level = (b.Level &^ 0xFF) | int(value & ^uint8(1))
	case 1:
		b.Level = (b.Level & 0xFF) | (int(value&3) << 8)
		b.Resolution = int(value >> 6)
	}
}

func (b *BIAS) ReadHalf() uint16   { return uint16(b.Read(0)) | uint16(b.Read(1))<<8 }
func (b *BIAS) WriteHalf(v uint16) { b.Write(0, uint8(v)); b.Write(1, uint8(v>>8)) }

func (b *BIAS) GetSampleInterval() int { return 512 >> b.Resolution }
func (b *BIAS) GetSampleRate() int     { return 32768 << b.Resolution }

// ---------------------------------------------------------------------
// APU top-level — owns SoundControl, BIAS, four PSG channels, two FIFOs.
// ---------------------------------------------------------------------

type Pipe struct {
	Word uint32
	Size int
}

// MP2KEngine ⇄ the slice of the HLE MP2K engine the mixer needs. Defined
// here as an interface so the APU package doesn't import internal/apu/hle.
type MP2KEngine interface {
	IsEngaged() bool
	ReadSample() (r, l float32)
}

// StereoResampler ⇄ the slice of dsp.Resampler the APU drives in
// StepMixer. Defined as an interface so internal/apu doesn't pull in the
// dsp package (the frontend constructs the concrete resampler).
type StereoResampler interface {
	Write(r, l float32)
	SetSampleRates(in, out float32)
}

// StereoSink ⇄ the slice of the host-rate ring buffer the resampler writes
// into. ReadSamples drains it 1:1 — the consumer never sees the APU's
// native rate, only host-rate samples (matching upstream's
// StereoRingBuffer<float> model).
type StereoSink interface {
	Pop() (r, l float32, ok bool)
}

type APU struct {
	scheduler *scheduler.Scheduler
	SOUNDCNT  SoundControl
	BIAS      BIAS
	FIFO      [2]WordFIFO
	PSG1      *QuadChannel
	PSG2      *QuadChannel
	PSG3      *WaveChannel
	PSG4      *NoiseChannel

	mp2k MP2KEngine

	// ResolutionOld ⇄ APU::resolution_old. Tracks the last BIAS
	// resolution we re-tuned the resampler against — re-tuned in
	// StepMixer when it changes.
	ResolutionOld int

	// resampler / sink ⇄ APU::resampler + APU::buffer. Owned by the APU
	// (mirrors upstream's structure): stepMixer writes float stereo
	// through the resampler at the APU's native rate; the resampler
	// converts to host rate and pushes into the sink ring.
	resampler StereoResampler
	sink      StereoSink

	// hostSampleRate caches what we last told the resampler the host
	// rate is, so we don't re-tune it every mixer tick.
	hostSampleRate int

	fifoPipe [2]Pipe
	latch    [2]int8

	// Volume ⇄ Config::audio.volume — applied at sample read time so the
	// user can tweak it live without restarting.
	Volume float32

	sampleMu sync.Mutex
}

// SetMP2K wires the HLE MP2K engine. When the engine is engaged the
// mixer uses MP2K's high-fidelity DMA-FIFO replacement.
func (a *APU) SetMP2K(m MP2KEngine) { a.mp2k = m }

// DMARequester is the slice of the DMA controller APU needs to drain FIFOs.
// Wired by core; nil-safe.
type DMARequester interface {
	RequestFIFO(idx int)
}

var dmaReq DMARequester

// SetDMARequester wires the DMA controller (called by core during init).
func (a *APU) SetDMARequester(d DMARequester) { dmaReq = d }

func New(s *scheduler.Scheduler) *APU {
	a := &APU{scheduler: s, Volume: 1.0}
	a.PSG1 = NewQuadChannel(s, true) // PSG1 has sweep
	a.PSG2 = NewQuadChannel(s, false)
	a.PSG3 = NewWaveChannel(s)
	a.PSG4 = NewNoiseChannel(s, &a.BIAS)
	a.SOUNDCNT.fifoReset = func(idx int) { a.FIFO[idx].Reset() }
	a.SOUNDCNT.psgReset = func(idx int) {
		switch idx {
		case 0:
			a.PSG1.Reset()
		case 1:
			a.PSG2.Reset()
		case 2:
			a.PSG3.Reset(WaveResetNo)
		case 3:
			a.PSG4.Reset()
		}
	}
	a.BIAS.Reset()
	// Register the class callbacks so AddClass dispatches and save-state
	// LoadState can re-fire the mixer/sequencer after restoration.
	// Scheduling happens in Reset() (matches upstream's APU::Reset).
	s.Register(scheduler.EventClassAPUMixer, func(uint64) { a.stepMixerClass() })
	s.Register(scheduler.EventClassAPUSequencer, func(uint64) { a.stepSequencerClass() })
	a.Reset()
	return a
}

// SetOutput ⇄ APU::buffer + APU::resampler wiring. Called by the frontend
// after constructing the resampler chain. hostRate is the audio device's
// playback rate; the APU re-tunes the resampler whenever its native
// rate changes (BIAS resolution or MP2K engagement).
func (a *APU) SetOutput(r StereoResampler, sink StereoSink, hostRate int) {
	a.resampler = r
	a.sink = sink
	a.hostSampleRate = hostRate
	a.ResolutionOld = -1 // force a re-tune on the next stepMixer
}

// Reset ⇄ APU::Reset. Replays the constructor's scheduler registrations
// after a full Scheduler.Reset, so a Core reset re-engages audio.
func (a *APU) Reset() {
	a.FIFO[0].Reset()
	a.FIFO[1].Reset()
	a.PSG1.Reset()
	a.PSG2.Reset()
	a.PSG3.Reset(WaveResetYes)
	a.PSG4.Reset()
	a.SOUNDCNT.Reset()
	a.BIAS.Reset()
	a.fifoPipe[0] = Pipe{}
	a.fifoPipe[1] = Pipe{}
	a.latch[0] = 0
	a.latch[1] = 0
	a.ResolutionOld = -1
	a.scheduler.AddClass(int64(a.BIAS.GetSampleInterval()), scheduler.EventClassAPUMixer, 0, 0)
	a.scheduler.AddClass(int64(CyclesPerStep), scheduler.EventClassAPUSequencer, 0, 0)
	// Reset MP2K HLE state (upstream apu.cc:41-42 calls mp2k.Reset()
	// and clears mp2k_read_index on every APU.Reset).
	if resettable, ok := a.mp2k.(interface{ Reset() }); ok && resettable != nil {
		resettable.Reset()
	}
}

// stepSequencerClass / stepMixerClass — re-add themselves via AddClass.
func (a *APU) stepSequencerClass() {
	a.PSG1.Tick()
	a.PSG2.Tick()
	a.PSG3.Tick()
	a.PSG4.Tick()
	a.scheduler.AddClass(int64(CyclesPerStep), scheduler.EventClassAPUSequencer, 0, 0)
}

func (a *APU) stepMixerClass() { a.stepMixer(0) }

// stepMixer ⇄ APU::StepMixer — produces one stereo sample per call,
// feeds it through the APU-owned resampler, which writes host-rate
// samples into the sink ring (1:1 port of upstream's StepMixer flow).
func (a *APU) stepMixer(_ int64) {
	psgVolumeTab := [4]int{1, 2, 4, 0}
	dmaVolumeTab := [2]int{2, 4}

	psg := &a.SOUNDCNT.PSG
	dma := &a.SOUNDCNT.DMA
	psgVolume := psgVolumeTab[psg.Volume]

	// MP2K HLE path: replace the DMA-FIFO stream with MP2K's pre-mixed
	// float samples. Mixer runs at MP2K's native 65536 Hz.
	if a.mp2k != nil && a.mp2k.IsEngaged() {
		// resolution_old==1 in upstream means "MP2K-engaged rate is set";
		// re-tune the resampler on the engage transition.
		if a.ResolutionOld != 1 && a.resampler != nil {
			a.resampler.SetSampleRates(65536, float32(a.hostSampleRate))
			a.ResolutionOld = 1
		}
		mp2kR, mp2kL := a.mp2k.ReadSample()
		var fsample [2]float32
		for ch := range 2 {
			var psgSample int16
			if psg.Enable[ch][0] != 0 {
				psgSample += int16(a.PSG1.Sample)
			}
			if psg.Enable[ch][1] != 0 {
				psgSample += int16(a.PSG2.Sample)
			}
			if psg.Enable[ch][2] != 0 {
				psgSample += int16(a.PSG3.Sample)
			}
			if psg.Enable[ch][3] != 0 {
				psgSample += int16(a.PSG4.Sample)
			}
			fsample[ch] = float32(int(psgSample)*psgVolume*int(psg.Master[ch]+1)) / (32.0 * 0x200)
			fifoSample := [2]float32{mp2kR, mp2kL}
			for f := range 2 {
				if dma[f].Enable[ch] != 0 {
					fsample[ch] += fifoSample[f] * float32(dmaVolumeTab[dma[f].Volume]) * 0.25
				}
			}
		}
		if !a.SOUNDCNT.MasterEnable {
			fsample[0] = 0
			fsample[1] = 0
		}
		if a.resampler != nil {
			a.sampleMu.Lock()
			a.resampler.Write(fsample[0], fsample[1])
			a.sampleMu.Unlock()
		}
		// MP2K runs at 65536 Hz (period 256 cycles). Align to global
		// cycle boundaries the same way upstream does — `256 - (now &
		// 255)` keeps the mixer phase-locked to the system clock.
		const period = int64(16777216 / 65536)
		cycles := period - (a.scheduler.Now() & (period - 1))
		a.scheduler.AddClass(cycles, scheduler.EventClassAPUMixer, 0, 0)
		return
	}

	// PSG + DMA-FIFO path. Re-tune the resampler whenever BIAS
	// resolution changes (mirrors upstream's resolution_old check).
	bias := &a.BIAS
	if a.ResolutionOld != bias.Resolution+2 && a.resampler != nil {
		a.resampler.SetSampleRates(float32(bias.GetSampleRate()), float32(a.hostSampleRate))
		// Encode "BIAS resolution N" as (N+2) to keep the MP2K sentinel
		// (1) and the "never set" sentinel (-1) distinct.
		a.ResolutionOld = bias.Resolution + 2
	}

	var sample [2]int32 // L, R
	for ch := range 2 {
		var psgSample int16
		if psg.Enable[ch][0] != 0 {
			psgSample += int16(a.PSG1.Sample)
		}
		if psg.Enable[ch][1] != 0 {
			psgSample += int16(a.PSG2.Sample)
		}
		if psg.Enable[ch][2] != 0 {
			psgSample += int16(a.PSG3.Sample)
		}
		if psg.Enable[ch][3] != 0 {
			psgSample += int16(a.PSG4.Sample)
		}
		sample[ch] = int32(psgSample) * int32(psgVolume) * int32(psg.Master[ch]+1) >> 5
		for f := range 2 {
			if dma[f].Enable[ch] != 0 {
				sample[ch] += int32(a.latch[f]) * int32(dmaVolumeTab[dma[f].Volume])
			}
		}
		sample[ch] += int32(a.BIAS.Level)
		if sample[ch] < 0 {
			sample[ch] = 0
		} else if sample[ch] > 0x3FF {
			sample[ch] = 0x3FF
		}
		sample[ch] -= 0x200
	}
	if !a.SOUNDCNT.MasterEnable {
		sample[0] = 0
		sample[1] = 0
	}

	// Convert to float (upstream: sample / float(0x200)) and push through
	// the resampler.
	if a.resampler != nil {
		a.sampleMu.Lock()
		a.resampler.Write(float32(sample[0])/float32(0x200), float32(sample[1])/float32(0x200))
		a.sampleMu.Unlock()
	}

	// Align the mixer to global cycle boundaries (matches upstream).
	sampleInterval := int64(a.BIAS.GetSampleInterval())
	cycles := sampleInterval - (a.scheduler.Now() & (sampleInterval - 1))
	a.scheduler.AddClass(cycles, scheduler.EventClassAPUMixer, 0, 0)
}

// SampleRate returns the current APU output sample rate in Hz. When the
// MP2K HLE engine is engaged the mixer produces samples at a fixed
// 65536 Hz; otherwise it tracks BIAS.resolution (32k/64k/128k/256k).
func (a *APU) SampleRate() int {
	if a.mp2k != nil && a.mp2k.IsEngaged() {
		return 65536
	}
	return a.BIAS.GetSampleRate()
}

// ReadSamples drains up to `len(dst)/4` stereo frames into dst (int16 LE
// stereo interleaved, byte form) from the host-rate sink ring. 1:1 drain
// — no rate conversion at the consumer (matches upstream's
// AudioCallback). Missing samples are zero-filled. Called from the audio
// device thread.
func (a *APU) ReadSamples(dst []byte) int {
	if a.sink == nil {
		// No sink wired (test harness, headless). Fill with silence.
		for i := range dst {
			dst[i] = 0
		}
		return len(dst)
	}
	a.sampleMu.Lock()
	defer a.sampleMu.Unlock()
	const maxAmp = 0.999
	clamp := func(v float32) float32 {
		v *= a.Volume
		if v > maxAmp {
			return maxAmp
		}
		if v < -maxAmp {
			return -maxAmp
		}
		return v
	}
	n := 0
	for i := 0; i+3 < len(dst); i += 4 {
		r, l, ok := a.sink.Pop()
		if !ok {
			dst[i+0] = 0
			dst[i+1] = 0
			dst[i+2] = 0
			dst[i+3] = 0
		} else {
			lv := int16(clamp(l) * 32767)
			rv := int16(clamp(r) * 32767)
			dst[i+0] = byte(lv)
			dst[i+1] = byte(lv >> 8)
			dst[i+2] = byte(rv)
			dst[i+3] = byte(rv >> 8)
		}
		n += 4
	}
	return n
}

// OnTimerOverflow ⇄ APU::OnTimerOverflow. Drains the matching FIFO into
// the pipe and latches a sample.
func (a *APU) OnTimerOverflow(timerID, times int) {
	if !a.SOUNDCNT.MasterEnable {
		return
	}
	for fifoID := range 2 {
		if a.SOUNDCNT.DMA[fifoID].TimerID != timerID {
			continue
		}
		fifo := &a.FIFO[fifoID]
		pipe := &a.fifoPipe[fifoID]

		if fifo.Count() <= 3 && dmaReq != nil {
			dmaReq.RequestFIFO(fifoID)
		}
		if pipe.Size == 0 && fifo.Count() > 0 {
			pipe.Word = fifo.ReadWord()
			pipe.Size = 4
		}
		sample := int8(pipe.Word & 0xFF)
		if pipe.Size > 0 {
			pipe.Word >>= 8
			pipe.Size--
		}
		a.latch[fifoID] = sample
		_ = times
	}
}

// ---------------------------------------------------------------------
// IO dispatch — full coverage of 0x04000060..0x040000A7 (PSG1-4, SOUNDCNT,
// SOUNDBIAS, FIFO_A/B, WAVE_RAM).
// ---------------------------------------------------------------------

// soundChanOffset maps an IO address to a (channel, offset-within-channel)
// for the PSG channel registers in 0x04000060..0x0400007F. The mapping
// matches the upstream io.cc per-address switch exactly — PSG1 occupies a
// contiguous block, but PSG2/3/4 have register gaps that read as 0.
//
// Returns psg = 0 when the address falls in one of those gaps.
func soundChanOffset(addr uint32) (psg, off int) {
	switch addr {
	// PSG1: sweep (0,1), len/env (2,3), freq/ctrl (4,5). Bytes 6/7 unused.
	case 0x04000060:
		return 1, 0
	case 0x04000061:
		return 1, 1
	case 0x04000062:
		return 1, 2
	case 0x04000063:
		return 1, 3
	case 0x04000064:
		return 1, 4
	case 0x04000065:
		return 1, 5
	// PSG2: no sweep, len/env at SOUND2CNT_L (0x68/0x69), freq/ctrl at
	// SOUND2CNT_H (0x6C/0x6D). Bytes 0x6A/0x6B and 0x6E/0x6F are gaps.
	case 0x04000068:
		return 2, 2
	case 0x04000069:
		return 2, 3
	case 0x0400006C:
		return 2, 4
	case 0x0400006D:
		return 2, 5
	// PSG3: stop/wave (0,1), length/volume (2,3), freq/ctrl (4,5). 0x76/77 unused.
	case 0x04000070:
		return 3, 0
	case 0x04000071:
		return 3, 1
	case 0x04000072:
		return 3, 2
	case 0x04000073:
		return 3, 3
	case 0x04000074:
		return 3, 4
	case 0x04000075:
		return 3, 5
	// PSG4: length/env at SOUND4CNT_L (0x78/0x79), freq/ctrl at
	// SOUND4CNT_H (0x7C/0x7D). 0x7A/0x7B/0x7E/0x7F are gaps.
	case 0x04000078:
		return 4, 0
	case 0x04000079:
		return 4, 1
	case 0x0400007C:
		return 4, 4
	case 0x0400007D:
		return 4, 5
	}
	return 0, 0
}

func (a *APU) IORead8(addr uint32) (uint8, bool) {
	switch addr {
	case 0x04000080:
		return a.SOUNDCNT.Read(0), true
	case 0x04000081:
		return a.SOUNDCNT.Read(1), true
	case 0x04000082:
		return a.SOUNDCNT.Read(2), true
	case 0x04000083:
		return a.SOUNDCNT.Read(3), true
	case 0x04000084:
		v := a.SOUNDCNT.Read(4)
		if a.PSG1.IsEnabled() {
			v |= 1
		}
		if a.PSG2.IsEnabled() {
			v |= 2
		}
		if a.PSG3.IsEnabled() {
			v |= 4
		}
		if a.PSG4.IsEnabled() {
			v |= 8
		}
		return v, true
	case 0x04000088:
		return a.BIAS.Read(0), true
	case 0x04000089:
		return a.BIAS.Read(1), true
	}
	if addr >= 0x04000090 && addr <= 0x0400009F {
		return a.PSG3.ReadSample(int(addr - 0x04000090)), true
	}
	if psg, off := soundChanOffset(addr); psg != 0 {
		switch psg {
		case 1:
			return a.PSG1.Read(off), true
		case 2:
			return a.PSG2.Read(off), true
		case 3:
			return a.PSG3.Read(off), true
		case 4:
			return a.PSG4.Read(off), true
		}
	}
	if addr >= 0x04000060 && addr < 0x040000A8 {
		return 0, true
	}
	return 0, false
}

func (a *APU) IOWrite8(addr uint32, v uint8) bool {
	// SOUNDCNT_H (offsets 2,3) + SOUNDCNT_X (offset 4) + BIAS are
	// written regardless of master_enable. SOUNDCNT_L (offsets 0,1)
	// is gated. Same split upstream uses in io.cc.
	apuEnable := a.SOUNDCNT.MasterEnable
	switch addr {
	case 0x04000080:
		if apuEnable {
			a.SOUNDCNT.Write(0, v)
		}
		return true
	case 0x04000081:
		if apuEnable {
			a.SOUNDCNT.Write(1, v)
		}
		return true
	case 0x04000082:
		a.SOUNDCNT.Write(2, v)
		return true
	case 0x04000083:
		a.SOUNDCNT.Write(3, v)
		return true
	case 0x04000084:
		a.SOUNDCNT.Write(4, v)
		return true
	case 0x04000088:
		a.BIAS.Write(0, v)
		return true
	case 0x04000089:
		a.BIAS.Write(1, v)
		return true
	}
	// WAVE_RAM ⇄ PSG3 sample bank — not gated upstream (the wave RAM
	// is accessible even when audio is off).
	if addr >= 0x04000090 && addr <= 0x0400009F {
		a.PSG3.WriteSample(int(addr-0x04000090), v)
		return true
	}
	// FIFO writes — gated on apu_enable.
	if addr >= 0x040000A0 && addr <= 0x040000A3 {
		if apuEnable {
			a.FIFO[0].WriteByte(addr-0x040000A0, v)
		}
		return true
	}
	if addr >= 0x040000A4 && addr <= 0x040000A7 {
		if apuEnable {
			a.FIFO[1].WriteByte(addr-0x040000A4, v)
		}
		return true
	}
	// PSG channel registers — gated on apu_enable.
	if psg, off := soundChanOffset(addr); psg != 0 {
		if !apuEnable {
			return true
		}
		switch psg {
		case 1:
			a.PSG1.Write(off, v)
		case 2:
			a.PSG2.Write(off, v)
		case 3:
			a.PSG3.Write(off, v)
		case 4:
			a.PSG4.Write(off, v)
		}
		return true
	}
	if addr >= 0x04000060 && addr < 0x040000A8 {
		return true
	}
	return false
}

func (a *APU) IORead16(addr uint32) (uint16, bool) {
	lo, ok := a.IORead8(addr)
	if !ok {
		return 0, false
	}
	hi, _ := a.IORead8(addr + 1)
	return uint16(lo) | uint16(hi)<<8, true
}

// IOWrite16 routes FIFO half-word writes as single FIFO halfword entries
// (one entry of width 16) — decomposing into two bytes would push two
// FIFO entries instead of one. FIFO writes are gated by master_enable.
func (a *APU) IOWrite16(addr uint32, v uint16) bool {
	if addr >= 0x040000A0 && addr <= 0x040000A2 {
		if a.SOUNDCNT.MasterEnable {
			a.FIFO[0].WriteHalf(addr-0x040000A0, v)
		}
		return true
	}
	if addr >= 0x040000A4 && addr <= 0x040000A6 {
		if a.SOUNDCNT.MasterEnable {
			a.FIFO[1].WriteHalf(addr-0x040000A4, v)
		}
		return true
	}
	if !a.IOWrite8(addr, uint8(v)) {
		return false
	}
	a.IOWrite8(addr+1, uint8(v>>8))
	return true
}

func (a *APU) IORead32(addr uint32) (uint32, bool) {
	lo, ok := a.IORead16(addr)
	if !ok {
		return 0, false
	}
	hi, _ := a.IORead16(addr + 2)
	return uint32(lo) | uint32(hi)<<16, true
}

// IOWrite32 routes FIFO word writes as a single FIFO entry — this is the
// common case (`str rN, [FIFO_A]`) and the path that needs to be atomic.
func (a *APU) IOWrite32(addr uint32, v uint32) bool {
	if addr == 0x040000A0 {
		if a.SOUNDCNT.MasterEnable {
			a.FIFO[0].WriteWord(v)
		}
		return true
	}
	if addr == 0x040000A4 {
		if a.SOUNDCNT.MasterEnable {
			a.FIFO[1].WriteWord(v)
		}
		return true
	}
	if !a.IOWrite16(addr, uint16(v)) {
		return false
	}
	a.IOWrite16(addr+2, uint16(v>>16))
	return true
}

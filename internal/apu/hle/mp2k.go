// Package hle — port of src/nba/src/hw/apu/hle/mp2k.{hh,cc}.
//
// High-Level Emulation of the Nintendo "M4A" / MP2K sound engine: instead
// of running the cart's MP2K interpreter on the emulated CPU, we read the
// engine's working state (SoundInfo) straight out of GBA memory and
// produce PCM samples directly. Used by most retail games.
package hle

import (
	"encoding/binary"
	"math"
)

const (
	MP2KMaxSoundChannels = 12
	MP2KSampleRate       = 65536
	MP2KSamplesPerFrame  = MP2KSampleRate/60 + 1
	MP2KTotalFrameCount  = 7
)

// Sound channel status bits ⇄ MP2K::SoundChannelStatus.
const (
	channelStart      = 0x80
	channelStop       = 0x40
	channelLoop       = 0x10
	channelEcho       = 0x04
	channelEnvMask    = 0x03
	channelEnvAttack  = 0x03
	channelEnvDecay   = 0x02
	channelEnvSustain = 0x01
	channelEnvRelease = 0x00
	channelOn         = channelStart | channelStop | channelEcho | channelEnvMask
)

// SoundChannel ⇄ MP2K::SoundChannel (76 bytes).
type SoundChannel struct {
	Status          uint8
	Type            uint8
	VolumeR         uint8
	VolumeL         uint8
	EnvelopeAttack  uint8
	EnvelopeDecay   uint8
	EnvelopeSustain uint8
	EnvelopeRelease uint8
	Unknown0        uint8
	EnvelopeVolume  uint8
	EnvelopeVolumeR uint8
	EnvelopeVolumeL uint8
	EchoVolume      uint8
	EchoLength      uint8
	Unknown1        [18]uint8
	Frequency       uint32
	WaveAddress     uint32
	Unknown2        [6]uint32
}

// Packed sizeof(SoundChannel) = 14 single-byte fields + 18-byte unknown1
// + 2 u32 (freq, wave_address) + 6 u32 (unknown2) = 64 bytes.
const soundChannelSize = 64

// Packed sizeof(SoundInfo) header (before channels[]): u32 magic +
// 4 u8 + 8 u8 (unknown0) + 2 s32 + 14 u32 = 80 bytes. Total = 80 + 12*64 = 848.
const soundInfoHeaderSize = 80
const soundInfoTotalSize = soundInfoHeaderSize + MP2KMaxSoundChannels*soundChannelSize

// SoundInfo ⇄ MP2K::SoundInfo.
type SoundInfo struct {
	Magic               uint32
	PCMDMACounter       uint8
	Reverb              uint8
	MaxChannels         uint8
	MasterVolume        uint8
	Unknown0            [8]uint8
	PCMSamplesPerVBlank int32
	PCMSampleRate       int32
	Unknown1            [14]uint32
	Channels            [MP2KMaxSoundChannels]SoundChannel
}

// WaveInfo ⇄ MP2K::Sampler::WaveInfo (16 bytes).
type WaveInfo struct {
	WaveType        uint16
	WaveStatus      uint16
	Frequency       uint32
	LoopPosition    uint32
	NumberOfSamples uint32
}

type sampler struct {
	compressed        bool
	shouldFetchSample bool
	currentPosition   uint32
	resamplePhase     float32
	sampleHistory     [4]float32
	waveInfo          WaveInfo
	waveData          []uint8
}

type envelope struct {
	volume  float32
	volumeL [2]float32
	volumeR [2]float32
}

// BusHostAddress ⇄ Bus::GetHostAddress — abstracts the bus dependency so
// the HLE engine doesn't import internal/bus directly.
type BusHostAddress interface {
	GetHostAddress(addr uint32, size int) []uint8
}

// MP2K ⇄ struct MP2K.
type MP2K struct {
	engaged         bool
	UseCubicFilter  bool
	ForceReverb     bool
	bus             BusHostAddress
	soundInfo       SoundInfo
	buffer          []float32
	currentFrame    int
	bufferReadIndex int
	samplers        [MP2KMaxSoundChannels]sampler
	envelopes       [MP2KMaxSoundChannels]envelope
}

// New constructs an MP2K engine bound to a bus.
func New(bus BusHostAddress) *MP2K {
	m := &MP2K{bus: bus}
	m.Reset()
	return m
}

func (m *MP2K) IsEngaged() bool { return m.engaged }

func (m *MP2K) Reset() {
	m.engaged = false
	m.currentFrame = 0
	m.bufferReadIndex = 0
	for i := range m.samplers {
		m.samplers[i] = sampler{shouldFetchSample: true}
	}
	for i := range m.envelopes {
		m.envelopes[i] = envelope{}
	}
}

func s8ToFloat(v int8) float32  { return float32(v) / 127.0 }
func u8ToFloat(v uint8) float32 { return float32(v) / 256.0 }

// SoundMainRAM ⇄ MP2K::SoundMainRAM. Called whenever the cart's MP2K
// interpreter would have run; takes a freshly-parsed SoundInfo and walks
// per-channel envelope state forward by one audio frame.
func (m *MP2K) SoundMainRAM(info SoundInfo) {
	if info.Magic != 0x68736D54 {
		return
	}
	if !m.engaged {
		if info.PCMSamplesPerVBlank == 0 {
			panic("MP2K: samples per V-blank must not be zero")
		}
		m.buffer = make([]float32, MP2KSamplesPerFrame*MP2KTotalFrameCount*2)
		m.engaged = true
	}

	maxChannels := min(int(info.MaxChannels), MP2KMaxSoundChannels)
	m.soundInfo = info

	for i := 0; i < maxChannels; i++ {
		channel := &m.soundInfo.Channels[i]
		if channel.Status&channelOn == 0 {
			continue
		}

		s := &m.samplers[i]
		envelopeVolume := uint32(channel.EnvelopeVolume)
		envelopePhase := channel.Status & channelEnvMask

		var hqEnv [2]float32
		hqEnv[0] = m.envelopes[i].volume

		switch {
		case channel.Status&channelStart != 0:
			if channel.Status&channelStop != 0 {
				channel.Status = 0
				continue
			}
			envelopeVolume = uint32(channel.EnvelopeAttack)
			if envelopeVolume == 0xFF {
				channel.Status = channelEnvDecay
			} else {
				channel.Status = channelEnvAttack
			}
			hqEnv[0] = u8ToFloat(channel.EnvelopeAttack)

			waveBytes := m.bus.GetHostAddress(channel.WaveAddress, 16)
			if waveBytes == nil {
				channel.Status = 0
				continue
			}
			*s = sampler{shouldFetchSample: true}
			s.waveInfo = WaveInfo{
				WaveType:        binary.LittleEndian.Uint16(waveBytes[0:]),
				WaveStatus:      binary.LittleEndian.Uint16(waveBytes[2:]),
				Frequency:       binary.LittleEndian.Uint32(waveBytes[4:]),
				LoopPosition:    binary.LittleEndian.Uint32(waveBytes[8:]),
				NumberOfSamples: binary.LittleEndian.Uint32(waveBytes[12:]),
			}
			if s.waveInfo.WaveStatus&0xC000 != 0 {
				channel.Status |= channelLoop
			}
		case channel.Status&channelEcho != 0:
			if channel.EchoLength == 0 {
				channel.Status = 0
				continue
			}
			channel.EchoLength--
		case channel.Status&channelStop != 0:
			envelopeVolume = (envelopeVolume * uint32(channel.EnvelopeRelease)) >> 8
			hqEnv[0] *= u8ToFloat(channel.EnvelopeRelease)
			if envelopeVolume <= uint32(channel.EchoVolume) {
				if channel.EchoVolume == 0 {
					channel.Status = 0
					continue
				}
				channel.Status |= channelEcho
				envelopeVolume = uint32(channel.EchoVolume)
				hqEnv[0] = u8ToFloat(channel.EchoVolume)
			}
		case envelopePhase == channelEnvAttack:
			envelopeVolume += uint32(channel.EnvelopeAttack)
			hqEnv[0] += u8ToFloat(channel.EnvelopeAttack)
			if hqEnv[0] > 1.0 {
				hqEnv[0] = 1.0
			}
			if envelopeVolume > 0xFE {
				channel.Status = (channel.Status &^ channelEnvMask) | channelEnvDecay
				envelopeVolume = 0xFF
			}
		case envelopePhase == channelEnvDecay:
			envelopeVolume = (envelopeVolume * uint32(channel.EnvelopeDecay)) >> 8
			hqEnv[0] *= u8ToFloat(channel.EnvelopeDecay)
			sustain := channel.EnvelopeSustain
			if envelopeVolume <= uint32(sustain) {
				if sustain == 0 && channel.EchoVolume == 0 {
					channel.Status = 0
					continue
				}
				channel.Status = (channel.Status &^ channelEnvMask) | channelEnvSustain
				envelopeVolume = uint32(sustain)
				hqEnv[0] = u8ToFloat(sustain)
			}
		}

		channel.EnvelopeVolume = uint8(envelopeVolume)
		envelopeVolume = (envelopeVolume * (uint32(m.soundInfo.MasterVolume) + 1)) >> 4
		channel.EnvelopeVolumeR = uint8((envelopeVolume * uint32(channel.VolumeR)) >> 8)
		channel.EnvelopeVolumeL = uint8((envelopeVolume * uint32(channel.VolumeL)) >> 8)

		switch {
		case channel.Status&channelStop != 0:
			if (envelopeVolume*uint32(channel.EnvelopeRelease))>>8 <= uint32(channel.EchoVolume) {
				hqEnv[1] = u8ToFloat(channel.EchoVolume)
			} else {
				hqEnv[1] = hqEnv[0] * u8ToFloat(channel.EnvelopeRelease)
			}
		case channel.Status&channelEnvMask == channelEnvAttack:
			hqEnv[1] = hqEnv[0] + u8ToFloat(channel.EnvelopeAttack)
			if hqEnv[1] > 1.0 {
				hqEnv[1] = 1.0
			}
		case channel.Status&channelEnvMask == channelEnvDecay:
			if (envelopeVolume*uint32(channel.EnvelopeDecay))>>8 <= uint32(channel.EnvelopeSustain) {
				hqEnv[1] = u8ToFloat(channel.EnvelopeSustain)
			} else {
				hqEnv[1] = hqEnv[0] * u8ToFloat(channel.EnvelopeDecay)
			}
		default:
			hqEnv[1] = hqEnv[0]
		}

		hqMaster := float32(info.MasterVolume+1) / 16.0
		hqVolR := hqMaster * u8ToFloat(channel.VolumeR)
		hqVolL := hqMaster * u8ToFloat(channel.VolumeL)

		m.envelopes[i].volume = hqEnv[0]
		for j := range 2 {
			m.envelopes[i].volumeR[j] = hqEnv[j] * hqVolR
			m.envelopes[i].volumeL[j] = hqEnv[j] * hqVolL
		}
	}
}

// RenderFrame ⇄ MP2K::RenderFrame. Mixes one audio frame into m.buffer.
func (m *MP2K) RenderFrame() {
	differentialLUT := [16]float32{
		s8ToFloat(0x00), s8ToFloat(0x01), s8ToFloat(0x04), s8ToFloat(0x09),
		s8ToFloat(0x10), s8ToFloat(0x19), s8ToFloat(0x24), s8ToFloat(0x31),
		s8ToFloat(-0x40), s8ToFloat(-0x31), s8ToFloat(-0x24), s8ToFloat(-0x19),
		s8ToFloat(-0x10), s8ToFloat(-0x09), s8ToFloat(-0x04), s8ToFloat(-0x01),
	}

	m.currentFrame = (m.currentFrame + 1) % MP2KTotalFrameCount

	reverbStrength := m.soundInfo.Reverb
	if m.ForceReverb {
		if reverbStrength < 48 {
			reverbStrength = 48
		}
	}
	maxChannels := min(int(m.soundInfo.MaxChannels), MP2KMaxSoundChannels)
	dest := m.buffer[m.currentFrame*MP2KSamplesPerFrame*2:]
	dest = dest[:MP2KSamplesPerFrame*2]

	if reverbStrength > 0 {
		m.renderReverb(dest, reverbStrength)
	} else {
		for i := range dest {
			dest[i] = 0
		}
	}

	for i := 0; i < maxChannels; i++ {
		channel := &m.soundInfo.Channels[i]
		s := &m.samplers[i]
		env := &m.envelopes[i]
		if channel.Status&channelOn == 0 {
			continue
		}

		var angularStep float32
		if channel.Type&8 != 0 {
			angularStep = float32(m.soundInfo.PCMSampleRate) / float32(MP2KSampleRate)
		} else {
			angularStep = float32(channel.Frequency) / float32(MP2KSampleRate)
		}

		compressed := (channel.Type & 32) != 0
		waveInfo := s.waveInfo

		if s.compressed != compressed || s.waveData == nil {
			waveSize := waveInfo.NumberOfSamples
			if compressed {
				waveSize = (waveSize*33 + 63) / 64
			}
			if waveSize == 0 {
				// Bogus channel — missing sample data. Silence + disable.
				channel.Status = 0
				continue
			}
			waveDataBegin := channel.WaveAddress + 16 // sizeof(WaveInfo)
			s.waveData = m.bus.GetHostAddress(waveDataBegin, int(waveSize))
			if s.waveData == nil || len(s.waveData) == 0 {
				channel.Status = 0
				continue
			}
			s.compressed = compressed
		}
		waveData := s.waveData
		if len(waveData) == 0 {
			channel.Status = 0
			continue
		}

		for j := range MP2KSamplesPerFrame {
			t := float32(j) / float32(MP2KSamplesPerFrame)
			volumeL := env.volumeL[0]*(1-t) + env.volumeL[1]*t
			volumeR := env.volumeR[0]*(1-t) + env.volumeR[1]*t

			if s.shouldFetchSample {
				var sample float32
				if compressed {
					blockOffset := s.currentPosition & 63
					blockAddress := (s.currentPosition >> 6) * 33
					address := blockAddress + (blockOffset >> 1) + 1
					if int(address) >= len(waveData) {
						// Sample buffer underflow — disable the channel
						// rather than crashing.
						channel.Status = 0
						break
					}
					if blockOffset == 0 {
						sample = s8ToFloat(int8(waveData[blockAddress]))
					} else {
						sample = s.sampleHistory[0]
					}
					lutIndex := waveData[address]
					if blockOffset&1 != 0 {
						lutIndex &= 15
					} else {
						lutIndex >>= 4
					}
					sample += differentialLUT[lutIndex]
				} else {
					if int(s.currentPosition) >= len(waveData) {
						channel.Status = 0
						break
					}
					sample = s8ToFloat(int8(waveData[s.currentPosition]))
				}

				if m.UseCubicFilter {
					s.sampleHistory[3] = s.sampleHistory[2]
					s.sampleHistory[2] = s.sampleHistory[1]
				}
				s.sampleHistory[1] = s.sampleHistory[0]
				s.sampleHistory[0] = sample
				s.shouldFetchSample = false
			}

			var out float32
			mu := s.resamplePhase
			if m.UseCubicFilter {
				mu2 := mu * mu
				a0 := s.sampleHistory[0] - s.sampleHistory[1] - s.sampleHistory[3] + s.sampleHistory[2]
				a1 := s.sampleHistory[3] - s.sampleHistory[2] - a0
				a2 := s.sampleHistory[1] - s.sampleHistory[3]
				a3 := s.sampleHistory[2]
				out = a0*mu*mu2 + a1*mu2 + a2*mu + a3
			} else {
				out = s.sampleHistory[0]*mu + s.sampleHistory[1]*(1.0-mu)
			}
			dest[j*2+0] += out * volumeR
			dest[j*2+1] += out * volumeL

			s.resamplePhase += angularStep
			if s.resamplePhase >= 1 {
				n := int(math.Floor(float64(s.resamplePhase)))
				s.resamplePhase -= float32(n)
				s.currentPosition += uint32(n)
				s.shouldFetchSample = true
				if s.currentPosition >= waveInfo.NumberOfSamples {
					if channel.Status&channelLoop != 0 {
						s.currentPosition = waveInfo.LoopPosition + uint32(n) - 1
					} else {
						s.currentPosition = waveInfo.NumberOfSamples
						s.shouldFetchSample = false
					}
				}
			}
		}
	}
}

func (m *MP2K) renderReverb(dest []float32, strength uint8) {
	const earlyCoef = 0.0015
	lateCoefs := [3][2]float32{
		{1.0, 0.1},
		{0.6, 0.25},
		{0.35, 0.35},
	}
	var sum float32
	for _, p := range lateCoefs {
		sum += p[0] + p[1]
	}
	normalizeCoef := 1.0 / sum

	earlyBuffer := m.buffer[((m.currentFrame+MP2KTotalFrameCount-1)%MP2KTotalFrameCount)*MP2KSamplesPerFrame*2:][:MP2KSamplesPerFrame*2]
	lateBuffers := [3][]float32{
		m.buffer[((m.currentFrame+2)%MP2KTotalFrameCount)*MP2KSamplesPerFrame*2:][:MP2KSamplesPerFrame*2],
		m.buffer[((m.currentFrame+1)%MP2KTotalFrameCount)*MP2KSamplesPerFrame*2:][:MP2KSamplesPerFrame*2],
		dest,
	}
	factor := float32(strength) / 128.0

	for l := 0; l < MP2KSamplesPerFrame*2; l += 2 {
		r := l + 1
		earlyL := earlyBuffer[l] * earlyCoef
		earlyR := earlyBuffer[r] * earlyCoef
		var lateL, lateR float32
		for j := range 3 {
			sL := lateBuffers[j][l]
			sR := lateBuffers[j][r]
			lateL += sL*lateCoefs[j][0] + sR*lateCoefs[j][1]
			lateR += sL*lateCoefs[j][1] + sR*lateCoefs[j][0]
		}
		lateL *= normalizeCoef
		lateR *= normalizeCoef
		dest[l] = (earlyL + lateL) * factor
		dest[r] = (earlyR + lateR) * factor
	}
}

// ReadSample ⇄ MP2K::ReadSample — returns the next interleaved stereo
// sample pair (R, L). RenderFrame is invoked automatically when the
// current buffered frame is exhausted.
func (m *MP2K) ReadSample() (float32, float32) {
	if m.bufferReadIndex == 0 {
		m.RenderFrame()
	}
	idx := (m.currentFrame*MP2KSamplesPerFrame + m.bufferReadIndex) * 2
	r, l := m.buffer[idx], m.buffer[idx+1]
	m.bufferReadIndex++
	if m.bufferReadIndex == MP2KSamplesPerFrame {
		m.bufferReadIndex = 0
	}
	return r, l
}

// serialization.go ⇄ src/nba/src/hw/apu/serialization.cc
package apu

import "github.com/mnmlyw/nanogoadvance/internal/savestate"

func (b *BaseChannel) loadPSG(s *savestate.PSGBase) {
	b.enabled = s.Enabled
	b.step = int(s.Step)
	b.Length.Enabled = s.Length.Enabled
	b.Length.Length = int(s.Length.Counter)
	b.Envelope.Active = s.Envelope.Active
	b.Envelope.Direction = int(s.Envelope.Direction)
	b.Envelope.InitialVolume = int(s.Envelope.InitialVolume)
	b.Envelope.CurrentVolume = int(s.Envelope.CurrentVolume)
	b.Envelope.Divider = int(s.Envelope.Divider)
	b.Envelope.step = int(s.Envelope.Step)
	b.Sweep.Active = s.Sweep.Active
	b.Sweep.Direction = int(s.Sweep.Direction)
	b.Sweep.CurrentFreq = int(s.Sweep.CurrentFreq)
	b.Sweep.ShadowFreq = int(s.Sweep.ShadowFreq)
	b.Sweep.Divider = int(s.Sweep.Divider)
	b.Sweep.Shift = int(s.Sweep.Shift)
	b.Sweep.step = int(s.Sweep.Step)
}

func (b *BaseChannel) copyPSG(s *savestate.PSGBase) {
	s.Enabled = b.enabled
	s.Step = uint8(b.step)
	s.Length.Enabled = b.Length.Enabled
	s.Length.Counter = uint8(b.Length.Length)
	s.Envelope.Active = b.Envelope.Active
	s.Envelope.Direction = uint8(b.Envelope.Direction)
	s.Envelope.InitialVolume = uint8(b.Envelope.InitialVolume)
	s.Envelope.CurrentVolume = uint8(b.Envelope.CurrentVolume)
	s.Envelope.Divider = uint8(b.Envelope.Divider)
	s.Envelope.Step = uint8(b.Envelope.step)
	s.Sweep.Active = b.Sweep.Active
	s.Sweep.Direction = uint8(b.Sweep.Direction)
	s.Sweep.CurrentFreq = uint16(b.Sweep.CurrentFreq)
	s.Sweep.ShadowFreq = uint16(b.Sweep.ShadowFreq)
	s.Sweep.Divider = uint8(b.Sweep.Divider)
	s.Sweep.Shift = uint8(b.Sweep.Shift)
	s.Sweep.Step = uint8(b.Sweep.step)
}

func (q *QuadChannel) LoadState(s *savestate.QuadChannelState) {
	q.loadPSG(&s.PSGBase)
	q.dacEnable = s.DACEnable
	q.phase = int(s.Phase)
	q.waveDuty = int(s.WaveDuty)
	q.Sample = s.Sample
	q.eventID = s.EventUID
	q.hasEvent = s.EventUID != 0
}

func (q *QuadChannel) CopyState(s *savestate.QuadChannelState) {
	q.copyPSG(&s.PSGBase)
	s.DACEnable = q.dacEnable
	s.Phase = uint8(q.phase)
	s.WaveDuty = uint8(q.waveDuty)
	s.Sample = q.Sample
	s.EventUID = uint64(q.eventID)
}

func (w *WaveChannel) LoadState(s *savestate.WaveChannelState) {
	w.loadPSG(&s.PSGBase)
	w.playing = s.Playing
	w.forceVolume = s.ForceVolume
	w.phase = int(s.Phase)
	w.volume = int(s.Volume)
	w.frequency = int(s.Frequency)
	w.dimension = int(s.Dimension)
	w.waveBank = int(s.WaveBank)
	w.WaveRAM = s.WaveRAM
	w.eventID = s.EventUID
	w.hasEvent = s.EventUID != 0
}

func (w *WaveChannel) CopyState(s *savestate.WaveChannelState) {
	w.copyPSG(&s.PSGBase)
	s.Playing = w.playing
	s.ForceVolume = w.forceVolume
	s.Phase = uint8(w.phase)
	s.Volume = uint8(w.volume)
	s.Frequency = uint16(w.frequency)
	s.Dimension = uint8(w.dimension)
	s.WaveBank = uint8(w.waveBank)
	s.WaveRAM = w.WaveRAM
	s.EventUID = uint64(w.eventID)
}

func (n *NoiseChannel) LoadState(s *savestate.NoiseChannelState) {
	n.loadPSG(&s.PSGBase)
	n.dacEnable = s.DACEnable
	n.frequencyShift = int(s.FrequencyShift)
	n.frequencyRatio = int(s.FrequencyRatio)
	n.width = int(s.Width)
	n.eventID = s.EventUID
	n.hasEvent = s.EventUID != 0
}

func (n *NoiseChannel) CopyState(s *savestate.NoiseChannelState) {
	n.copyPSG(&s.PSGBase)
	s.DACEnable = n.dacEnable
	s.FrequencyShift = uint8(n.frequencyShift)
	s.FrequencyRatio = uint8(n.frequencyRatio)
	s.Width = uint8(n.width)
	s.EventUID = uint64(n.eventID)
}

// LoadState/CopyState ⇄ FIFO::{LoadState,CopyState} (channel/fifo.hh).
// The serialised form stores entries in pop order starting at index 0;
// rd_ptr is set to 0 and wr_ptr to count % length on restore.
func (f *WordFIFO) LoadState(s *savestate.FIFOState) {
	for i := 0; i < fifoLen; i++ {
		f.data[i] = s.Data[i]
	}
	f.rdPtr = 0
	f.count = int(s.Count)
	f.wrPtr = f.count % fifoLen
}

func (f *WordFIFO) CopyState(s *savestate.FIFOState) {
	for i := 0; i < fifoLen; i++ {
		s.Data[i] = f.data[(f.rdPtr+i)%fifoLen]
	}
	s.Count = uint8(f.count)
}

func (a *APU) LoadState(s *savestate.SaveState) {
	a.SOUNDCNT.WriteWord(s.APU.IO.SOUNDCNT)
	a.BIAS.WriteHalf(s.APU.IO.SOUNDBIAS)

	a.PSG1.LoadState(&s.APU.IO.Quad[0])
	a.PSG2.LoadState(&s.APU.IO.Quad[1])
	a.PSG3.LoadState(&s.APU.IO.Wave)
	a.PSG4.LoadState(&s.APU.IO.Noise)

	for i := 0; i < 2; i++ {
		a.FIFO[i].LoadState(&s.APU.FIFO[i])
		a.fifoPipe[i].Word = s.APU.FIFO[i].Pipe.Word
		a.fifoPipe[i].Size = int(s.APU.FIFO[i].Pipe.Size)
	}
	a.ResolutionOld = int(s.APU.ResolutionOld)
}

func (a *APU) CopyState(s *savestate.SaveState) {
	s.APU.IO.SOUNDCNT = a.SOUNDCNT.ReadWord()
	s.APU.IO.SOUNDBIAS = a.BIAS.ReadHalf()

	a.PSG1.CopyState(&s.APU.IO.Quad[0])
	a.PSG2.CopyState(&s.APU.IO.Quad[1])
	a.PSG3.CopyState(&s.APU.IO.Wave)
	a.PSG4.CopyState(&s.APU.IO.Noise)

	for i := 0; i < 2; i++ {
		a.FIFO[i].CopyState(&s.APU.FIFO[i])
		s.APU.FIFO[i].Pipe.Word = a.fifoPipe[i].Word
		s.APU.FIFO[i].Pipe.Size = uint8(a.fifoPipe[i].Size)
	}
	s.APU.ResolutionOld = uint8(a.ResolutionOld)
}

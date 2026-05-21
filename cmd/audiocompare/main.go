// audiocompare — port-side audio capture tool. Mirrors
// tools/nba-headless --audio so we can run both emulators against the
// same ROM/BIOS/frames and diff the resulting WAVs sample-by-sample.
//
// Usage:
//
//	audiocompare --rom game.gba [--bios bios.bin] --audio out.wav
//	             [--frames N] [--audio-rate HZ] [--mp2k-hle]
//
// The output WAV format (int16 stereo little-endian, configurable rate)
// matches nba-headless's RecordingAudio writer so the two files are
// directly comparable via any byte/sample-diff tool.
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/mnmlyw/nanogoadvance/internal/apu"
	"github.com/mnmlyw/nanogoadvance/internal/core"
	"github.com/mnmlyw/nanogoadvance/internal/dsp"
)

// recordingSink — captures every host-rate stereo sample the resampler
// writes. Implements both dsp.WriteStream (for the resampler) and
// apu.StereoSink (Pop/Available/Peek for the APU's ReadSamples path,
// though we won't actually call ReadSamples here).
type recordingSink struct {
	samples []dsp.StereoSample[float32]
}

func (r *recordingSink) Write(s dsp.StereoSample[float32]) {
	r.samples = append(r.samples, s)
}

// Stubs to satisfy apu.StereoSink — never called in this tool but the
// interface needs them.
func (r *recordingSink) Pop() (right, left float32, ok bool)     { return 0, 0, false }
func (r *recordingSink) Available() int                          { return 0 }
func (r *recordingSink) Peek(_ int) (right, left float32)        { return 0, 0 }

type apuResamplerAdapter struct {
	inner interface {
		Write(s dsp.StereoSample[float32])
		SetSampleRates(in, out float32)
	}
}

func (a apuResamplerAdapter) Write(l, r float32)               { a.inner.Write(dsp.StereoSample[float32]{Left: l, Right: r}) }
func (a apuResamplerAdapter) SetSampleRates(in, out float32)   { a.inner.SetSampleRates(in, out) }

func main() {
	romPath := flag.String("rom", "", "ROM path")
	biosPath := flag.String("bios", "", "BIOS path (empty → skip-BIOS init)")
	audioPath := flag.String("audio", "", "output WAV path")
	frames := flag.Int("frames", 600, "number of frames to run")
	audioRate := flag.Int("audio-rate", 32768, "host sample rate (Hz)")
	mp2kHLE := flag.Bool("mp2k-hle", false, "enable MP2K audio HLE")
	flag.Parse()
	if *romPath == "" || *audioPath == "" {
		fmt.Fprintln(os.Stderr, "usage: audiocompare --rom PATH --audio OUT.wav [--bios PATH] [--frames N] [--audio-rate HZ] [--mp2k-hle]")
		os.Exit(2)
	}

	c := core.New()
	if *biosPath != "" {
		bios, err := os.ReadFile(*biosPath)
		if err != nil {
			log.Fatalf("bios: %v", err)
		}
		c.LoadBIOS(bios)
	}
	rom, err := os.ReadFile(*romPath)
	if err != nil {
		log.Fatalf("rom: %v", err)
	}
	c.LoadROM(rom)
	cfg := core.DefaultConfig()
	cfg.Audio.MP2KHLEEnable = *mp2kHLE
	c.ApplyConfig(cfg)
	c.Reset()

	// Wire the APU to a recording sink + cubic resampler (matches
	// upstream's default Interpolation::Cubic).
	sink := &recordingSink{}
	resampler := dsp.NewCubicResampler(sink)
	var sinkIface apu.StereoSink = sink
	c.APU.SetOutput(apuResamplerAdapter{inner: resampler}, sinkIface, *audioRate)
	c.APU.Volume = 1.0

	for f := 0; f < *frames; f++ {
		c.RunFrame()
	}

	// Convert float32 stereo samples to int16 PCM (same conversion as
	// the APU's ReadSamples path: clamp, multiply by 32767, round).
	const maxAmp float32 = 0.999
	clamp := func(v float32) float32 {
		if v > maxAmp {
			return maxAmp
		}
		if v < -maxAmp {
			return -maxAmp
		}
		return v
	}
	round := func(v float32) int16 {
		if v >= 0 {
			return int16(v + 0.5)
		}
		return int16(v - 0.5)
	}
	pcm := make([]int16, 0, len(sink.samples)*2)
	for _, s := range sink.samples {
		pcm = append(pcm, round(clamp(s.Left)*32767))
		pcm = append(pcm, round(clamp(s.Right)*32767))
	}

	if err := writeWAV(*audioPath, pcm, *audioRate); err != nil {
		log.Fatalf("write WAV: %v", err)
	}
	fmt.Printf("wrote %d stereo frames (%d samples) to %s at %d Hz\n",
		len(sink.samples), len(pcm), *audioPath, *audioRate)
}

// writeWAV writes a stereo 16-bit PCM WAV — same format as
// nba-headless's WriteWav.
func writeWAV(path string, pcm []int16, rate int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	const channels uint16 = 2
	const bits uint16 = 16
	dataBytes := uint32(len(pcm) * 2)
	byteRate := uint32(rate) * uint32(channels) * uint32(bits/8)
	blockAlign := channels * (bits / 8)
	w := func(v any) error { return binary.Write(f, binary.LittleEndian, v) }
	if _, err := f.Write([]byte("RIFF")); err != nil {
		return err
	}
	if err := w(uint32(36 + dataBytes)); err != nil {
		return err
	}
	if _, err := f.Write([]byte("WAVE")); err != nil {
		return err
	}
	if _, err := f.Write([]byte("fmt ")); err != nil {
		return err
	}
	if err := w(uint32(16)); err != nil {
		return err
	}
	if err := w(uint16(1)); err != nil {
		return err
	}
	if err := w(channels); err != nil {
		return err
	}
	if err := w(uint32(rate)); err != nil {
		return err
	}
	if err := w(byteRate); err != nil {
		return err
	}
	if err := w(blockAlign); err != nil {
		return err
	}
	if err := w(bits); err != nil {
		return err
	}
	if _, err := f.Write([]byte("data")); err != nil {
		return err
	}
	if err := w(dataBytes); err != nil {
		return err
	}
	return w(pcm)
}

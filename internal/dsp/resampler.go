// resampler.go ⇄ src/nba/include/nba/common/dsp/resampler.hh + the four
// resampler variants in resampler/{cosine,cubic,nearest,sinc}.hh.
//
// Go generics carry the templating from upstream; the float arithmetic is
// done in float32 because both inputs and outputs are float-valued stereo
// samples and we want the same precision as upstream.
package dsp

import "math"

// Sample is the constraint on the inner numeric type of a resampler's
// stereo channel: it must support float-style mixing.
type Sample interface {
	StereoSample[float32]
}

// resamplerBase ⇄ Resampler<T> in resampler.hh (data members + the shared
// SetSampleRates path).
type resamplerBase[T any] struct {
	output            WriteStream[T]
	resamplePhaseShift float32
}

// SetSampleRates ⇄ Resampler::SetSampleRates.
func (r *resamplerBase[T]) SetSampleRates(in, out float32) {
	r.resamplePhaseShift = in / out
}

// NearestResampler ⇄ NearestResampler<T>.
type NearestResampler[T any] struct {
	resamplerBase[T]
	resamplePhase float32
}

func NewNearestResampler[T any](output WriteStream[T]) *NearestResampler[T] {
	r := &NearestResampler[T]{}
	r.output = output
	r.resamplePhaseShift = 1
	return r
}

func (r *NearestResampler[T]) Write(input T) {
	for r.resamplePhase < 1.0 {
		r.output.Write(input)
		r.resamplePhase += r.resamplePhaseShift
	}
	r.resamplePhase -= 1.0
}

// CosineResampler ⇄ CosineResampler<T>. Operates on StereoSample[float32].
type CosineResampler struct {
	resamplerBase[StereoSample[float32]]
	previous      StereoSample[float32]
	resamplePhase float32
	lut           [cosineLUTSize]float32
}

const cosineLUTSize = 512

func NewCosineResampler(output WriteStream[StereoSample[float32]]) *CosineResampler {
	r := &CosineResampler{}
	r.output = output
	r.resamplePhaseShift = 1
	for i := 0; i < cosineLUTSize; i++ {
		r.lut[i] = float32((math.Cos(math.Pi*float64(i)/float64(cosineLUTSize-1)) + 1.0) * 0.5)
	}
	return r
}

func (r *CosineResampler) Write(input StereoSample[float32]) {
	for r.resamplePhase < 1.0 {
		index := r.resamplePhase * float32(cosineLUTSize-1)
		a0 := r.lut[int(index)]
		a1 := r.lut[int(index)+1]
		a := a0 + (a1-a0)*(index-float32(int(index)))

		out := r.previous.MulScalar(a).AddSample(input.MulScalar(1.0 - a))
		r.output.Write(out)

		r.resamplePhase += r.resamplePhaseShift
	}
	r.resamplePhase -= 1.0
	r.previous = input
}

// CubicResampler ⇄ CubicResampler<T>. Operates on StereoSample[float32].
type CubicResampler struct {
	resamplerBase[StereoSample[float32]]
	previous      [3]StereoSample[float32]
	resamplePhase float32
}

func NewCubicResampler(output WriteStream[StereoSample[float32]]) *CubicResampler {
	r := &CubicResampler{}
	r.output = output
	r.resamplePhaseShift = 1
	return r
}

func (r *CubicResampler) Write(input StereoSample[float32]) {
	for r.resamplePhase < 1.0 {
		mu := r.resamplePhase
		mu2 := mu * mu

		a0 := input.SubSample(r.previous[0]).SubSample(r.previous[2]).AddSample(r.previous[1])
		a1 := r.previous[2].SubSample(r.previous[1]).SubSample(a0)
		a2 := r.previous[0].SubSample(r.previous[2])
		a3 := r.previous[1]

		out := a0.MulScalar(mu * mu2).
			AddSample(a1.MulScalar(mu2)).
			AddSample(a2.MulScalar(mu)).
			AddSample(a3)
		r.output.Write(out)

		r.resamplePhase += r.resamplePhaseShift
	}
	r.resamplePhase -= 1.0
	r.previous[2] = r.previous[1]
	r.previous[1] = r.previous[0]
	r.previous[0] = input
}

// SincResampler ⇄ SincResampler<T, points>. Points is a runtime constant
// (we don't have C++ NTTPs). Operates on StereoSample[float32].
type SincResampler struct {
	resamplerBase[StereoSample[float32]]
	points        int
	lut           []float64
	resamplePhase float32
	taps          *RingBuffer[StereoSample[float32]]
}

const sincLUTResolution = 512

func NewSincResampler(output WriteStream[StereoSample[float32]], points int) *SincResampler {
	if points%4 != 0 {
		panic("SincResampler: points must be divisible by 4")
	}
	r := &SincResampler{points: points}
	r.output = output
	r.resamplePhaseShift = 1
	r.lut = make([]float64, points*sincLUTResolution)
	r.taps = NewRingBuffer[StereoSample[float32]](points, false)
	r.SetSampleRates(1, 1)
	for i := 0; i < points-1; i++ {
		r.taps.Write(StereoSample[float32]{})
	}
	return r
}

// SetSampleRates ⇄ SincResampler::SetSampleRates.
func (r *SincResampler) SetSampleRates(in, out float32) {
	r.resamplerBase.SetSampleRates(in, out)
	cutoff := 0.9
	if r.resamplePhaseShift > 1.0 {
		cutoff /= float64(r.resamplePhaseShift)
	}
	kernelSum := 0.0
	for n := 0; n < r.points; n++ {
		for m := 0; m < sincLUTResolution; m++ {
			t := float64(m) / float64(sincLUTResolution)
			x1 := math.Pi*(t-float64(n)+float64(r.points)/2.0) + 1e-6
			x2 := 2 * math.Pi * (float64(n) + t) / float64(r.points)
			sinc := math.Sin(cutoff*x1) / x1
			blackman := 0.42 - 0.49*math.Cos(x2) + 0.076*math.Cos(2*x2)
			r.lut[n*sincLUTResolution+m] = sinc * blackman
			kernelSum += sinc * blackman
		}
	}
	kernelSum /= sincLUTResolution
	for i := 0; i < r.points*sincLUTResolution; i++ {
		r.lut[i] /= kernelSum
	}
}

func (r *SincResampler) Write(input StereoSample[float32]) {
	r.taps.Write(input)
	for r.resamplePhase < 1.0 {
		var sample StereoSample[float32]
		x := int(r.resamplePhase * sincLUTResolution)
		for n := 0; n < r.points; n += 4 {
			s0 := r.taps.Peek(n + 0)
			s1 := r.taps.Peek(n + 1)
			s2 := r.taps.Peek(n + 2)
			s3 := r.taps.Peek(n + 3)
			c0 := float32(r.lut[x+0*sincLUTResolution])
			c1 := float32(r.lut[x+1*sincLUTResolution])
			c2 := float32(r.lut[x+2*sincLUTResolution])
			c3 := float32(r.lut[x+3*sincLUTResolution])
			sample = sample.AddSample(s0.MulScalar(c0))
			sample = sample.AddSample(s1.MulScalar(c1))
			sample = sample.AddSample(s2.MulScalar(c2))
			sample = sample.AddSample(s3.MulScalar(c3))
			x += 4 * sincLUTResolution
		}
		r.output.Write(sample)
		r.resamplePhase += r.resamplePhaseShift
	}
	r.taps.Read()
	r.resamplePhase -= 1.0
}

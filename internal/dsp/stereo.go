// stereo.go ⇄ src/nba/include/nba/common/dsp/stereo.hh
package dsp

// Numeric ⇄ the implicit numeric concept upstream's StereoSample<T> uses.
// Restrict it to the float/int types we actually serialize.
type Numeric interface {
	~float32 | ~float64 | ~int8 | ~int16 | ~int32 | ~int64
}

// StereoSample ⇄ StereoSample<T> in stereo.hh.
type StereoSample[T Numeric] struct {
	Left, Right T
}

// AddScalar ⇄ operator+(T scalar).
func (s StereoSample[T]) AddScalar(v T) StereoSample[T] {
	return StereoSample[T]{Left: s.Left + v, Right: s.Right + v}
}

// AddSample ⇄ operator+(StereoSample const&).
func (s StereoSample[T]) AddSample(o StereoSample[T]) StereoSample[T] {
	return StereoSample[T]{Left: s.Left + o.Left, Right: s.Right + o.Right}
}

// SubScalar ⇄ operator-(T scalar).
func (s StereoSample[T]) SubScalar(v T) StereoSample[T] {
	return StereoSample[T]{Left: s.Left - v, Right: s.Right - v}
}

// SubSample ⇄ operator-(StereoSample const&).
func (s StereoSample[T]) SubSample(o StereoSample[T]) StereoSample[T] {
	return StereoSample[T]{Left: s.Left - o.Left, Right: s.Right - o.Right}
}

// MulScalar ⇄ operator*(T scalar).
func (s StereoSample[T]) MulScalar(v T) StereoSample[T] {
	return StereoSample[T]{Left: s.Left * v, Right: s.Right * v}
}

// MulSample ⇄ operator*(StereoSample const&).
func (s StereoSample[T]) MulSample(o StereoSample[T]) StereoSample[T] {
	return StereoSample[T]{Left: s.Left * o.Left, Right: s.Right * o.Right}
}

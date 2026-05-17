// ring_buffer.go ⇄ src/nba/include/nba/common/dsp/ring_buffer.hh
package dsp

// RingBuffer ⇄ RingBuffer<T> in ring_buffer.hh.
type RingBuffer[T any] struct {
	data     []T
	rdPtr    int
	wrPtr    int
	length   int
	count    int
	blocking bool
}

// NewRingBuffer ⇄ RingBuffer<T>(length, blocking=false).
func NewRingBuffer[T any](length int, blocking bool) *RingBuffer[T] {
	r := &RingBuffer[T]{
		data:     make([]T, length),
		length:   length,
		blocking: blocking,
	}
	r.Reset()
	return r
}

func (r *RingBuffer[T]) Available() int { return r.count }

func (r *RingBuffer[T]) Reset() {
	r.rdPtr = 0
	r.wrPtr = 0
	r.count = 0
	var zero T
	for i := 0; i < r.length; i++ {
		r.data[i] = zero
	}
}

// Peek ⇄ Peek(offset).
func (r *RingBuffer[T]) Peek(offset int) T {
	return r.data[(r.rdPtr+offset)%r.length]
}

// Read ⇄ Read() — pops one element.
func (r *RingBuffer[T]) Read() T {
	value := r.data[r.rdPtr]
	if r.count > 0 {
		r.rdPtr = (r.rdPtr + 1) % r.length
		r.count--
	}
	return value
}

// Write ⇄ Write(value).
func (r *RingBuffer[T]) Write(value T) {
	if r.blocking && r.count == r.length {
		return
	}
	r.data[r.wrPtr] = value
	r.wrPtr = (r.wrPtr + 1) % r.length
	r.count++
}

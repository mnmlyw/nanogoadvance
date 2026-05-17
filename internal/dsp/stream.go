// stream.go ⇄ src/nba/include/nba/common/dsp/stream.hh
package dsp

// ReadStream ⇄ ReadStream<T> in stream.hh.
type ReadStream[T any] interface {
	Read() T
}

// WriteStream ⇄ WriteStream<T> in stream.hh.
type WriteStream[T any] interface {
	Write(value T)
}

// Stream ⇄ Stream<T> = ReadStream<T> + WriteStream<T>.
type Stream[T any] interface {
	ReadStream[T]
	WriteStream[T]
}

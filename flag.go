package msync

// A Flag is a level-triggered single-value buffer shared by a producer and a
// consumer. A producer calls Send to make a value available, and a consumer
// calls Ready to obtain a channel which delivers the most recently-sent value.
//
// Setting value on the flag does not block: Once a value is set, additional
// values are discarded until the buffered value is consumed.
//
// The Ready method returns a channel that delivers buffered values to the
// consumer. Once a value has been consumed, the buffer is empty and the
// producer can send another.
type Flag[T any] struct {
	ch chan T
}

// NewFlag constructs a new empty flag.
func NewFlag[T any]() *Flag[T] { return &Flag[T]{ch: make(chan T, 1)} }

// Set buffers or discards v, and reports whether v was buffered (true) or
// discarded (false). Set does not block.
func (f *Flag[T]) Set(v T) bool {
	select {
	case f.ch <- v:
		return true
	default:
		return false
	}
}

// Ready returns a channel that delivers a value when one is available.  Once a
// value is received, further reads on the channel will block until the flag is
// Set again.
func (f *Flag[T]) Ready() <-chan T { return f.ch }

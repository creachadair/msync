package msync

// A Relay is a level-triggered single-value buffer shared by a producer and a
// consumer. A producer calls [Relay.Set] to make a value available, and a
// consumer calls [Relay.Ready] to obtain a channel which delivers the most
// recently-sent value.
//
// Setting a value on the relay does not block: Once a value is set, additional
// values are discarded until the buffered value is consumed.
type Relay[T any] struct {
	ch chan T
}

// NewRelay constructs a new empty handoff.
func NewRelay[T any]() *Relay[T] { return &Relay[T]{ch: make(chan T, 1)} }

// Set buffers or discards v, and reports whether v was buffered (true) or
// discarded (false). Set does not block.
func (f *Relay[T]) Set(v T) bool {
	select {
	case f.ch <- v:
		return true
	default:
		return false
	}
}

// Ready returns a channel that delivers a value when one is available.
// Once a value is received, further reads on the channel will block until
// another value is set.
func (f *Relay[T]) Ready() <-chan T { return f.ch }

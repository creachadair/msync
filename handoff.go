package msync

// A Handoff is a level-triggered single-value buffer shared by a producer and
// a consumer. A producer calls Send to make a value available, and a consumer
// calls Ready to obtain a channel which delivers the most recently-sent value.
//
// Sending a value to the handoff does not block: Once a value is buffered,
// additional values are discarded until the buffered value is consumed.
//
// The Ready method returns a channel that delivers buffered values to the
// consumer. Once a value has been consumed, the buffer is empty and the
// producer can send another.
type Handoff[T any] struct {
	ch chan T
}

// NewHandoff constructs a new empty handoff.
func NewHandoff[T any]() *Handoff[T] { return &Handoff[T]{ch: make(chan T, 1)} }

// Send buffers or discards v, and reports whether v was successfully buffered
// for handoff (true) discarded (false). Send does not block.
func (h *Handoff[T]) Send(v T) bool {
	select {
	case h.ch <- v:
		return true
	default:
		return false
	}
}

// Ready returns a channel that delivers a value when a handoff is available.
func (h *Handoff[T]) Ready() <-chan T { return h.ch }

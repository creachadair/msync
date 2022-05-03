// Package msync defines some helpful types for managing concurrency.
package msync

import "sync"

// Trigger is an edge-triggered condition. It is analogous in effect to the
// standard sync.Cond type, but uses a channel for signaling so that the
// waiters can select on its completion.
//
// A zero value is ready for use but must not be copied after first use.
type Trigger struct {
	μ  sync.Mutex
	ch chan struct{}

	// The signal channel is lazily initialized by the first waiter.
}

// NewTrigger constructs a new unready Trigger.
func NewTrigger() *Trigger { return new(Trigger) }

// Signal wakes all pending waiters and resets the trigger.
func (t *Trigger) Signal() {
	t.μ.Lock()
	defer t.μ.Unlock()
	if t.ch == nil {
		// There are no goroutines waiting, so there is nothing to do.
		return
	}
	close(t.ch)
	t.ch = nil
}

// Ready returns a channel that is closed when t is signaled.
func (t *Trigger) Ready() <-chan struct{} {
	t.μ.Lock()
	defer t.μ.Unlock()
	if t.ch == nil {
		t.ch = make(chan struct{})
	}
	return t.ch
}

// Handoff is a singly-buffered level-triggered rendezvous channel. A waiter
// blocks on the Ready channel until a value is provided by Send. A value
// handed off persists until consumed.
type Handoff[T any] struct {
	ch chan T
}

// NewHandoff constructs a new empty handoff.
func NewHandoff[T any]() *Handoff[T] { return &Handoff[T]{ch: make(chan T, 1)} }

// Send provides a value to handoff.  Send does not block.  Once v is sent to
// h, subsequent calls to Send will be discarded until a receiver accepts the
// handoff of v.
func (h *Handoff[T]) Send(v T) {
	select {
	case h.ch <- v:
	default:
	}
}

// Ready returns a channel that delivers a value when a handoff is available.
func (h *Handoff[T]) Ready() <-chan T { return h.ch }

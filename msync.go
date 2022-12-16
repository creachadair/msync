// Package msync defines some helpful types for managing concurrency.
package msync

import (
	"context"
	"sync"
)

// Trigger is an edge-triggered condition shared by multiple goroutines.  It is
// analogous to the standard condition variable (sync.Cond) but uses a channel
// for signaling.
//
// To wait on the trigger, call Ready to obtain a channel. This channel will be
// closed the next time the Signal method is executed. A fresh channel is
// created after each call to Signal, and is shared among all calls to Ready
// that occur in the window between signals.
//
// A zero Trigger is ready for use but must not be copied after first use.
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

	// If the channel is nil, there are no goroutines waiting.  The next waiter
	// will update the channel (see Ready).
	if t.ch != nil {
		close(t.ch)
		t.ch = nil
	}
}

// Ready returns a channel that is closed when t is signaled.
func (t *Trigger) Ready() <-chan struct{} {
	t.μ.Lock()
	defer t.μ.Unlock()

	// The first waiter after construction or a signal lazily populates the
	// channel for the next period.
	if t.ch == nil {
		t.ch = make(chan struct{})
	}
	return t.ch
}

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

// A Value is a mutable container for a single value of type T that can be
// concurrently accessed by multiple goroutines. A zero Value is ready for use,
// but must not be copied after its first use.
type Value[T any] struct {
	x     T
	mu    sync.Mutex
	ready chan struct{}
}

// NewValue creates a new Value with the given initial value.
func NewValue[T any](init T) *Value[T] { return &Value[T]{x: init} }

// Set updates the value stored in v to newValue. Calling Set also wakes any
// goroutines that are blocked in the Wait method.
func (v *Value[T]) Set(newValue T) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.x = newValue
	if v.ready != nil {
		close(v.ready)
		v.ready = nil
	}
}

// Get returns the current value stored in v.
func (v *Value[T]) Get() T {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.x
}

// Wait blocks until v.Set is called, or until ctx ends, and returns the
// current value in v. The flag indicates whether Set was called (true) or ctx
// timed out (false).
//
// If v.Set is not called before ctx ends, Wait returns the value v held when
// Wait was called. Otherwise, Wait returns the value from one of the v.Set
// calls made during its execution.
//
// If multiple goroutines set v concurrently with a call to Wait, the Wait call
// will return the value from one of them, but not necessarily the first.
func (v *Value[T]) Wait(ctx context.Context) (T, bool) {
	v.mu.Lock()
	if v.ready == nil {
		v.ready = make(chan struct{})
	}
	old, ready := v.x, v.ready
	v.mu.Unlock()
	for {
		select {
		case <-ctx.Done():
			return old, false
		case <-ready:
			v.mu.Lock()
			defer v.mu.Unlock()
			return v.x, true
		}
	}
}

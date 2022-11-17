// Package msync defines some helpful types for managing concurrency.
package msync

import (
	"context"
	"sync"
)

// Trigger is an edge-triggered condition shared by multiple waiting
// goroutines.  It is analogous in effect to the standard condition variable
// (sync.Cond) type, but uses a channel for signaling so that the waiters can
// select on its completion.
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

// Handoff is a singly-buffered level-triggered producer-consumer handoff.
// A consumer blocks on the Ready channel until a producer calls Send.  Calls
// to Send do not block; once a value has been sent to the handoff, subsequent
// values are discarded until the first one was consumed.
type Handoff[T any] struct {
	ch chan T
}

// NewHandoff constructs a new empty handoff.
func NewHandoff[T any]() *Handoff[T] { return &Handoff[T]{ch: make(chan T, 1)} }

// Send hands off or discards v, and reports whether v was successfully
// delivered for handoff (true) discarded (false). Send does not block.
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

// Value holds a single value of type T that can be concurrently accessed by
// multiple goroutines. A zero Value is ready for use, but must not be copied
// after its first use.
type Value[T any] struct {
	x     T
	mu    sync.Mutex
	ready chan struct{}
}

// NewValue creates a new Value with the given initial value.
func NewValue[T any](init T) *Value[T] { return &Value[T]{x: init} }

// Set updates the value stored in v to newValue.
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
// current value in v. The flag indicating whether Set was called (true) or ctx
// timed out (false).
//
// If v.Set is not called before ctx ends, Wait returns the value v held when
// Wait was called. Otherwise, Wait returns the value from one of the v.Set
// calls made during its execution. If multiple goroutines set v concurrently,
// Wait will return the value from one of them, but not necessarily the first.
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

// Package msync defines some helpful types for managing concurrency.
package msync

import (
	"context"
	"sync"
)

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

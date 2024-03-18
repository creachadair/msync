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
	mu    sync.Mutex
	x     T
	gen   uint64        // write generation, incremented by Set and SC
	ready chan struct{} // signal channel for Wait
}

// NewValue creates a new Value with the given initial value.
func NewValue[T any](init T) *Value[T] { return &Value[T]{x: init} }

// Set updates the value stored in v to newValue. Calling Set also wakes any
// goroutines that are blocked in the Wait method. Set invalidates any linked
// snapshots open on v, even if the target value does not change.
func (v *Value[T]) Set(newValue T) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.setLocked(newValue)
}

func (v *Value[T]) setLocked(newValue T) {
	v.x = newValue
	v.gen++
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
	select {
	case <-ctx.Done():
		return old, false
	case <-ready:
		v.mu.Lock()
		defer v.mu.Unlock()
		return v.x, true
	}
}

// LoadLink returns a linked view of the current value of v.
func (v *Value[T]) LoadLink() *Linked[T] {
	v.mu.Lock()
	defer v.mu.Unlock()
	return &Linked[T]{v: v, snap: v.x, gen: v.gen}
}

// Linked is a snapshot of a Value acquired by a call to its LoadLink method.
//
// A linked snapshot may be "valid" or "invalid". It is "valid" if a call to
// its StoreCond method could succeed at some point in the future; otherwise it
// is "invalid". A valid snapshot may become invalid, but an invalid snapshot
// is permanently so.
type Linked[T any] struct {
	v    *Value[T] // the base Value
	snap T         // the snapshotted value
	gen  uint64    // the generation at the time of link
}

// Get returns the current contents of the snapshot.
func (lv *Linked[T]) Get() T { return lv.snap }

// Set updates the contents of the snapshot. Modifications of the snapshot do
// not affect the original value until a successful call to StoreCond.
func (lv *Linked[T]) Set(v T) { lv.snap = v }

// StoreCond attempts to update the linked Value with the current contents of
// the snapshot, and reports whether doing so succeeded. Once StoreCond has
// been called, lv is invalid.
//
// StoreCond succeeds if no successful StoreCond or Set operation has been
// applied to the underlying Value since the LoadLink that created lv.
func (lv *Linked[T]) StoreCond() bool {
	lv.v.mu.Lock()
	defer lv.v.mu.Unlock()
	if lv.v.gen == lv.gen {
		lv.v.setLocked(lv.snap)
		return true
	}
	return false
}

// Validate reports whether a call to StoreCond would have succeeded given the
// current state of lv. When Validate reports true, lv is valid; otherwise lv
// is invalid.
func (lv *Linked[T]) Validate() bool {
	lv.v.mu.Lock()
	defer lv.v.mu.Unlock()
	return lv.v.gen == lv.gen
}

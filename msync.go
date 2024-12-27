// Package msync defines some helpful types for managing concurrency.
package msync

import (
	"sync"
)

// A Value is a mutable container for a single value of type T that can be
// concurrently accessed by multiple goroutines. A zero Value is ready for use,
// but must not be copied after its first use.
//
// The Value takes ownership of the value in its custody. In particular, if a
// pointer or a slice is stored in a Value, the caller must not modify the
// contents without separate synchronization.
type Value[T any] struct {
	mu   sync.Mutex
	x    T
	gen  uint64   // write generation, incremented by Set and SC
	wait []chan T // reporting channels for wait
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

	for _, ch := range v.wait {
		ch <- newValue
		close(ch)
	}
	v.wait = v.wait[:0]
}

// Get returns the current value stored in v.
func (v *Value[T]) Get() T {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.x
}

// Wait returns a new channel that blocks until the value of v changes, then
// delivers the current value of v. Each call to Wait returns a new channel.
// Once a value has been delivered to the channel, it is closed.
//
// If multiple goroutines set v concurrently, the channel will deliver the
// value from one of them, but not necessarily the first.
func (v *Value[T]) Wait() <-chan T {
	v.mu.Lock()
	defer v.mu.Unlock()

	// Buffer so that delivery does not block if the caller gives up.
	ch := make(chan T, 1)
	v.wait = append(v.wait, ch)
	return ch
}

// LoadLink links a view of the current value of v.
// If lv == nil, a new linked value is allocated and returned.
// Otherwise, the contents of *lv are replaced and lv is returned.
func (v *Value[T]) LoadLink(lv *Linked[T]) *Linked[T] {
	if lv == nil {
		lv = new(Linked[T])
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	lv.v, lv.snap, lv.gen = v, v.x, v.gen
	return lv
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

// StoreCond attempts to update the linked Value with v, and reports whether
// doing so succeeded. Once StoreCond has been called, lv is invalid.
//
// StoreCond succeeds if no successful StoreCond or Set operation has been
// applied to the underlying Value since the LoadLink that initialized lv.
func (lv *Linked[T]) StoreCond(v T) bool {
	lv.v.mu.Lock()
	defer lv.v.mu.Unlock()
	if lv.v.gen == lv.gen {
		lv.snap = v
		lv.v.setLocked(v)
		return true
	}
	return false
}

// Validate reports whether a call to StoreCond would have succeeded given the
// current state of lv. When Validate reports true, it means lv was valid at
// the time of the call; it may have become invalid by the time the caller
// receives the result. If Validate reports false, lv is forever invalid.
func (lv *Linked[T]) Validate() bool {
	lv.v.mu.Lock()
	defer lv.v.mu.Unlock()
	return lv.v.gen == lv.gen
}

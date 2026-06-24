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
	wait chan struct{} // reporting channel for wait
	gen  uint64        // write generation, incremented by Set and SC
	x    T
}

// NewValue creates a new Value with the given initial value.
func NewValue[T any](init T) *Value[T] { return &Value[T]{x: init} }

// Set updates the value stored in v to newValue. Calling Set also wakes any
// goroutines that are blocked on a Wait channel. Set invalidates any linked
// snapshots open on v, even if the target value does not change.
func (v *Value[T]) Set(newValue T) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.setLocked(newValue)
}

// Update updates the value stored in v in-place using f.  Calling Update also
// wakes any goroutines that are blocked on a Wait channel, and invalidates any
// linked snapshots open on v.
//
// The pointer passed to f will never be nil.
func (v *Value[T]) Update(f func(*T)) {
	v.mu.Lock()
	defer v.mu.Unlock()
	cp := v.x // shallow copy, in case f fails during an update
	f(&cp)
	v.setLocked(cp)
}

func (v *Value[T]) setLocked(newValue T) {
	v.x = newValue
	v.gen++

	// If waiters are expecting a signal, give it to them.
	if v.wait != nil {
		close(v.wait)
		v.wait = nil
	}
}

// Get returns the current value stored in v.
func (v *Value[T]) Get() T {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.x
}

// Wait returns a channel that is closed the next time the value of v is set
// via [Value.Set] or a successful call to [Link.StoreCond].
func (v *Value[T]) Wait() <-chan struct{} {
	v.mu.Lock()
	defer v.mu.Unlock()

	// Buffer so that delivery does not block if the caller gives up.
	if v.wait == nil {
		v.wait = make(chan struct{})
	}
	return v.wait
}

// LoadLink links a view of the current value of v.
// If lv == nil, a new link is allocated and returned.
// Otherwise, the contents of *lv are replaced and lv is returned.
func (v *Value[T]) LoadLink(lv *Link[T]) *Link[T] {
	if lv == nil {
		lv = new(Link[T])
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	lv.v, lv.snap, lv.gen = v, v.x, v.gen
	return lv
}

// Link is a snapshot of a Value acquired by a call to its LoadLink method.
// Use [Link.Get] to obtain the captured value.  The captured value is fixed,
// and does not change if the underlying Value is updated separately.
//
// A linked snapshot is either "valid" or "invalid". It is "valid" if a call to
// [Link.StoreCond] or [Link.UpdateCond] could succeed at some point in the
// future; otherwise it is "invalid". A valid snapshot may become invalid, but
// an invalid snapshot is permanently so. See also: [Link.Validate].
type Link[T any] struct {
	v    *Value[T] // the base Value
	snap T         // the snapshotted value
	gen  uint64    // the generation at the time of link
}

// Get returns the value captured in the snapshot.
func (lv *Link[T]) Get() T { return lv.snap }

// StoreCond attempts to update the linked [Value] with v, and reports whether
// the update succeeded. An update succeeds if no successful StoreCond,
// UpdateCond, Set, or Update operation has been applied to the linked Value
// since the [Value.LoadLink] that initialized lv.
//
// Once StoreCond has been called, whether successful or not, lv is invalid.
// It is safe to re-link and reuse an invalid [Link].
// If StoreCond succeeds, the Get method returns the updated value.
func (lv *Link[T]) StoreCond(v T) bool {
	lv.v.mu.Lock()
	defer lv.v.mu.Unlock()
	if lv.v.gen == lv.gen {
		lv.snap = v
		lv.v.setLocked(v)
		return true
	}
	return false
}

// UpdateCond attempts to update the linked [Value] with f, and reports whether
// the update succeeded. An update succeeds if no successful StoreCond,
// UpdateCond, Set, or Update operation has been applied to the linked Value
// since the [Value.LoadLink] that initialized lv.  If an update is not
// possible, f is not called.
//
// Once UpdateCond has been called, whether successful or not, lv is invalid.
// It is safe to re-link and reuse an invalid [Link].
// If UpdateCond succeeds, the Get method returns the updated value.
//
// The pointer passed to f will never be nil.
func (lv *Link[T]) UpdateCond(f func(*T)) bool {
	lv.v.mu.Lock()
	defer lv.v.mu.Unlock()
	if lv.v.gen == lv.gen {
		f(&lv.snap)
		lv.v.setLocked(lv.snap)
		return true
	}
	return false
}

// Validate reports whether a call to [Link.StoreCond] or [Link.UpdateCond]
// would have succeeded given the current state of lv. When Validate reports
// true, it means lv was valid at the time of the call; it may have become
// invalid by the time the caller receives the result. If Validate reports
// false, lv is invalid.  It is safe to re-link and reuse an invalid [Link].
func (lv *Link[T]) Validate() bool {
	lv.v.mu.Lock()
	defer lv.v.mu.Unlock()
	return lv.v.gen == lv.gen
}

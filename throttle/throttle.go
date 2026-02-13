// Package throttle allows calls to a function to be coalesced among
// multiple concurrent goroutines.
package throttle

import (
	"context"
	"fmt"
	"sync"
)

// Adapt converts a function with a compatible signature to a [Func].
// The concrete value of fn must be a function with one of the following
// type schemes:
//
//	func()
//	func(context.Context)
//	func() error
//	func(context.Context) error
//	func() V
//	func(context.Context) V
//	func() (V, error)
//	func(context.Context) (V, error)
//
// If fn is not a function or does not have one of these forms, Adapt panics.
// If fn is already a Func[V], it is returned unmodified.
// Wrapped functions with no error result report a nil error.
// Wrapped functions with no value result report a zero value.
func Adapt[V any](fn any) Func[V] {
	var zero V
	switch f := fn.(type) {
	case Func[V]:
		return f
	case func():
		return func(context.Context) (V, error) {
			f()
			return zero, nil
		}
	case func(context.Context):
		return func(ctx context.Context) (V, error) {
			f(ctx)
			return zero, nil
		}
	case func() error:
		return func(context.Context) (V, error) {
			return zero, f()
		}
	case func(context.Context) error:
		return func(ctx context.Context) (V, error) {
			return zero, f(ctx)
		}
	case func() V:
		return func(ctx context.Context) (V, error) {
			return f(), nil
		}
	case func(ctx context.Context) V:
		return func(ctx context.Context) (V, error) {
			return f(ctx), nil
		}
	case func() (V, error):
		return func(context.Context) (V, error) {
			return f()
		}
	case func(ctx context.Context) (V, error):
		return f
	default:
		panic(fmt.Sprintf("unsupported type %T", fn))
	}
}

// A Throttle coalesces function calls so that all goroutines concurrently
// executing [Throttle.Call] share the result of a single function execution
// made by one of the participants. This behaviour is sometimes also described
// as "single-flighting".
//
// A Throttle is initially idle. The first goroutine to execute [Throttle.Call]
// on an idle throttle begins a new session and executes its function. All
// other goroutines that call the throttle during an active session block until
// either:
//
//   - The context governing that goroutine's call ends, in which case it
//     reports a zero value and the error that ended the context.
//
//   - The goroutine leading the session completes its execution of its
//     function, and reports a value and error, which is then shared among all
//     the goroutines participating in the session.
//
// If the leading goroutine's execution ends because the context governing its
// calling goroutine ended, another waiting goroutine (if any) is woken up and
// given an opportunity to use the throttle.  Once all concurrent goroutines
// have returned, the throttle is once again idle, and the next caller will
// begin a new session.
//
// Within a given session, a proposed function may partially execute multiple
// times, if one or more goroutines leading the session return early due to
// context termination. At most one goroutine will be active in the throttle at
// a time, however; and if any completes before its context ends (even with an
// error) no further run functions will be executed within the scope of that
// session.
//
// A zero Throttle is ready for use, but must not be copied after first use.
type Throttle[V any] struct {
	μ      sync.Mutex
	waits  []chan result[V]
	active bool
}

type result[V any] struct {
	value V
	err   error
}

// Func is a function that can be managed by a [Throttle].
type Func[V any] func(context.Context) (V, error)

// New constructs a new idle [Throttle].
func New[V any]() *Throttle[V] { return new(Throttle[V]) }

// Call proposes to invoke run guarded by t.
//
// If ctx ends before a value is resolved, Call returns a zero value and the
// context error. Otherwise, it returns the value and error from the first
// proposed run function to complete among all goroutines concurrently active
// in t during the execution of Call.
//
// Concurrent goroutines are permitted to propose different run functions to t,
// so the values returned to a caller on one goroutine may not come from the
// function passed to Call by that goroutine.
//
// Call is safe for concurrent use by multiple goroutines.
func (t *Throttle[V]) Call(ctx context.Context, run Func[V]) (V, error) {
	var zero V

	t.μ.Lock()
	for t.active {
		// Someone is already working on the call, wait for them.
		// N.B. buffer the channel so that if a waiter gives up, a successful
		// call will not stall waiting for a receive.
		ready := make(chan result[V], 1)
		t.waits = append(t.waits, ready)

		t.μ.Unlock()
		select {
		case <-ctx.Done():
			// Our context has ended, give up on the call.
			return zero, ctx.Err()
		case r, ok := <-ready:
			// The previous caller is finished.
			if ok {
				// The previous caller determined the result.
				return r.value, r.err
			}
		}
		t.μ.Lock()

		// Reaching here, the previous caller ended without determining a result
		// before ctx ended. Try again.
	}
	defer t.μ.Unlock()

	// Attempt to determine the result. This may fail if ctx ends, or may
	// report some other error.
	v, err := t.runProtectLocked(ctx, run)

	// If our context has not yet ended (signifying any error is from the target
	// function), then the result is determined.  Propagate it to any waiting
	// tasks, and then return it.
	if ctx.Err() == nil {
		for _, w := range t.waits {
			w <- result[V]{value: v, err: err}
			close(w)
		}
		t.waits = nil
		return v, err
	}

	// Otherwise, ctx ended before we could determine the result.  Signal any
	// waiting tasks that they should try, and then fail back to the caller.
	for _, w := range t.waits {
		close(w) // N.B. no value sent signals a retry is needed
	}
	t.waits = nil

	// Reaching here, our context has ended with no result determined.
	return zero, ctx.Err()
}

// runProtectLocked calls t.run(ctx) outside the lock, and reports its result.
// The caller must hold t.μ.
func (t *Throttle[V]) runProtectLocked(ctx context.Context, run Func[V]) (_ V, err error) {
	t.active = true
	t.μ.Unlock() // release the lock while running
	defer func() {
		t.μ.Lock()
		t.active = false // N.B. after re-acquire
	}()
	return run(ctx)
}

// Set is a collection of [Throttle] values indexed by key.  A zero value is
// ready for use, but must not be copied after first use.
type Set[Key comparable, V any] struct {
	μ     sync.Mutex // protects throttle
	entry map[Key]*Throttle[V]
}

// NewSet constructs a new empty [Set].
func NewSet[Key comparable, V any]() *Set[Key, V] { return new(Set[Key, V]) }

// Call calls the throttle associated with key, constructing a new one if
// necessary. Call is safe for use by multiple concurrent goroutines.
//
// All concurrent callers of Call with a given key share a single [Throttle].
func (s *Set[Key, V]) Call(ctx context.Context, key Key, run Func[V]) (V, error) {
	throttle := s.throttleFor(key)
	defer func() {
		s.μ.Lock()
		defer s.μ.Unlock()

		// If the throttle for this key is still the one that just finished
		// executing (that is, tkey), remove it from the set: Any other
		// concurrent goroutines sharing this throttle have either been delivered
		// their pending values, given up from context termination, or panicked.
		// Either way, this group is effectively done, and the next caller on
		// this key should start with a fresh throttle.
		//
		// Note that we don't have to handle a panic here, as Throttle.Call has
		// already converted a panic in the run function into an error.
		if s.entry[key] == throttle {
			delete(s.entry, key)
		}
	}()
	return throttle.Call(ctx, run)
}

func (s *Set[Key, V]) throttleFor(key Key) *Throttle[V] {
	s.μ.Lock()
	defer s.μ.Unlock()
	t, ok := s.entry[key]
	if !ok {
		t = new(Throttle[V])
		if s.entry == nil {
			s.entry = make(map[Key]*Throttle[V])
		}
		s.entry[key] = t
	}
	return t
}

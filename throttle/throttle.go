// Package throttle allows calls to a function to be coalesced among
// multiple concurrent goroutines.
package throttle

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
)

// A Throttle coalesces calls to a function so that all goroutines concurrently
// executing [Throttle.Call] share the result of a single execution of the
// function made by one of the participants.
//
// A Throttle is initially idle. The first goroutine to execute [Throttle.Call]
// on an idle throttle begins a new session. All goroutines that call the
// throttle during an active session block until either:
//
//   - The context governing that call ends, in which case it reports a zero
//     value and the error that ended the context.
//
//   - The goroutine executing the throttled function completes its call and
//     reports a value and error, which is then shared among all the goroutines
//     participating in the session.
//
// If the execution of the throttled function ends because the context
// governing its calling goroutine ended, another waiting goroutine (if any) is
// woken up and given an oppoartunity to call the throttled function.  Once all
// concurrent goroutines have returned, the throttle is once again idle, and
// the next caller will begin a new session.
type Throttle[T any] struct {
	run Func[T] // read-only after initialization

	μ      sync.Mutex
	waits  []chan result[T]
	active bool
}

type result[T any] struct {
	value T
	err   error
}

// Func is an alias for a function that can be managed by a [Throttle].
type Func[T any] func(context.Context) (T, error)

// New constructs a new [Throttle] that executes run.
func New[T any](run Func[T]) *Throttle[T] { return &Throttle[T]{run: run} }

// Call invokes the function guarded by t. Call is safe for concurrent use by
// multiple goroutines.
func (t *Throttle[T]) Call(ctx context.Context) (T, error) {
	var zero T

	t.μ.Lock()
	for ctx.Err() == nil {
		// Safety check: The lock must be held at this point.
		if t.μ.TryLock() {
			panic("lock is not held at start of call")
		}

		if t.active {
			// Someone is already working on the call, wait for them.
			// N.B. buffer the channel so that if a waiter gives up, a successful
			// call will not stall waiting for a receive.
			ready := make(chan result[T], 1)
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

				// The previous caller did not determine a result.
				// We should go back and retry (if our ctx is still alive).
				t.μ.Lock()
				continue
			}

			// unreachable
		}

		// Begin a new session.
		t.active = true

		// Attempt to determine the result. This may fail if ctx ends, or may
		// report some other error.
		t.μ.Unlock()
		v, err := func() (_ T, err error) {
			defer func() {
				if x := recover(); x != nil {
					err = fmt.Errorf("panic in run: %v\n%s", x, string(debug.Stack()))
				}
			}()
			return t.run(ctx)
		}()
		t.μ.Lock()
		t.active = false

		// If run succeeded, or our context has not yet completed, the result is
		// determined.  Propagate it to any waiting tasks, and then return it.
		if err == nil || ctx.Err() == nil {
			for _, w := range t.waits {
				w <- result[T]{value: v, err: err}
				close(w)
			}
			t.waits = nil
			t.μ.Unlock()
			return v, err
		}

		// Otherwise, ctx ended before we could determine the result.  Signal any
		// waiting tasks that they should try, and then fail back to the caller.
		for _, w := range t.waits {
			close(w) // N.B. no value sent signals a retry is needed
		}
		t.waits = nil
		break
	}

	// Reaching here, our context has ended with no result determined.
	t.μ.Unlock()
	return zero, ctx.Err()
}

// Set is a collection of [Throttle] values indexed by key.
// A zero value is ready for use, but must not be copied after first use.
type Set[Key comparable, T any] struct {
	μ        sync.Mutex // protects throttle
	throttle map[Key]*Throttle[T]
}

// NewSet constructs a new empty [Set].
func NewSet[Key comparable, T any]() *Set[Key, T] { return new(Set[Key, T]) }

// Call calls the throttle associated with key, constructing a new one if
// necessary. Call is safe for use by multiple concurrent goroutines.
//
// All concurrent callers of Call with a given key share a single [Throttle].
func (s *Set[Key, T]) Call(ctx context.Context, key Key, run Func[T]) (T, error) {
	tkey := func() *Throttle[T] {
		s.μ.Lock()
		defer s.μ.Unlock()
		tkey, ok := s.throttle[key]
		if !ok {
			if s.throttle == nil {
				s.throttle = make(map[Key]*Throttle[T])
			}
			tkey = New(run)
			s.throttle[key] = tkey
		}
		return tkey
	}()
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
		if s.throttle[key] == tkey {
			delete(s.throttle, key)
		}
	}()
	return tkey.Call(ctx)
}

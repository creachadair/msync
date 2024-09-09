package msync

import "sync"

// Trigger is an edge-triggered condition shared by multiple goroutines.
// The Ready method returns a channel that is closed when the trigger is
// activated.
//
// When a trigger is first created it is inactive. It remains inactive until
// the Set or Signal method is called, which causes the current ready channel
// to be closed. Once a trigger has been activated, it remains active until it
// is reset. Use the Reset method to reset the trigger to inactive.
//
// The Signal method immediately resets the trigger, acting as Set and Reset
// done in a single step.
//
// A zero Trigger is ready for use, and is inactive, but must not be copied
// after any of its methods have been called.
type Trigger struct {
	μ      sync.Mutex
	ch     chan struct{}
	closed bool

	// The signal channel is lazily initialized by the first waiter.
}

// NewTrigger constructs a new inactive Trigger.
func NewTrigger() *Trigger { return new(Trigger) }

// Signal activates and immediately resets the trigger.  If the trigger was
// already active, this is equivalent to Reset.
func (t *Trigger) Signal() {
	t.μ.Lock()
	defer t.μ.Unlock()

	if t.ch != nil && !t.closed {
		close(t.ch) // wake any pending waiters
	}
	t.ch = make(chan struct{})
	t.closed = false
}

// Set activates the trigger. If the trigger was already active, it has no
// effect.
func (t *Trigger) Set() {
	t.μ.Lock()
	defer t.μ.Unlock()

	if t.ch == nil {
		t.ch = make(chan struct{})
		close(t.ch)
	} else if !t.closed {
		close(t.ch)
	}
	t.closed = true
}

// Reset resets the trigger. If the trigger was already inactive, it has no
// effect.
func (t *Trigger) Reset() {
	t.μ.Lock()
	defer t.μ.Unlock()

	if t.closed {
		t.ch = nil
		t.closed = false
	}
}

// Ready returns a channel that is closed when t is activated.  If t is active
// when Ready is called, the returned channel will already be closed.
func (t *Trigger) Ready() <-chan struct{} {
	t.μ.Lock()
	defer t.μ.Unlock()

	if t.ch == nil {
		t.ch = make(chan struct{})
		t.closed = false
	}
	return t.ch
}

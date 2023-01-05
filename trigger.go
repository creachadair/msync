package msync

import "sync"

// Trigger is an edge-triggered condition shared by multiple goroutines.  It is
// analogous in effect to the standard condition variable (sync.Cond) but uses
// a channel for signaling.
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

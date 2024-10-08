package msync

import (
	"context"
	"errors"
	"sync"
)

// ErrClosed is the sentinel error reported by a collector that is closed
// before a value could be delivered.
var ErrClosed = errors.New("collector is closed")

// A Collector manages a fan-in channel shared by multiple readers and writers.
// It wraps and behaves like a normal buffered channel, but when c is closed
// any pending writes are safely terminated and report errors rather than
// panicking.
type Collector[T any] struct {
	// μ protects the fields below:
	// Lock μ shared to copy or send to ch.
	// Lock μ exclusively to close ch or modify either field.
	μ    sync.RWMutex
	ch   chan T        // delivers values to the receiver
	done chan struct{} // closed when the collector is closed
}

// Collect returns a new collector wrapping the specified channel.
//
// The collector takes ownership of ch, and the caller must not access ch
// except via the methods of the Collector. The channel may be buffered or
// unbuffered.
func Collect[T any](ch chan T) *Collector[T] {
	return &Collector[T]{ch: ch, done: make(chan struct{})}
}

// Recv returns a channel to which sent values are delivered.  The returned
// channel is closed when c is closed.  After c is closed, Recv returns a nil
// channel.
func (c *Collector[T]) Recv() <-chan T {
	c.μ.RLock()
	defer c.μ.RUnlock()
	return c.ch
}

// Send sends v to the collector. It blocks until v is delivered, c closes, or
// ctx ends.  If c closes or ctx ends before v is sent, Send reports an error;
// otherwise Send returns nil.
func (c *Collector[T]) Send(ctx context.Context, v T) error {
	c.μ.RLock()
	defer c.μ.RUnlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return ErrClosed
	case c.ch <- v:
		return nil
	}
}

// Close closes the collector, which closes the receiver and causes any pending
// sends to fail. If c is already closed, Close returns ErrClosed.  Close can
// be called repeatedly, but from at most one goroutine at a time.
func (c *Collector[T]) Close() error {
	select {
	case <-c.done:
		return ErrClosed
	default:
		close(c.done)

		c.μ.Lock()
		defer c.μ.Unlock()
		close(c.ch)
		c.ch = nil // no future sender must see c.ch as ready
		return nil
	}
}

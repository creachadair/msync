package msync_test

import (
	"context"
	"errors"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creachadair/msync"
	"github.com/fortytw2/leaktest"
)

func TestTrigger(t *testing.T) {
	defer leaktest.Check(t)()

	// Start up a bunch of tasks that listen to a trigger, signal the trigger,
	// and verify that it woke them all up.
	tr := msync.NewTrigger()

	const numTasks = 3

	ok := make([]bool, numTasks)
	var start, stop sync.WaitGroup

	for i := 0; i < numTasks; i++ {
		i := i
		start.Add(1)
		stop.Add(1)
		go func() {
			ch := tr.Ready()
			start.Done()
			<-ch
			ok[i] = true
			stop.Done()
		}()
	}

	start.Wait()
	tr.Signal()
	stop.Wait()

	for i, b := range ok {
		if !b {
			t.Errorf("Task %d did not report success", i+1)
		}
	}
}

func TestHandoff(t *testing.T) {
	defer leaktest.Check(t)()

	h := msync.NewHandoff[int]()

	mustSend := func(v int, want bool) {
		if got := h.Send(v); got != want {
			t.Errorf("Send(%v): got %v, want %v", v, got, want)
		}
	}

	// Multiple sends to h do not block.
	mustSend(1, true)
	mustSend(2, false)
	mustSend(3, false)

	// The first value handed off is ready.
	if got := <-h.Ready(); got != 1 {
		t.Errorf("Ready: got %v, want 1", got)
	}

	// Now that the value has been handed off, nothing is available till we send
	// another value.
	select {
	case <-time.After(100 * time.Millisecond):
		// OK, nothing here
	case bad := <-h.Ready():
		t.Errorf("Ready: unexpected value: %v", bad)
	}

	// The next value sent is the next received.
	mustSend(4, true)
	if got := <-h.Ready(); got != 4 {
		t.Errorf("Ready: got %v, want 4", got)
	}

	// Play ping-ping.
	p := msync.NewHandoff[any]()
	done := make(chan struct{})
	var sum int
	go func() {
		defer close(done)
		for i := 0; i < 3; i++ {
			sum += <-h.Ready()
			p.Send(nil)
		}
	}()

	mustSend(1, true)
	<-p.Ready()
	mustSend(3, true)
	<-p.Ready()
	mustSend(5, true)
	<-p.Ready()
	<-done

	if sum != 9 {
		t.Errorf("Checksum: got %v, want 9", sum)
	}
}

func TestValue_Zero(t *testing.T) {
	var v msync.Value[int]

	if got, want := v.Get(), 0; got != want {
		t.Errorf("Get from zero Value: got %d, want %d", got, want)
	}
	v.Set(25)
	if got, want := v.Get(), 25; got != want {
		t.Errorf("Get: got %d, want %d", got, want)
	}
}

func TestValue(t *testing.T) {
	defer leaktest.Check(t)()

	v := msync.NewValue("apple")
	var wg sync.WaitGroup

	mustGet := func(want string) {
		if got := v.Get(); got != want {
			t.Errorf("Get: got %q, want %q", got, want)
		}
	}
	setAfter := func(d time.Duration, s string) {
		wg.Add(1)
		time.AfterFunc(d, func() {
			defer wg.Done()
			v.Set(s)
		})
	}
	mustWait := func(ctx context.Context, wantV string, wantF bool) {
		got, ok := v.Wait(ctx)
		if got != wantV {
			t.Errorf("Wait value: got %q, want %q", got, wantV)
		}
		if ok != wantF {
			t.Errorf("Wait flag: got %v, want %v", ok, wantF)
		}
	}

	ctx := context.Background()

	// Verify the initial value.
	mustGet("apple")

	// Verify that we can Get the expected value from a Set.
	v.Set("pear")
	mustGet("pear")

	// Verify that a waiter wakes for a set.
	setAfter(5*time.Millisecond, "plum")
	mustWait(ctx, "plum", true)
	mustGet("plum")

	// Verify that a waiter times out if no Set occurs.
	t.Run("Timeout", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
		defer cancel()
		mustWait(ctx, "plum", false)
	})

	// Verify that Wait gets the value of a concurrent Set.
	t.Run("Concur", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Millisecond)
		defer cancel()

		setAfter(2000*time.Microsecond, "cherry")
		setAfter(1500*time.Microsecond, "raspberry")

		got, ok := v.Wait(ctx)
		checkOneOf(t, "Wait value", got, "raspberry", "cherry")
		if !ok {
			t.Error("Wait: got false flag, wanted true")
		}
	})

	// Clean up goroutines for the leak checker.
	wg.Wait()

	// Make sure the value settled to one of the ones we set.
	checkOneOf(t, "Get value", v.Get(), "raspberry", "cherry")
}

func checkOneOf(t *testing.T, pfx, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if got == w {
			return
		}
	}
	t.Errorf("%s: got %q, want one of {%+v}", pfx, got, strings.Join(want, ", "))
}

func TestCollector(t *testing.T) {
	t.Run("Basic", func(t *testing.T) {
		defer leaktest.Check(t)()

		const numWriters = 5
		const numValues = 100

		c := msync.NewCollector[int](0)
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			var nr int
			for range c.Recv() {
				nr++
				if nr >= numValues {
					if err := c.Close(); err != nil {
						t.Errorf("Close: got %v, want nil", err)
					}
					return
				}
			}
		}()

		ctx := context.Background()
		for i := 0; i < numWriters; i++ {
			w := i + 1
			wg.Add(1)
			go func() {
				defer wg.Done()

				// Each writer will keep sending until c closes.
				for {
					if err := c.Send(ctx, rand.Intn(500)-250); err != nil {
						if !errors.Is(err, msync.ErrClosed) {
							t.Errorf("Writer %d: got error %v, want %v", w, err, msync.ErrClosed)
						}
						return
					}
				}
			}()
		}

		wg.Wait()
	})

	t.Run("Cancel", func(t *testing.T) {
		c := msync.NewCollector[bool](0)
		defer c.Close()

		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		defer cancel()

		if err := c.Send(ctx, true); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Send: got %v, want %v", err, context.DeadlineExceeded)
		}
	})

	t.Run("MultiClose", func(t *testing.T) {
		c := msync.NewCollector[any](0)
		if err := c.Close(); err != nil {
			t.Errorf("Close 1: got %v, want nil", err)
		}

		if err := c.Close(); !errors.Is(err, msync.ErrClosed) {
			t.Errorf("Close 2: got %v, want %v", err, msync.ErrClosed)
		}
	})
}

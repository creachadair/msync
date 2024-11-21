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

func TestFlag(t *testing.T) {
	defer leaktest.Check(t)()

	f := msync.NewFlag[int]()

	mustSet := func(v int, want bool) {
		if got := f.Set(v); got != want {
			t.Errorf("Send(%v): got %v, want %v", v, got, want)
		}
	}

	// Multiple sends to h do not block.
	mustSet(1, true)
	mustSet(2, false)
	mustSet(3, false)

	// The first value set is ready.
	if got := <-f.Ready(); got != 1 {
		t.Errorf("Ready: got %v, want 1", got)
	}

	// Now that the value has been set, nothing is available till we set another
	// value.
	select {
	case <-time.After(100 * time.Millisecond):
		// OK, nothing here
	case bad := <-f.Ready():
		t.Errorf("Ready: unexpected value: %v", bad)
	}

	// The next value sent is the next received.
	mustSet(4, true)
	if got := <-f.Ready(); got != 4 {
		t.Errorf("Ready: got %v, want 4", got)
	}

	// Play ping-ping.
	p := msync.NewFlag[any]()
	done := make(chan struct{})
	var sum int
	go func() {
		defer close(done)
		for range 3 {
			sum += <-f.Ready()
			p.Set(nil)
		}
	}()

	mustSet(1, true)
	<-p.Ready()
	mustSet(3, true)
	<-p.Ready()
	mustSet(5, true)
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

func TestValue_llsc(t *testing.T) {
	checkValue := func(t *testing.T, get func() int, want int) {
		t.Helper()
		if got := get(); got != want {
			t.Errorf("Value is %d, want %d", got, want)
		}
	}
	v := msync.NewValue(1)
	checkValue(t, v.Get, 1)

	t.Run("Success", func(t *testing.T) {
		s := v.LoadLink(nil)
		checkValue(t, s.Get, 1)

		if !s.Validate() {
			t.Error("Validate reported false")
		}

		checkValue(t, v.Get, 1)

		if !s.StoreCond(10) {
			t.Error("StoreCond reported false")
		}
		checkValue(t, s.Get, 10) // updated

		if s.StoreCond(10) {
			t.Errorf("Second StoreCond reported true")
		}
		checkValue(t, v.Get, 10)
	})

	t.Run("Fail/Set", func(t *testing.T) {
		s := v.LoadLink(nil)
		checkValue(t, s.Get, 10)

		if !s.Validate() {
			t.Error("Validate reported false")
		}

		v.Set(20)
		checkValue(t, v.Get, 20)

		if s.StoreCond(25) {
			t.Error("StoreCond reported true")
		}
		checkValue(t, v.Get, 20)
	})

	t.Run("Fail/SetSame", func(t *testing.T) {
		s := v.LoadLink(nil)
		checkValue(t, s.Get, 20)

		// Even a set back to the same value invalidates s.
		v.Set(20)

		if s.StoreCond(25) {
			t.Error("StoreCond reported true")
		}
		checkValue(t, v.Get, 20)
		checkValue(t, s.Get, 20) // not updated
	})

	t.Run("Fail/StoreCond", func(t *testing.T) {
		var v1, v2 msync.Linked[int]

		s1 := v.LoadLink(&v1)
		s2 := v.LoadLink(&v2)

		if s1 != &v1 {
			t.Errorf("LoadLink(v1): got %p, want %p", s1, &v1)
		}
		if s2 != &v2 {
			t.Errorf("LoadLink(v2): got %p, want %p", s2, &v2)
		}

		checkValue(t, v.Get, 20)

		if !s1.StoreCond(30) {
			t.Error("StoreCond(1) reported false")
		}
		checkValue(t, v.Get, 30)

		if s2.StoreCond(40) {
			t.Error("StoreCond(2) reported true")
		}
		checkValue(t, v.Get, 30)
	})

	t.Run("StoreCondWait", func(t *testing.T) {
		s := v.LoadLink(nil)

		// Verify that a successful StoreCond triggers waiters.
		ready := make(chan struct{})
		done := make(chan int, 1)
		go func() {
			defer close(done)
			close(ready)
			z, _ := v.Wait(context.Background())
			done <- z
		}()

		<-ready
		if !s.StoreCond(50) {
			t.Error("StoreCond reported false")
		}
		select {
		case got := <-done:
			if got != s.Get() {
				t.Errorf("Wait got %d, want %d", got, s.Get())
			}
		case <-time.After(10 * time.Second):
			t.Error("Timed out waiting for Wait to return")
		}
	})
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

		c := msync.Collect(make(chan int))
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
		for i := range numWriters {
			wg.Add(1)
			go func() {
				defer wg.Done()

				// Each writer will keep sending until c closes.
				for {
					if err := c.Send(ctx, rand.Intn(500)-250); err != nil {
						if !errors.Is(err, msync.ErrClosed) {
							t.Errorf("Writer %d: got error %v, want %v", i+1, err, msync.ErrClosed)
						}
						return
					}
				}
			}()
		}

		wg.Wait()
	})

	t.Run("Cancel", func(t *testing.T) {
		defer leaktest.Check(t)()

		c := msync.Collect(make(chan bool))
		defer c.Close()

		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		defer cancel()

		if err := c.Send(ctx, true); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Send: got %v, want %v", err, context.DeadlineExceeded)
		}
	})

	t.Run("MultiClose", func(t *testing.T) {
		c := msync.Collect(make(chan any))
		if err := c.Close(); err != nil {
			t.Errorf("Close 1: got %v, want nil", err)
		}

		if err := c.Close(); !errors.Is(err, msync.ErrClosed) {
			t.Errorf("Close 2: got %v, want %v", err, msync.ErrClosed)
		}
	})
}

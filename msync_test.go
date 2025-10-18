package msync_test

import (
	"context"
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
	ctx := context.Background()

	// Verify the initial value.
	mustGet("apple")

	// Verify that we can Get the expected value from a Set.
	v.Set("pear")
	mustGet("pear")

	// Verify that a waiter wakes for a set.
	t.Run("Wait", func(t *testing.T) {
		ch := v.Wait()
		setAfter(5*time.Millisecond, "plum")
		if got, ok := <-ch; !ok || got != "plum" {
			t.Errorf("Wait: got %q, %v; want plum, true", got, ok)
		}
		mustGet("plum")
	})

	// Verify that a waiter times out if no Set occurs.
	t.Run("Timeout", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
		defer cancel()

		ch := v.Wait()
		select {
		case <-ctx.Done():
			t.Log("Correctly timed out waiting for update")
		case got := <-ch:
			t.Errorf("Got value %q, wanted timeout", got)
		}
	})

	// Verify that if a waiter gives up, updates do not block.
	t.Run("GiveUp", func(t *testing.T) {
		v := msync.NewValue("quince")
		_ = v.Wait()
		_ = v.Wait()

		done := make(chan struct{})
		go func() { v.Set("pluot"); close(done) }()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Timed out waiting for Set")
		}
	})

	// Verify that Wait gets the value of a concurrent Set.
	t.Run("Concur", func(t *testing.T) {
		ch := v.Wait()

		setAfter(2000*time.Microsecond, "cherry")
		setAfter(1500*time.Microsecond, "raspberry")

		got := <-ch
		checkOneOf(t, "Wait value", got, "raspberry", "cherry")
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
		done := v.Wait()
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

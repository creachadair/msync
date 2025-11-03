package msync_test

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/creachadair/msync"
)

func TestRelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := msync.NewRelay[int]()

		mustSet := func(v int, want bool) {
			if got := h.Set(v); got != want {
				t.Errorf("Send(%v): got %v, want %v", v, got, want)
			}
		}

		// Multiple sends to h do not block.
		mustSet(1, true)
		mustSet(2, false)
		mustSet(3, false)

		// The first value set is ready.
		if got := <-h.Ready(); got != 1 {
			t.Errorf("Ready: got %v, want 1", got)
		}

		// Now that the value has been set, nothing is available till we set another
		// value.
		select {
		case <-time.After(time.Second):
			// OK, nothing here
		case bad := <-h.Ready():
			t.Errorf("Ready: unexpected value: %v", bad)
		}

		// The next value sent is the next received.
		mustSet(4, true)
		if got := <-h.Ready(); got != 4 {
			t.Errorf("Ready: got %v, want 4", got)
		}

		// Play ping-ping.
		p := msync.NewRelay[any]()
		var sum int
		go func() {
			for range 3 {
				sum += <-h.Ready()
				p.Set(nil)
			}
		}()

		mustSet(1, true)
		<-p.Ready()
		mustSet(3, true)
		<-p.Ready()
		mustSet(5, true)
		<-p.Ready()

		synctest.Wait()

		if sum != 9 {
			t.Errorf("Checksum: got %v, want 9", sum)
		}
	})
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
	synctest.Test(t, func(t *testing.T) {
		v := msync.NewValue("apple")

		mustGet := func(want string) {
			if got := v.Get(); got != want {
				t.Errorf("Get: got %q, want %q", got, want)
			}
		}
		setAfter := func(d time.Duration, s string) {
			time.AfterFunc(d, func() { v.Set(s) })
		}
		ctx := t.Context()

		// Verify the initial value.
		mustGet("apple")

		// Verify that we can Get the expected value from a Set.
		v.Set("pear")
		mustGet("pear")

		// Verify that a waiter wakes for a set.
		func() {
			t.Log("Wait")
			setAfter(5*time.Millisecond, "plum")

			if got, ok := <-v.Wait(); !ok || got != "plum" {
				t.Errorf("Wait: got %q, %v; want plum, true", got, ok)
			}
			mustGet("plum")
		}()

		// Verify that a waiter times out if no Set occurs.
		func() {
			t.Log("Timeout")
			ctx, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
			defer cancel()

			select {
			case <-ctx.Done():
				t.Log("Correctly timed out waiting for update")
			case got := <-v.Wait():
				t.Errorf("Got value %q, wanted timeout", got)
			}
		}()

		// Verify that if a waiter gives up, updates do not block.
		func() {
			t.Log("GiveUp")

			v := msync.NewValue("quince")
			_ = v.Wait()
			_ = v.Wait()

			go v.Set("pluot")

			synctest.Wait()
		}()

		// Verify that Wait gets the value of a concurrent Set.
		func() {
			t.Log("Concur")

			setAfter(200*time.Microsecond, "cherry")
			setAfter(100*time.Microsecond, "raspberry")

			time.Sleep(150 * time.Microsecond)
			mustGet("raspberry")
			time.Sleep(time.Second)
			mustGet("cherry")
		}()

		synctest.Wait()
		mustGet("cherry")
	})
}

func TestValue_llsc(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		checkValue := func(t *testing.T, get func() int, want int) {
			t.Helper()
			if got := get(); got != want {
				t.Errorf("Value is %d, want %d", got, want)
			}
		}
		v := msync.NewValue(1)
		checkValue(t, v.Get, 1)

		func() {
			t.Log("Success")
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
		}()

		func() {
			t.Log("Fail/Set")
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
		}()

		func() {
			t.Log("Fail/SetSame")
			s := v.LoadLink(nil)
			checkValue(t, s.Get, 20)

			// Even a set back to the same value invalidates s.
			v.Set(20)

			if s.StoreCond(25) {
				t.Error("StoreCond reported true")
			}
			checkValue(t, v.Get, 20)
			checkValue(t, s.Get, 20) // not updated
		}()

		func() {
			t.Log("Fail/StoreCond")
			var v1, v2 msync.Link[int]

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
		}()

		func() {
			t.Log("StoreCondWait")
			s := v.LoadLink(nil)

			// Verify that a successful StoreCond triggers waiters.
			go func() {
				time.Sleep(time.Second)
				if !s.StoreCond(50) {
					t.Error("StoreCond reported false")
				}
			}()
			if got := <-v.Wait(); got != s.Get() {
				t.Errorf("Wait got %d, want %d", got, s.Get())
			}
		}()
	})
}

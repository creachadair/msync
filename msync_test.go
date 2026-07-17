package msync_test

import (
	"testing"
	"testing/synctest"
	"time"
	"unsafe"

	"github.com/creachadair/msync"
)

func TestValue_Zero(t *testing.T) {
	var v msync.Value[int]

	t.Logf("%T is %d bytes", msync.Value[int]{}, unsafe.Sizeof(v))

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
		updateAfter := func(d time.Duration, repl string) {
			time.AfterFunc(d, func() {
				v.Update(func(s *string) { *s = repl })
			})
		}

		// Verify the initial value.
		mustGet("apple")

		// Verify that we can Get the expected value from a Set.
		v.Set("pear")
		mustGet("pear")

		// Verify that we can Get the expected value from an Update.
		v.Update(func(s *string) { *s += "?" })
		mustGet("pear?")

		// Verify that a waiter wakes for a set.
		setAfter(5*time.Millisecond, "plum")

		select {
		case <-v.Wait():
			mustGet("plum")
		case <-time.After(time.Second):
			t.Fatal("Timed out unexpectedly in Wait")
		}

		// Verify that a waiter wakes for an update.
		updateAfter(5*time.Millisecond, "lolwut")

		select {
		case <-v.Wait():
			mustGet("lolwut")
		case <-time.After(time.Second):
			t.Fatal("Timed out unexpectedly in Wait")
		}

		// Verify that a waiter times out if no Set occurs.
		select {
		case <-time.After(time.Second):
			t.Log("Correctly timed out waiting for update")
		case <-v.Wait():
			t.Error("Wait completed when it should not have")
		}

		// Verify that if a waiter gives up, updates do not block.
		v = msync.NewValue("quince")
		_ = v.Wait()
		_ = v.Wait()

		v.Set("pluot")

		// Verify that Wait gets some value of a concurrent Set.
		ready := v.Wait()
		setAfter(time.Millisecond, "cherry")
		setAfter(time.Millisecond, "raspberry")
		updateAfter(time.Millisecond, "quince")

		<-ready
		if w := v.Get(); w != "cherry" && w != "raspberry" && w != "quince" {
			t.Errorf("Wait: got %q, want cherry, raspberry, or quince", w)
		}
	})
}

func TestValue_llsc(t *testing.T) {
	checkValue := func(t *testing.T, get func() int, want int) {
		t.Helper()
		if got := get(); got != want {
			t.Errorf("Value is %d, want %d", got, want)
		}
	}

	t.Logf("%T is %d bytes", msync.Link[int]{}, unsafe.Sizeof(msync.Link[int]{}))

	t.Run("Store/OK", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			v := msync.NewValue(1)
			checkValue(t, v.Get, 1)

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
			checkValue(t, v.Get, 10) // updated

			if s.StoreCond(10) {
				t.Error("Second StoreCond reported true")
			}
			checkValue(t, v.Get, 10)
		})
	})

	t.Run("Update/OK", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			v := msync.NewValue(1)
			checkValue(t, v.Get, 1)

			s := v.LoadLink(nil)
			checkValue(t, s.Get, 1)

			if !s.Validate() {
				t.Errorf("Validate reported false")
			}
			checkValue(t, s.Get, 1)

			if !s.UpdateCond(func(v *int) { *v = 10 }) {
				t.Error("UpdateCond reported false")
			}
			checkValue(t, s.Get, 10) // updated
			checkValue(t, v.Get, 10) // updated

			if s.UpdateCond(func(*int) {}) {
				t.Error("Second UpdateCond reported true")
			}
			checkValue(t, v.Get, 10)
		})

	})

	t.Run("Fail/Set", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			v := msync.NewValue(10)

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
			checkValue(t, s.Get, 10) // the snapshot did not change
		})
	})

	t.Run("Fail/SetSame", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			v := msync.NewValue(20)

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
	})

	t.Run("Fail/StoreCond", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			v := msync.NewValue(20)

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
				t.Error("StoreCond(30) reported false")
			}
			checkValue(t, v.Get, 30)

			if s2.StoreCond(40) {
				t.Error("StoreCond(40) reported true")
			}
			checkValue(t, v.Get, 30)
			checkValue(t, s1.Get, 30) // s1 succeeded and was updated
			checkValue(t, s2.Get, 20) // s2 failed and remained unchanged
		})
	})

	t.Run("Fail/UpdateCond", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			v := msync.NewValue(20)

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

			if !s1.UpdateCond(func(v *int) { *v = 30 }) {
				t.Error("UpdateCond(30) reported false")
			}
			checkValue(t, v.Get, 30)

			if s2.UpdateCond(func(v *int) { *v = 40 }) {
				t.Error("UpdateCond(40) reported true")
			}
			checkValue(t, v.Get, 30)
			checkValue(t, s1.Get, 30) // s1 succeeded and was updated
			checkValue(t, s2.Get, 20) // s2 failed and remained unchanged
		})
	})

	t.Run("StoreCondWait", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			v := msync.NewValue(30)

			s := v.LoadLink(nil)

			// Verify that a successful StoreCond triggers waiters.
			time.AfterFunc(time.Second, func() {
				if !s.StoreCond(50) {
					t.Error("StoreCond reported false")
				}
			})
			<-v.Wait()
			if got := v.Get(); got != s.Get() {
				t.Errorf("Value after Wait = %d, want %d", got, s.Get())
			}
		})
	})

	t.Run("UpdateCondWait", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			v := msync.NewValue(30)

			s := v.LoadLink(nil)

			// Verify that a successful StoreCond triggers waiters.
			time.AfterFunc(time.Second, func() {
				if !s.UpdateCond(func(v *int) { *v = 50 }) {
					t.Error("UpdateCond reported false")
				}
			})
			<-v.Wait()
			if got := v.Get(); got != s.Get() {
				t.Errorf("Value after Wait = %d, want %d", got, s.Get())
			}
		})
	})
}

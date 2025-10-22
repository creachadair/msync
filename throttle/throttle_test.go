package throttle_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/creachadair/mds/mtest"
	"github.com/creachadair/mds/value"
	"github.com/creachadair/msync/throttle"
)

func TestThrottle(t *testing.T) {
	t.Run("Basic", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var th throttle.Throttle[int]

			for range 3 {
				v, err := th.Call(t.Context(), func(context.Context) (int, error) {
					return 12345, nil
				})
				if v != 12345 || err != nil {
					t.Errorf("Call: got %v, %v; want 12345, nil", v, err)
				}
			}
		})
	})

	t.Run("Cancelled", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var th throttle.Throttle[int]

			dead, cancel := context.WithCancel(t.Context())
			cancel() // N.B. before starting (that is what we're testing)

			v, err := th.Call(dead, func(context.Context) (int, error) {
				return -1, errors.New("not seen")
			})
			if v != 0 || !errors.Is(err, context.Canceled) {
				t.Errorf("Got %v, %v, want 0, %v", v, err, context.Canceled)
			}
		})
	})

	blockUntilCancelled := func(ctx context.Context) (int, error) {
		<-ctx.Done()
		return 12345, nil
	}

	t.Run("SlowFail", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var th throttle.Throttle[int]

			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
			defer cancel()

			got, err := th.Call(ctx, blockUntilCancelled)
			if got != 0 || !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("Call: got %v, %v; want 0, %v", got, err, context.DeadlineExceeded)
			}
		})
	})

	t.Run("SlowOK", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var th throttle.Throttle[int]
			got, err := th.Call(t.Context(), func(context.Context) (int, error) {
				time.Sleep(10 * time.Second)
				return 12345, nil
			})
			if got != 12345 || err != nil {
				t.Errorf("Call: got %v, %v; want 12345, nil", got, err)
			}
		})
	})

	t.Run("AllGiveUp", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var th throttle.Throttle[int]

			var wg sync.WaitGroup
			for range 100 {
				wg.Go(func() {
					ctx, cancel := context.WithTimeout(t.Context(), rand.N(5*time.Second))
					defer cancel()
					v, err := th.Call(ctx, blockUntilCancelled)
					if v != 0 || !errors.Is(err, context.DeadlineExceeded) {
						t.Errorf("Call: got (%v, %v), want (0, error)", v, err)
					}
				})
			}
			wg.Wait()
		})
	})

	t.Run("Many", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var th throttle.Throttle[string]

			type idKey struct{}
			var active atomic.Int32
			f := func(ctx context.Context) (string, error) {
				id := ctx.Value(idKey{}).(int)

				v := active.Add(1)
				defer active.Add(-1)
				if v != 1 {
					return "ouch", fmt.Errorf("%d active callers", v)
				}
				t.Logf("Caller %d is running the task", id)
				select {
				case <-ctx.Done():
					t.Logf("Caller %d is cancelled", id)
					return "", ctx.Err()
				case <-time.After(5 * time.Second):
					return "OK", nil
				}
			}

			dead, cancel := context.WithCancel(t.Context())
			cancel() // N.B. immediately cancelled, not deferred

			var next int
			var wg sync.WaitGroup
			for range 25 {
				next++
				id := next

				// Arrange for some of the workers to be cancelled.
				isCancel := id%7 == 0
				ctx := value.Cond(isCancel, dead, t.Context())

				wg.Go(func() {
					ctx := context.WithValue(ctx, idKey{}, id)
					v, err := th.Call(ctx, f)
					if errors.Is(err, context.Canceled) {
						// The only workers that should report cancellation are those we
						// arranged to be cancelled. In particular, one worker getting
						// cancelled must not prevent others from getting a value, as
						// long as some of them do survive.
						if !isCancel {
							t.Errorf("Call: unexpected error: %v", err)
						}
					} else if v != "OK" || err != nil {
						t.Errorf("Call: got %q, %v, want OK, nil", v, err)
					}
				})
			}

			wg.Wait()
			if v := active.Load(); v != 0 {
				t.Errorf("Have %d active after all complete", v)
			}
		})
	})
}

func TestSet(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var tset throttle.Set[string, int64]
		ctx := t.Context()

		slowRandom := func(context.Context) (int64, error) {
			time.Sleep(50 * time.Millisecond) // simulate work
			return rand.Int64(), nil
		}

		var wg sync.WaitGroup

		// A bunch of goroutines using the same key should get the same value if
		// they arrive in the same active period.
		got := make([]int64, 25)
		for i := range got {
			wg.Go(func() {
				var err error
				got[i], err = tset.Call(ctx, "apple", slowRandom)
				if err != nil {
					t.Errorf("Call apple: unexpected error: %v", err)
				}
			})
		}

		// We ought not get the same value from a call under a different key.
		var alt int64
		wg.Go(func() {
			var err error
			alt, err = tset.Call(ctx, "pear", slowRandom)
			if err != nil {
				t.Errorf("Call pear: unexpected error: %v", err)
			}
		})

		wg.Wait()

		// Verify that all the concurrent callers got the same value.
		for i, next := range got[1:] {
			if got[i] != next {
				t.Errorf("Result %d: got %d ≠ %d", i, got[i], next)
			}
		}

		// Verify the caller of a different key got a different value.
		if alt == got[0] {
			t.Errorf("Call pear: got %d, want some other value", alt)
		}

		// Now that nobody is using "apple", we ought to get a different value too.
		if alt, err := tset.Call(ctx, "apple", slowRandom); err != nil {
			t.Fatalf("Call apple: unexpected error: %v", err)
		} else if alt == got[0] {
			t.Errorf("Call apple: got %d, want some other value", alt)
		}
	})
}

func TestRaces(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var data struct {
			sync.RWMutex
			v []int
		}
		data.v = make([]int, 1024)
		n := len(data.v)

		write1 := func(pos, v int) throttle.Func[int] {
			return func(context.Context) (int, error) {
				data.Lock()
				defer data.Unlock()
				data.v[pos] = v
				return 0, nil
			}
		}
		var write throttle.Set[int, int]

		var th throttle.Throttle[int]
		sum := func(context.Context) (sum int, _ error) {
			data.RLock()
			defer data.RUnlock()
			for _, v := range data.v {
				sum += v
			}
			return
		}

		// Run several goroutines in parallel contending for access to the throttles
		// managed by write and sum. This gives the race and deadlock detectors
		// something to push against.
		const numTasks = 32
		const numOps = 200

		var wg sync.WaitGroup
		for range numTasks {
			wg.Go(func() {
				var rnd uint32
				for range numOps {
					if rnd == 0 {
						rnd = rand.Uint32()
					}
					switch rnd & 1 {
					case 0:
						pos := rand.N(n)
						write.Call(t.Context(), pos, write1(pos, rand.N(200000)))
					case 1:
						th.Call(t.Context(), sum)
					}
					rnd >>= 1
				}
			})
		}
		wg.Wait()
	})
}

func TestAdapt(t *testing.T) {
	testErr := errors.New("test error")

	t.Run("Thunk", func(t *testing.T) {
		v, err := throttle.Adapt[int](func() {})(t.Context())
		if v != 0 || err != nil {
			t.Errorf("Got %v, %v; want 0, nil", v, err)
		}
	})
	t.Run("CtxThunk", func(t *testing.T) {
		v, err := throttle.Adapt[bool](func(context.Context) {})(t.Context())
		if v != false || err != nil {
			t.Errorf("Got %v, %v; want false, nil", v, err)
		}
	})
	t.Run("ErrOnly", func(t *testing.T) {
		v, err := throttle.Adapt[any](func() error { return testErr })(t.Context())
		if v != nil || err != testErr {
			t.Fatalf("Got %v, %v; want nil, %v", v, err, testErr)
		}
	})
	t.Run("CtxErrOnly", func(t *testing.T) {
		v, err := throttle.Adapt[any](func(ctx context.Context) error { return testErr })(t.Context())
		if v != nil || err != testErr {
			t.Fatalf("Got %v, %v; want nil, %v", v, err, testErr)
		}
	})
	t.Run("ValOnly", func(t *testing.T) {
		v, err := throttle.Adapt[int](func() int { return 25 })(t.Context())
		if v != 25 || err != nil {
			t.Fatalf("Got %d, %v; want 25, nil", v, err)
		}
	})
	t.Run("CtxValOnly", func(t *testing.T) {
		v, err := throttle.Adapt[int](func(context.Context) int { return 25 })(t.Context())
		if v != 25 || err != nil {
			t.Fatalf("Got %d, %v; want 25, nil", v, err)
		}
	})
	t.Run("ValError", func(t *testing.T) {
		v, err := throttle.Adapt[int](func() (int, error) { return 25, nil })(t.Context())
		if v != 25 || err != nil {
			t.Fatalf("Got %d, %v; want 25, nil", v, err)
		}
	})
	t.Run("CtxValError", func(t *testing.T) {
		v, err := throttle.Adapt[int](func(context.Context) (int, error) { return 25, nil })(t.Context())
		if v != 25 || err != nil {
			t.Fatalf("Got %d, %v; want 25, nil", v, err)
		}
	})
	t.Run("Invalid", func(t *testing.T) {
		for _, tc := range []any{"string", 25, func(int) {}, func(byte) byte { return 0 }} {
			mtest.MustPanicf(t, func() { throttle.Adapt[any](tc) }, "expected Adapt to panic for %T", tc)
		}
	})
}

package throttle_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/creachadair/msync/throttle"
	"github.com/fortytw2/leaktest"
)

func TestThrottle(t *testing.T) {
	defer leaktest.Check(t)()

	t.Run("Basic", func(t *testing.T) {
		th := throttle.New(func(context.Context) (int, error) {
			return 12345, nil
		})
		for range 3 {
			if v, err := th.Call(context.Background()); v != 12345 || err != nil {
				t.Errorf("Call: got %v, %v; want 12345, nil", v, err)
			}
		}
	})

	t.Run("Cancelled", func(t *testing.T) {
		th := throttle.New(func(context.Context) (int, error) {
			return -1, errors.New("not seen")
		})
		dead, cancel := context.WithCancel(context.Background())
		cancel() // N.B. before starting (that is what we're testing)

		v, err := th.Call(dead)
		if v != 0 || !errors.Is(err, context.Canceled) {
			t.Errorf("Got %v, %v, want 0, %v", v, err, context.Canceled)
		}
	})

	const shortTime = 10 * time.Millisecond
	const longTime = 5 * shortTime

	t.Run("Slow", func(t *testing.T) {
		th := throttle.New(func(ctx context.Context) (int, error) {
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-time.After(longTime):
				return 12345, nil
			}
		})

		t.Run("Fail", func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), shortTime)
			defer cancel()

			got, err := th.Call(ctx)
			if got != 0 || !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("Call: got %v, %v; want 0, %v", got, err, context.DeadlineExceeded)
			}
		})
		t.Run("OK", func(t *testing.T) {
			got, err := th.Call(context.Background())
			if got != 12345 || err != nil {
				t.Errorf("Call: got %v, %v; want 12345, nil", got, err)
			}
		})
	})

	t.Run("AllGiveUp", func(t *testing.T) {
		done := make(chan struct{})
		th := throttle.New(func(ctx context.Context) (bool, error) {
			select {
			case <-ctx.Done():
				return false, ctx.Err()
			case <-done:
				return true, nil
			}
		})

		var wg sync.WaitGroup
		for range 5 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(context.Background(), rand.N(5*time.Millisecond))
				defer cancel()
				v, err := th.Call(ctx)
				if v || err == nil {
					t.Errorf("Call: got (%v, %v), want (false, error)", v, err)
				}
			}()
		}
		wg.Wait()
		close(done)
	})

	t.Run("Many", func(t *testing.T) {
		type idKey struct{}

		var active atomic.Int32
		th := throttle.New(func(ctx context.Context) (string, error) {
			id := ctx.Value(idKey{}).(int)

			v := active.Add(1)
			defer active.Add(-1)
			if v != 1 {
				return "ouch", fmt.Errorf("%d active callers", v)
			}
			t.Logf("Caller %d is running the task", id)
			time.Sleep(5 * time.Microsecond)
			return "OK", nil
		})

		var next int
		var wg sync.WaitGroup
		for range 25 {
			next++
			id := next

			wg.Add(1)
			go func() {
				defer wg.Done()

				ctx := context.WithValue(context.Background(), idKey{}, id)
				v, err := th.Call(ctx)
				if v != "OK" || err != nil {
					t.Errorf("Call: got %q, %v, want OK, nil", v, err)
				}
			}()
		}

		wg.Wait()
		if v := active.Load(); v != 0 {
			t.Errorf("Have %d active after all complete", v)
		}
	})
}

func TestSet(t *testing.T) {
	var tset throttle.Set[string, int]
	ctx := context.Background()

	slowRandom := func(context.Context) (int, error) {
		// Wait long enough that it is "sufficiently likely" all our probe calls
		// will land on the same throttle.
		time.Sleep(50 * time.Millisecond)
		return rand.IntN(20000), nil
	}

	var wg sync.WaitGroup

	// A bunch of goroutines using the same key should get the same value if
	// they arrive in the same active period.
	got := make([]int, 5)
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var err error
			got[i], err = tset.Call(ctx, "apple", slowRandom)
			if err != nil {
				t.Errorf("Call apple: unexpected error: %v", err)
			}
		}()
	}

	// We ought not get the same value from a call under a different key.
	var alt int
	wg.Add(1)
	go func() {
		defer wg.Done()
		var err error
		alt, err = tset.Call(ctx, "pear", slowRandom)
		if err != nil {
			t.Errorf("Call pear: unexpected error: %v", err)
		}
	}()

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
}

func TestRaces(t *testing.T) {
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

	sum := throttle.New(func(context.Context) (sum int, _ error) {
		data.RLock()
		defer data.RUnlock()
		for _, v := range data.v {
			sum += v
		}
		return
	})

	// Run several goroutines in parallel contending for access to the throttles
	// managed by write and sum. This gives the race and deadlock detectors
	// something to push against.
	const numTasks = 32
	const numOps = 200

	ctx := context.Background()
	var wg sync.WaitGroup
	for range numTasks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var rnd uint32
			for range numOps {
				if rnd == 0 {
					rnd = rand.Uint32()
				}
				switch rnd & 1 {
				case 0:
					pos := rand.N(n)
					write.Call(ctx, pos, write1(pos, rand.N(200000)))
				case 1:
					sum.Call(ctx)
				}
				rnd >>= 1
			}
		}()
	}
	wg.Wait()
}

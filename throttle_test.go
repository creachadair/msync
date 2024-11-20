package msync_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/creachadair/msync"
	"github.com/fortytw2/leaktest"
)

func TestThrottle(t *testing.T) {
	defer leaktest.Check(t)()

	t.Run("Basic", func(t *testing.T) {
		th := msync.NewThrottle(func(context.Context) (int, error) {
			return 12345, nil
		})
		for range 3 {
			if v, err := th.Call(context.Background()); v != 12345 || err != nil {
				t.Errorf("Call: got %v, %v; want 12345, nil", v, err)
			}
		}
	})

	t.Run("Slow", func(t *testing.T) {
		th := msync.NewThrottle(func(ctx context.Context) (int, error) {
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-time.After(50 * time.Millisecond):
				return 12345, nil
			}
		})

		t.Run("Fail", func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
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

	t.Run("Many", func(t *testing.T) {
		type idKey struct{}

		var active atomic.Int32
		th := msync.NewThrottle(func(ctx context.Context) (string, error) {
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

	t.Run("Panic", func(t *testing.T) {
		ready := make(chan struct{})
		th := msync.NewThrottle(func(context.Context) (bool, error) {
			<-ready
			panic("oh no")
		})

		var start, finish sync.WaitGroup
		start.Add(3)
		finish.Add(3)
		for range 3 {
			go func() {
				defer finish.Done()
				start.Done()
				v, err := th.Call(context.Background())
				if err == nil {
					t.Errorf("Call: got %v, want error", v)
				}
			}()
		}
		start.Wait()
		close(ready)
		finish.Wait()
	})
}

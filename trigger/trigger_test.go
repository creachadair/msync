package trigger_test

import (
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/creachadair/msync/trigger"
)

func TestTrigger(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		checkNotActive := func(t *testing.T, tr *trigger.Cond) {
			t.Helper()
			select {
			case <-tr.Ready():
				t.Error("Trigger is active when it should not be")
			default:
			}
		}
		checkActive := func(t *testing.T, tr *trigger.Cond, msg string, args ...any) {
			t.Helper()
			select {
			case <-tr.Ready():
				t.Logf(msg, args...)
			default:
				t.Error("Trigger is not ready when it should be")
			}
		}

		func() {
			t.Log("Signal")
			// Start up a bunch of tasks that listen to a trigger, signal the trigger,
			// and verify that it woke them all up.
			tr := trigger.New()
			checkNotActive(t, tr)

			const numTasks = 25

			ok := make([]bool, numTasks)

			var wg sync.WaitGroup
			for i := range numTasks {
				wg.Go(func() {
					<-tr.Ready()
					ok[i] = true
				})
			}

			// Wait until all the tasks have their channel.
			synctest.Wait()

			// Signal the trigger, and confirm that it is no longer active.
			checkNotActive(t, tr)
			tr.Signal()
			checkNotActive(t, tr)

			// Wait until all the tasks have completed.
			wg.Wait()

			for i, b := range ok {
				if !b {
					t.Errorf("Task %d did not report success", i+1)
				}
			}
		}()

		func() {
			t.Log("Set")
			tr := trigger.New()
			checkNotActive(t, tr)

			// Verify that a goroutine that observes the trigger before the first set
			// properly observes the activation later.
			go func() {
				<-tr.Ready()
				t.Log("OK, early observer saw the trigger fire")
			}()

			synctest.Wait()

			tr.Set()
			checkActive(t, tr, "OK, set trigger is active")
			tr.Set() // safe to do it multiple times
			for i := range 3 {
				time.Sleep(time.Millisecond)
				checkActive(t, tr, "OK, set trigger is still active (check %d)", i+1)
			}

			synctest.Wait()

			tr.Reset()
			checkNotActive(t, tr)
			tr.Reset() // safe to do it multiple times
			checkNotActive(t, tr)
		}()
	})
}

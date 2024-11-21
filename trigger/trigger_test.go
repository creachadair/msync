package trigger_test

import (
	"sync"
	"testing"
	"time"

	"github.com/creachadair/msync/trigger"
	"github.com/fortytw2/leaktest"
)

func TestTrigger(t *testing.T) {
	defer leaktest.Check(t)()

	checkNotActive := func(t *testing.T, tr *trigger.Cond) {
		t.Helper()
		select {
		case <-tr.Ready():
			t.Error("Trigger is active when it should not be")
		default:
		}
	}

	t.Run("Signal", func(t *testing.T) {
		// Start up a bunch of tasks that listen to a trigger, signal the trigger,
		// and verify that it woke them all up.
		tr := trigger.New()
		checkNotActive(t, tr)

		const numTasks = 5

		ok := make([]bool, numTasks)
		var start, stop sync.WaitGroup

		for i := range numTasks {
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

		// Wait until all the tasks have their channel.
		start.Wait()

		// Signal the trigger, and confirm that it is no longer active.
		checkNotActive(t, tr)
		tr.Signal()
		checkNotActive(t, tr)

		// Wait until all the tasks have completed.
		stop.Wait()

		for i, b := range ok {
			if !b {
				t.Errorf("Task %d did not report success", i+1)
			}
		}
	})

	t.Run("Set", func(t *testing.T) {
		tr := trigger.New()
		checkNotActive(t, tr)

		// Verify that a goroutine that observes the trigger before the first set
		// properly observes the activation later.
		start := make(chan struct{})
		done := make(chan struct{})
		go func() {
			ch := tr.Ready()
			close(start)
			<-ch
			close(done)
		}()
		<-start

		tr.Set()
		tr.Set() // safe to do it multiple times
		for i := range 3 {
			select {
			case <-tr.Ready():
				t.Logf("OK, set trigger is active (check %d)", i+1)
			case <-time.After(time.Second):
				t.Error("Trigger is not ready when it should be")
			}
		}
		select {
		case <-done:
			t.Log("OK, early observer saw the trigger fire")
		case <-time.After(time.Second):
			t.Error("Early observer did not see the activation")
		}

		tr.Reset()
		checkNotActive(t, tr)
		tr.Reset() // safe to do it multiple times
		checkNotActive(t, tr)
	})
}

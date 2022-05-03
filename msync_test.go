package msync_test

import (
	"sync"
	"testing"
	"time"

	"github.com/creachadair/msync"
)

func TestTrigger(t *testing.T) {
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
	h := msync.NewHandoff[int]()

	// Multiple sends to h do not block.
	h.Send(1)
	h.Send(2)
	h.Send(3)

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
	h.Send(4)
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

	h.Send(1)
	<-p.Ready()
	h.Send(3)
	<-p.Ready()
	h.Send(5)
	<-p.Ready()
	<-done

	if sum != 9 {
		t.Errorf("Checksum: got %v, want 9", sum)
	}
}

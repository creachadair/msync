package trigger_test

import (
	"fmt"
	"sync"

	"github.com/creachadair/msync/trigger"
)

func printReady(c *trigger.Cond) {
	select {
	case <-c.Ready():
		fmt.Println("condition ready")
	default:
		fmt.Println("condition not ready")
	}
}

func ExampleCond() {
	var wg sync.WaitGroup
	defer wg.Wait()

	var c trigger.Cond

	// Various tasks can wait for a condition to be ready.
	r1 := c.Ready()
	wg.Go(func() { <-r1; fmt.Println("task 1: ready") })

	// When the condition is set, any waiters are woken.
	c.Set()

	// Once set, the condition remains set until reset.
	printReady(&c)

	// Once reset, the task is no longer ready.
	c.Reset()
	printReady(&c)

	// Signaling the condition wakes any goroutines that are already waiting,
	// then immediately resets the condition so that later arrivals will wait
	// for a subsequent signal.
	r2 := c.Ready()
	wg.Go(func() { <-r2; fmt.Println("task 2: ready") })

	fmt.Println("signal 1")
	c.Signal()

	// This goroutine arrives late, and will not be woken by the prior signal.
	r3 := c.Ready()
	wg.Go(func() { <-r3; fmt.Println("task 3: ready") })

	printReady(&c)

	// This signal will now wake the later arrival (task 3).
	fmt.Println("signal 2")
	c.Signal()

	// Unordered output:
	// task 1: ready
	// condition ready
	// condition not ready
	// signal 1
	// task 2: ready
	// condition not ready
	// signal 2
	// task 3: ready
}

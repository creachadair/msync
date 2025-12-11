package msync_test

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/creachadair/msync"
)

func ExampleValue() {
	var s msync.Value[string]

	var wg sync.WaitGroup
	defer wg.Wait()

	// Callers can use Set to modify the value concurrently.
	wg.Go(func() { s.Set("apple") })
	wg.Go(func() { s.Set("pear") })
	wg.Go(func() { s.Set("plum") })
	wg.Go(func() { s.Set("cherry") })

	// Use Wait to block until a new value becomes available.
	select {
	case <-time.After(10 * time.Second):
		log.Fatal("Timeout waiting for value to change")
	case v := <-s.Wait():
		fmt.Println(v)
	}

	// Use Get to fetch the current value at any time.
	fmt.Println(s.Get())
}

func ExampleLink() {
	s := msync.NewValue(1)

	// Links support a form of optimistic transaction control, allowing the
	// caller do detect whether its view of state has changed due to the actions
	// of other goroutines.
	//
	// Call LoadLink to obtain a view. The view is a snapshot of the value at
	// the moment the link was taken.
	v := s.LoadLink(nil)
	fmt.Printf("snapshot: %d\n", v.Get())

	// After doing some work, we would like to update s to some new value, but
	// only if s has not been separately modified (which means our view is no
	// longer current). Call StoreCond to update the value if the view is still
	// current:
	if v.StoreCond(2) {
		fmt.Printf("update succeeded, now %d\n", v.Get())
	}

	// A successful update acts as a "commit", and the link becomes invalid.
	if v.Validate() {
		log.Fatal("unexpected")
	}

	// We can re-link to get a new snapshot. It is OK to reuse a link.
	s.LoadLink(v)

	// Time passes. While we are working, another goroutine modifies the value.
	// This call is not in a separate goroutine, but simulates the effect.
	s.Set(4)

	// After completing our work, we want to update, but now our snapshot is no
	// longer current, so the update will fail. We can retry or report an error.
	if v.StoreCond(3) {
		log.Fatal("unexpected")
	}

	// A failed update invalidates the link.
	if v.Validate() {
		log.Fatal("unexpected")
	}

	// The underlying value has the last successful update.
	fmt.Printf("view now: %d\n", v.Get())
	fmt.Printf("value now: %d\n", s.Get())

	// We can also use a Link to check whether our view is still current without
	// updating the value:
	s.LoadLink(v)

	// ... time passes ...

	// Validate reports whether StoreCond would have succeeded, but does not
	// actually modify the underlying value. If Validate reports true, the view
	// is still current as of this moment.
	fmt.Printf("view %d still current: %v\n", v.Get(), v.Validate())

	// Output:
	// snapshot: 1
	// update succeeded, now 2
	// view now: 2
	// value now: 4
	// view 4 still current: true
}

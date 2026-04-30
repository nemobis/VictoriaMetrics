package syncwg

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWaitGroupBasic(t *testing.T) {
	var wg WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
	}()
	wg.Wait()
}

func TestWaitGroupMultipleWorkers(t *testing.T) {
	var wg WaitGroup
	const n = 10
	var counter atomic.Int64
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			counter.Add(1)
		}()
	}
	wg.Wait()
	if got := counter.Load(); got != n {
		t.Fatalf("expected %d workers to finish, got %d", n, got)
	}
}

func TestWaitGroupConcurrentAdd(t *testing.T) {
	var wg WaitGroup
	const n = 50
	var mu sync.Mutex
	var counter int
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mu.Lock()
			counter++
			mu.Unlock()
		}()
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if counter != n {
		t.Fatalf("expected counter=%d, got %d", n, counter)
	}
}

func TestWaitGroupWaitBlocks(t *testing.T) {
	var wg WaitGroup
	done := make(chan struct{})
	wg.Add(1)

	go func() {
		wg.Wait()
		close(done)
	}()

	// Ensure Wait is actually blocking
	select {
	case <-done:
		t.Fatal("Wait returned before Done was called")
	case <-time.After(50 * time.Millisecond):
		// Good - Wait is blocking
	}

	wg.Done()

	select {
	case <-done:
		// Good - Wait unblocked after Done
	case <-time.After(time.Second):
		t.Fatal("Wait did not unblock after Done")
	}
}

func TestWaitGroupWaitAndBlock(t *testing.T) {
	var wg WaitGroup
	var counter atomic.Int64

	const n = 5
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			counter.Add(1)
		}()
	}

	// WaitAndBlock should wait for all goroutines and then block further Add calls
	wg.WaitAndBlock()

	// All goroutines should have finished
	if got := counter.Load(); got != n {
		t.Fatalf("expected counter=%d after WaitAndBlock, got %d", n, got)
	}

	// After WaitAndBlock, Add should block. Verify by trying in a goroutine with timeout.
	addBlocked := make(chan struct{})
	go func() {
		wg.Add(1) // This should block forever since mu is held
		close(addBlocked)
	}()

	select {
	case <-addBlocked:
		t.Fatal("Add should be permanently blocked after WaitAndBlock, but it returned")
	case <-time.After(100 * time.Millisecond):
		// Good - Add is blocked
	}
}

func TestWaitGroupDoneIsGoroutineSafe(t *testing.T) {
	// This test verifies that Done() can be called from concurrent goroutines
	var wg WaitGroup
	const n = 100
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			wg.Done()
		}()
	}
	wg.Wait()
}

func TestWaitGroupMultipleWaits(t *testing.T) {
	// Multiple goroutines can wait concurrently
	var wg WaitGroup
	var waitersReady sync.WaitGroup
	waitersReady.Add(3)

	waitDone := make(chan struct{}, 3)
	for i := 0; i < 3; i++ {
		go func() {
			waitersReady.Done()
			wg.Wait()
			waitDone <- struct{}{}
		}()
	}

	// Ensure workers actually run
	wg.Add(1)
	waitersReady.Wait()

	// All 3 goroutines should be blocking on Wait
	select {
	case <-waitDone:
		t.Fatal("Wait returned before Done")
	case <-time.After(50 * time.Millisecond):
	}

	wg.Done()

	// All waiters should unblock
	for i := 0; i < 3; i++ {
		select {
		case <-waitDone:
		case <-time.After(time.Second):
			t.Fatalf("waiter %d did not unblock", i)
		}
	}
}

func TestWaitGroupZeroAdd(t *testing.T) {
	var wg WaitGroup
	// Wait on empty group should return immediately
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		// Good
	case <-time.After(time.Second):
		t.Fatal("Wait on zero WaitGroup should return immediately")
	}
}

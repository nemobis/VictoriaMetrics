package ratelimiter

// Unit tests for ratelimiter.go
//
// RateLimiter.Register() is the only exported method (besides New).
// Key behaviours exercised:
//   - nil receiver is a no-op.
//   - perSecondLimit <= 0 is a no-op (no blocking).
//   - Under the budget, Register() returns immediately.
//   - Exceeding the budget causes blocking until the next second-window resets
//     the budget; closing stopCh unblocks Register() early.
//   - limitReached counter is incremented when the budget is exhausted.
//   - Multiple goroutines calling Register() concurrently do not race.

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VictoriaMetrics/metrics"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

var counterSeq atomic.Int64

// newCounter returns a fresh *metrics.Counter registered under a unique name
// to avoid "already registered" panics across tests.
func newCounter() *metrics.Counter {
	id := counterSeq.Add(1)
	name := fmt.Sprintf(`test_ratelimiter_ctr_%d`, id)
	return metrics.NewCounter(name)
}

// makeRL creates a RateLimiter with the given per-second limit and a fresh
// stopCh.  The caller must close the returned channel when done.
func makeRL(limit int64) (*RateLimiter, chan struct{}) {
	stopCh := make(chan struct{})
	return New(limit, newCounter(), stopCh), stopCh
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestNilReceiver verifies that Register on a nil *RateLimiter is a no-op and
// does not panic.
func TestNilReceiver(t *testing.T) {
	var rl *RateLimiter
	rl.Register(100) // must not panic
}

// TestZeroLimit verifies that Register returns immediately when the
// perSecondLimit is 0.
func TestZeroLimit(t *testing.T) {
	rl, stop := makeRL(0)
	defer close(stop)

	done := make(chan struct{})
	go func() {
		rl.Register(1_000_000)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Register with limit=0 blocked unexpectedly")
	}
}

// TestNegativeLimit verifies that Register returns immediately when the limit
// is negative.
func TestNegativeLimit(t *testing.T) {
	rl, stop := makeRL(-1)
	defer close(stop)

	done := make(chan struct{})
	go func() {
		rl.Register(1)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Register with limit<0 blocked unexpectedly")
	}
}

// TestRegisterWithinBudget verifies that calls that stay within the per-second
// budget complete promptly (no blocking).
func TestRegisterWithinBudget(t *testing.T) {
	const limit = 1_000_000
	rl, stop := makeRL(limit)
	defer close(stop)

	start := time.Now()
	rl.Register(100)
	rl.Register(200)
	rl.Register(300)
	elapsed := time.Since(start)
	if elapsed > 500*time.Millisecond {
		t.Errorf("Register within budget took too long: %v", elapsed)
	}
}

// TestStopChUnblocksRegister verifies that closing stopCh causes a blocked
// Register to return promptly.
func TestStopChUnblocksRegister(t *testing.T) {
	// limit=1: the first Register drains the budget; the second will block.
	const limit = 1
	rl, stop := makeRL(limit)

	// Drain the initial budget.
	rl.Register(1)

	done := make(chan struct{})
	go func() {
		rl.Register(1) // blocks here
		close(done)
	}()

	// Give the goroutine time to enter the blocking select.
	time.Sleep(50 * time.Millisecond)
	close(stop)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Register did not unblock after closing stopCh")
	}
}

// TestLimitReachedCounter verifies that the limitReached counter is incremented
// at least once when the budget is exhausted.
func TestLimitReachedCounter(t *testing.T) {
	stopCh := make(chan struct{})
	ctr := newCounter()
	rl := New(1, ctr, stopCh)

	// Drain budget.
	rl.Register(1)

	done := make(chan struct{})
	go func() {
		rl.Register(1) // will block, increment counter, then unblock on close
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	close(stopCh)
	<-done

	if ctr.Get() == 0 {
		t.Error("limitReached counter was not incremented when budget was exhausted")
	}
}

// TestConcurrentRegister verifies that concurrent Register calls from many
// goroutines do not race and all eventually complete.
func TestConcurrentRegister(t *testing.T) {
	const limit = 10_000_000
	rl, stop := makeRL(limit)
	defer close(stop)

	var wg sync.WaitGroup
	const goroutines = 20
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rl.Register(1)
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent Register calls did not all complete within 2s")
	}
}

// TestNewReturnsNonNil verifies that New always returns a non-nil *RateLimiter.
func TestNewReturnsNonNil(t *testing.T) {
	stopCh := make(chan struct{})
	defer close(stopCh)
	rl := New(100, newCounter(), stopCh)
	if rl == nil {
		t.Fatal("New returned nil")
	}
}

// TestRegisterLargeCount verifies that registering a count larger than the
// per-second limit still completes (after one budget refresh) when stopCh is
// closed to avoid indefinite blocking.
func TestRegisterLargeCount(t *testing.T) {
	const limit = 10
	rl, stop := makeRL(limit)

	done := make(chan struct{})
	go func() {
		rl.Register(1000) // larger than limit; will block for budget refreshes
		close(done)
	}()

	// Close stop after a short wait to unblock the goroutine.
	time.Sleep(20 * time.Millisecond)
	close(stop)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Register with large count did not unblock after stopCh closed")
	}
}

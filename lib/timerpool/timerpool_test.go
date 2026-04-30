package timerpool

import (
	"testing"
	"time"
)

func TestGetPut(t *testing.T) {
	d := 10 * time.Millisecond
	timer := Get(d)
	if timer == nil {
		t.Fatal("Get returned nil timer")
	}
	Put(timer)
}

func TestTimerFires(t *testing.T) {
	d := 20 * time.Millisecond
	timer := Get(d)
	defer Put(timer)

	select {
	case <-timer.C:
		// timer fired as expected
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timer did not fire within expected duration")
	}
}

func TestTimerReuse(t *testing.T) {
	// Get, put back, get again – the second Get should reuse the pooled timer.
	timer := Get(10 * time.Millisecond)
	Put(timer)

	timer2 := Get(20 * time.Millisecond)
	if timer2 == nil {
		t.Fatal("second Get returned nil timer")
	}
	defer Put(timer2)

	select {
	case <-timer2.C:
		// timer fired
	case <-time.After(500 * time.Millisecond):
		t.Fatal("reused timer did not fire within expected duration")
	}
}

func TestPutStopsTimer(t *testing.T) {
	// A timer with a very long duration; after Put it must be stopped so C
	// never receives a value.
	timer := Get(10 * time.Second)
	Put(timer)

	select {
	case <-timer.C:
		t.Fatal("timer channel should not receive after Put")
	default:
		// correct: channel is empty
	}
}

func TestGetZeroDuration(t *testing.T) {
	// Duration 0 is valid; the timer fires immediately.
	timer := Get(0)
	defer Put(timer)

	select {
	case <-timer.C:
		// expected
	case <-time.After(500 * time.Millisecond):
		t.Fatal("zero-duration timer did not fire")
	}
}

func TestConcurrentGetPut(t *testing.T) {
	const goroutines = 50
	done := make(chan struct{}, goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			timer := Get(5 * time.Millisecond)
			<-timer.C
			Put(timer)
			done <- struct{}{}
		}()
	}
	for i := 0; i < goroutines; i++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for goroutine")
		}
	}
}

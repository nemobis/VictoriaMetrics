package contextutil

import (
	"context"
	"testing"
	"time"
)

func TestNewStopChanContextNotCanceled(t *testing.T) {
	stopCh := make(chan struct{})
	ctx, cancel := NewStopChanContext(stopCh)
	defer cancel()

	// Context must not be done yet.
	select {
	case <-ctx.Done():
		t.Fatal("context should not be done before stopCh is closed or cancel is called")
	default:
	}

	if err := ctx.Err(); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestNewStopChanContextCanceledByStopCh(t *testing.T) {
	stopCh := make(chan struct{})
	ctx, cancel := NewStopChanContext(stopCh)
	defer cancel()

	close(stopCh)

	select {
	case <-ctx.Done():
		// expected
	case <-time.After(time.Second):
		t.Fatal("context was not canceled after stopCh was closed")
	}
}

func TestNewStopChanContextCanceledByCancel(t *testing.T) {
	stopCh := make(chan struct{}) // never closed
	ctx, cancel := NewStopChanContext(stopCh)

	cancel()

	select {
	case <-ctx.Done():
		// expected
	case <-time.After(time.Second):
		t.Fatal("context was not canceled after cancel() was called")
	}
}

func TestNewStopChanContextErrAfterStopCh(t *testing.T) {
	stopCh := make(chan struct{})
	ctx, cancel := NewStopChanContext(stopCh)
	defer cancel()

	close(stopCh)

	// Give context.WithCancel a moment to propagate.
	time.Sleep(10 * time.Millisecond)

	if err := ctx.Err(); err == nil {
		t.Fatal("expected non-nil error after stopCh closed, got nil")
	}
}

func TestStopChanContextDeadline(t *testing.T) {
	stopCh := make(chan struct{})
	inner := &stopChanContext{stopCh: stopCh}

	deadline, ok := inner.Deadline()
	if ok {
		t.Fatal("expected no deadline")
	}
	if !deadline.IsZero() {
		t.Fatal("expected zero deadline time")
	}
}

func TestStopChanContextValue(t *testing.T) {
	stopCh := make(chan struct{})
	inner := &stopChanContext{stopCh: stopCh}

	if v := inner.Value("key"); v != nil {
		t.Fatalf("expected nil value, got %v", v)
	}
}

func TestStopChanContextDone(t *testing.T) {
	stopCh := make(chan struct{})
	inner := &stopChanContext{stopCh: stopCh}

	if inner.Done() != stopCh {
		t.Fatal("Done() should return the stopCh channel")
	}
}

func TestStopChanContextErrOpenChannel(t *testing.T) {
	stopCh := make(chan struct{})
	inner := &stopChanContext{stopCh: stopCh}

	if err := inner.Err(); err != nil {
		t.Fatalf("expected nil error on open channel, got %v", err)
	}
}

func TestStopChanContextErrClosedChannel(t *testing.T) {
	stopCh := make(chan struct{})
	close(stopCh)
	inner := &stopChanContext{stopCh: stopCh}

	if err := inner.Err(); err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

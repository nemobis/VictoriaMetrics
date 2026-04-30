package limiter

import (
	"bytes"
	"io"
	"testing"
	"time"
)

func TestNewLimiter(t *testing.T) {
	l := NewLimiter(1000)
	if l == nil {
		t.Fatal("NewLimiter returned nil")
	}
	if l.perSecondLimit != 1000 {
		t.Fatalf("perSecondLimit: got %d, want 1000", l.perSecondLimit)
	}
}

func TestLimiterRegisterNoLimit(t *testing.T) {
	// With limit <= 0, Register should return immediately without blocking
	l := NewLimiter(0)
	start := time.Now()
	l.Register(1_000_000)
	elapsed := time.Since(start)
	if elapsed > 100*time.Millisecond {
		t.Fatalf("Register with limit=0 should not block; took %v", elapsed)
	}
}

func TestLimiterRegisterNegativeLimit(t *testing.T) {
	l := NewLimiter(-1)
	start := time.Now()
	l.Register(1_000_000)
	elapsed := time.Since(start)
	if elapsed > 100*time.Millisecond {
		t.Fatalf("Register with limit=-1 should not block; took %v", elapsed)
	}
}

func TestLimiterRegisterWithinBudget(t *testing.T) {
	// Large limit so we never exhaust the budget in a single call
	l := NewLimiter(1_000_000_000)
	start := time.Now()
	l.Register(100)
	elapsed := time.Since(start)
	if elapsed > 500*time.Millisecond {
		t.Fatalf("Register within budget should be fast; took %v", elapsed)
	}
}

func TestLimiterMultipleRegistrations(t *testing.T) {
	l := NewLimiter(1_000_000_000)
	for i := 0; i < 10; i++ {
		l.Register(1000)
	}
}

func TestNewWriteLimiter(t *testing.T) {
	var buf bytes.Buffer
	l := NewLimiter(0)
	wl := NewWriteLimiter(&buf, l)
	if wl == nil {
		t.Fatal("NewWriteLimiter returned nil")
	}
}

func TestWriteLimiterWrite(t *testing.T) {
	var buf bytes.Buffer
	l := NewLimiter(0) // no limit
	wl := NewWriteLimiter(&buf, l)

	data := []byte("hello, world")
	n, err := wl.Write(data)
	if err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if n != len(data) {
		t.Fatalf("Write: got n=%d, want %d", n, len(data))
	}
	if got := buf.String(); got != string(data) {
		t.Fatalf("Write: buf=%q, want %q", got, string(data))
	}
}

func TestWriteLimiterWriteMultiple(t *testing.T) {
	var buf bytes.Buffer
	l := NewLimiter(1_000_000_000)
	wl := NewWriteLimiter(&buf, l)

	for _, chunk := range []string{"foo", "bar", "baz"} {
		n, err := wl.Write([]byte(chunk))
		if err != nil {
			t.Fatalf("Write returned error: %v", err)
		}
		if n != len(chunk) {
			t.Fatalf("Write: got n=%d, want %d", n, len(chunk))
		}
	}
	if got := buf.String(); got != "foobarbaz" {
		t.Fatalf("buffer: got %q, want %q", got, "foobarbaz")
	}
}

// closeWriter is an io.WriteCloser for testing Close behavior
type closeWriter struct {
	bytes.Buffer
	closed bool
}

func (cw *closeWriter) Close() error {
	cw.closed = true
	return nil
}

func TestWriteLimiterCloseWithCloser(t *testing.T) {
	cw := &closeWriter{}
	l := NewLimiter(0)
	wl := NewWriteLimiter(cw, l)
	if err := wl.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if !cw.closed {
		t.Fatal("Close should have called underlying writer's Close")
	}
}

func TestWriteLimiterCloseWithoutCloser(t *testing.T) {
	// bytes.Buffer doesn't implement io.Closer
	var buf bytes.Buffer
	l := NewLimiter(0)
	wl := NewWriteLimiter(&buf, l)
	if err := wl.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
}

func TestWriteLimiterImplementsInterfaces(t *testing.T) {
	var buf bytes.Buffer
	l := NewLimiter(0)
	wl := NewWriteLimiter(&buf, l)
	// Verify it implements io.Writer and io.Closer
	var _ io.Writer = wl
	var _ io.Closer = wl
}

func TestLimiterBudgetRefill(t *testing.T) {
	// Use a very small limit so we exhaust the budget quickly and can observe refill
	// Set limit = 10 bytes/sec. Register 5 bytes twice: first should be quick,
	// second would be quick too (budget goes negative but refills).
	// We just verify it doesn't panic or deadlock with a small data size.
	l := NewLimiter(10)
	done := make(chan struct{})
	go func() {
		l.Register(5)
		l.Register(5)
		// The third register after exhausting budget will block for ~1 sec
		// Don't call it here to keep test fast
		close(done)
	}()
	select {
	case <-done:
		// Good
	case <-time.After(5 * time.Second):
		t.Fatal("Limiter.Register deadlocked")
	}
}

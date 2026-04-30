package bufferedwriter

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

// TestGetPut verifies Get returns a working Writer and Put returns it to the pool.
func TestGetPut(t *testing.T) {
	var buf bytes.Buffer
	bw := Get(&buf)
	if bw == nil {
		t.Fatal("Get returned nil")
	}
	data := []byte("hello world")
	n, err := bw.Write(data)
	if err != nil {
		t.Fatalf("unexpected Write error: %v", err)
	}
	if n != len(data) {
		t.Fatalf("Write returned %d, want %d", n, len(data))
	}
	if err := bw.Flush(); err != nil {
		t.Fatalf("unexpected Flush error: %v", err)
	}
	if got := buf.String(); got != "hello world" {
		t.Fatalf("buffer contains %q, want %q", got, "hello world")
	}
	Put(bw)
}

// TestWriteEmpty verifies that writing an empty slice is a no-op.
func TestWriteEmpty(t *testing.T) {
	var buf bytes.Buffer
	bw := Get(&buf)
	defer Put(bw)

	n, err := bw.Write([]byte{})
	if err != nil {
		t.Fatalf("unexpected error writing empty slice: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 bytes written, got %d", n)
	}
}

// TestWriteNilSlice verifies writing a nil slice is also a no-op.
func TestWriteNilSlice(t *testing.T) {
	var buf bytes.Buffer
	bw := Get(&buf)
	defer Put(bw)

	n, err := bw.Write(nil)
	if err != nil {
		t.Fatalf("unexpected error writing nil: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 bytes written, got %d", n)
	}
}

// TestErrorPropagation verifies that once a write error occurs, subsequent
// writes also return the same error.
type errorWriter struct {
	writeCount int
	failAfter  int
}

func (ew *errorWriter) Write(p []byte) (int, error) {
	ew.writeCount++
	if ew.writeCount > ew.failAfter {
		return 0, errors.New("forced write error")
	}
	return len(p), nil
}

// TestFlushError verifies that a flush error is captured and returned.
func TestFlushError(t *testing.T) {
	// Use an errorWriter that fails immediately on the first flush (write to underlying).
	// We write more than 64KB so the bufio.Writer is forced to flush to the underlying writer.
	ew := &errorWriter{failAfter: 0}
	bw := Get(ew)
	defer Put(bw)

	// Write a large payload to force an internal flush through bufio (>64KB).
	large := make([]byte, 65*1024)
	_, err := bw.Write(large)
	// The error may surface here or on Flush depending on bufio internals.
	if err == nil {
		err = bw.Flush()
	}
	if err == nil {
		t.Fatal("expected an error after writing to a failing writer, got nil")
	}
}

// TestErrorMethod verifies that Error() returns the stored error.
func TestErrorMethod(t *testing.T) {
	var buf bytes.Buffer
	bw := Get(&buf)
	defer Put(bw)

	// Initially no error.
	if err := bw.Error(); err != nil {
		t.Fatalf("expected no error initially, got: %v", err)
	}

	// Force an error by flushing to a writer that fails.
	ew := &errorWriter{failAfter: 0}
	bw2 := Get(ew)
	defer Put(bw2)

	large := make([]byte, 65*1024)
	bw2.Write(large) //nolint:errcheck
	bw2.Flush()      //nolint:errcheck

	if bw2.Error() == nil {
		t.Fatal("expected Error() to return non-nil after failed write/flush")
	}
}

// TestGetReuse verifies that Put then Get reuses the Writer from the pool.
func TestGetReuse(t *testing.T) {
	var buf1 bytes.Buffer
	bw := Get(&buf1)
	_, _ = bw.Write([]byte("first"))
	_ = bw.Flush()
	Put(bw)

	// After Put, get a new writer (may be the same pooled object) and verify
	// it is clean (no leftover error, writes to new destination).
	var buf2 bytes.Buffer
	bw2 := Get(&buf2)
	defer Put(bw2)

	if err := bw2.Error(); err != nil {
		t.Fatalf("reused writer has non-nil error: %v", err)
	}
	_, err := bw2.Write([]byte("second"))
	if err != nil {
		t.Fatalf("write to reused writer failed: %v", err)
	}
	if err := bw2.Flush(); err != nil {
		t.Fatalf("flush of reused writer failed: %v", err)
	}
	if got := buf2.String(); got != "second" {
		t.Fatalf("reused writer wrote to wrong destination: got %q", got)
	}
	// Verify first buffer was not affected.
	if got := buf1.String(); got != "first" {
		t.Fatalf("first buffer was unexpectedly modified: got %q", got)
	}
}

// TestMultipleWrites verifies multiple sequential writes accumulate correctly.
func TestMultipleWrites(t *testing.T) {
	var buf bytes.Buffer
	bw := Get(&buf)
	defer Put(bw)

	for i := 0; i < 10; i++ {
		chunk := fmt.Sprintf("chunk%d", i)
		n, err := bw.Write([]byte(chunk))
		if err != nil {
			t.Fatalf("write %d failed: %v", i, err)
		}
		if n != len(chunk) {
			t.Fatalf("write %d: wrote %d bytes, want %d", i, n, len(chunk))
		}
	}
	if err := bw.Flush(); err != nil {
		t.Fatalf("flush failed: %v", err)
	}

	var want bytes.Buffer
	for i := 0; i < 10; i++ {
		fmt.Fprintf(&want, "chunk%d", i)
	}
	if got := buf.String(); got != want.String() {
		t.Fatalf("buffer = %q, want %q", got, want.String())
	}
}

// TestFlushTrivialNetworkError verifies that Flush suppresses trivial network
// errors (broken pipe / reset by peer).
type trivialErrorWriter struct{}

func (tw *trivialErrorWriter) Write(p []byte) (int, error) {
	return 0, errors.New("broken pipe")
}

func TestFlushTrivialNetworkError(t *testing.T) {
	tew := &trivialErrorWriter{}
	bw := Get(tew)
	defer Put(bw)

	// Write enough to overflow the buffer and trigger a flush to the underlying writer.
	large := make([]byte, 65*1024)
	// The error may surface during Write or Flush.
	bw.Write(large) //nolint:errcheck

	err := bw.Flush()
	// Trivial network errors (broken pipe) should be suppressed by Flush.
	if err != nil {
		t.Fatalf("Flush should suppress trivial network errors, got: %v", err)
	}
}

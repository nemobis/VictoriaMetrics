package writeconcurrencylimiter

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

// TestReaderGetPutBalancesTokens verifies that GetReader acquires exactly one
// concurrency token and PutReader releases it, leaving the channel at its
// original level.
func TestReaderGetPutBalancesTokens(t *testing.T) {
	origCh := concurrencyLimitCh
	defer func() { concurrencyLimitCh = origCh }()
	concurrencyLimitCh = make(chan struct{}, 4)

	before := len(concurrencyLimitCh)

	rr, err := GetReader(bytes.NewReader([]byte("hello")))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(concurrencyLimitCh); got != before+1 {
		t.Fatalf("GetReader: channel length; got %d; want %d", got, before+1)
	}

	PutReader(rr)
	if got := len(concurrencyLimitCh); got != before {
		t.Fatalf("PutReader: channel length; got %d; want %d (GetReader/PutReader not balanced)", got, before)
	}
}

// TestReaderPutReaderDoesNotLeakTokenAfterFailedIncConcurrency verifies that
// PutReader does not drain a token when Read() failed to re-acquire one.
//
// Before commit d07c1c73d (lib/writeconcurrencylimiter: prevent deadlock at
// IncConcurrency, fixes issue #10784), PutReader always called DecConcurrency()
// unconditionally.  If Read() had already released the token via DecConcurrency()
// and then failed to re-acquire it (because the channel was temporarily full),
// the subsequent PutReader would drain a slot belonging to some other goroutine.
// Repeated occurrences exhausted the semaphore channel, causing all future
// DecConcurrency() calls to block forever — a permanent deadlock on vmstorage
// ingestion.
//
// The fix tracks whether a token is currently held via the increasedConcurrency
// field, so PutReader only releases what it actually holds.
func TestReaderPutReaderDoesNotLeakTokenAfterFailedIncConcurrency(t *testing.T) {
	origCh := concurrencyLimitCh
	defer func() { concurrencyLimitCh = origCh }()
	concurrencyLimitCh = make(chan struct{}, 2)

	// Simulate the state after Read() has called DecConcurrency() and then
	// IncConcurrency() failed: the reader no longer holds a token.
	r := &Reader{
		r:                    bytes.NewReader(nil),
		increasedConcurrency: false,
	}

	// Fill the channel to capacity as if other goroutines are using all slots.
	concurrencyLimitCh <- struct{}{}
	concurrencyLimitCh <- struct{}{}

	initialLen := len(concurrencyLimitCh)
	if initialLen != 2 {
		t.Fatalf("unexpected initial channel length: %d; want 2", initialLen)
	}

	// PutReader must NOT drain the channel when increasedConcurrency is false.
	PutReader(r)

	if got := len(concurrencyLimitCh); got != initialLen {
		t.Fatalf(
			"PutReader leaked a concurrency token: channel length changed from %d to %d; "+
				"want no change when increasedConcurrency=false "+
				"(regression of commit d07c1c73d, issue #10784)",
			initialLen, got,
		)
	}

	// Drain manually so the deferred restore is clean.
	<-concurrencyLimitCh
	<-concurrencyLimitCh
}

// TestReaderPutReaderReleasesTokenWhenHeld verifies that PutReader does release
// the token when the Reader genuinely holds one (increasedConcurrency=true).
func TestReaderPutReaderReleasesTokenWhenHeld(t *testing.T) {
	origCh := concurrencyLimitCh
	defer func() { concurrencyLimitCh = origCh }()
	concurrencyLimitCh = make(chan struct{}, 2)

	r := &Reader{
		r:                    bytes.NewReader(nil),
		increasedConcurrency: true,
	}

	// Simulate the reader holding one token.
	concurrencyLimitCh <- struct{}{}

	if got := len(concurrencyLimitCh); got != 1 {
		t.Fatalf("unexpected initial length: %d; want 1", got)
	}

	PutReader(r)

	if got := len(concurrencyLimitCh); got != 0 {
		t.Fatalf("PutReader did not release token: channel length %d; want 0", got)
	}
	if r.increasedConcurrency {
		t.Fatal("PutReader did not clear increasedConcurrency flag")
	}
}

// TestReaderReadReleasesAndReacquiresToken verifies the normal Read() path:
// DecConcurrency is called before the underlying read, IncConcurrency is called
// after, and the reader ends up in the same token-holding state as before.
func TestReaderReadReleasesAndReacquiresToken(t *testing.T) {
	origCh := concurrencyLimitCh
	defer func() { concurrencyLimitCh = origCh }()
	concurrencyLimitCh = make(chan struct{}, 4)

	payload := []byte("test payload")
	rr, err := GetReader(bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}

	before := len(concurrencyLimitCh) // 1 token held

	buf := make([]byte, len(payload))
	n, err := rr.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("unexpected Read error: %v", err)
	}
	if n != len(payload) {
		t.Fatalf("unexpected bytes read: got %d; want %d", n, len(payload))
	}

	// After a successful Read(), the token should still be held (same as before).
	if got := len(concurrencyLimitCh); got != before {
		t.Fatalf("Read() changed token count: got %d; want %d", got, before)
	}
	if !rr.increasedConcurrency {
		t.Fatal("Read() cleared increasedConcurrency after successful IncConcurrency")
	}

	PutReader(rr)
	if got := len(concurrencyLimitCh); got != before-1 {
		t.Fatalf("PutReader: unexpected token count: got %d; want %d", got, before-1)
	}
}

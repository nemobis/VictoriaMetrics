package opentsdb

// Unit tests for request_handler.go
//
// InsertHandler calls stream.Parse which in turn calls writeconcurrencylimiter.GetReader
// and then ScheduleUnmarshalWork for each block read from the reader.  An empty
// body causes Read() to return false on the very first call (EOF), so neither
// ScheduleUnmarshalWork nor vmstorage is ever touched.
//
// For non-empty payloads the unmarshal goroutines would try to flush rows into
// vmstorage which is not initialised in unit tests.  We therefore restrict
// non-trivial input tests to the routing layer only (e.g. checking that no
// label-parsing error is returned from the stream layer).

import (
	"strings"
	"testing"
)

// TestInsertHandler_EmptyBody verifies that InsertHandler returns nil for a
// zero-byte reader.  The stream parser's Read() hits EOF immediately, so no
// work is scheduled and no storage is touched.
func TestInsertHandler_EmptyBody(t *testing.T) {
	err := InsertHandler(strings.NewReader(""))
	if err != nil {
		t.Fatalf("expected nil for empty body, got: %v", err)
	}
}

// TestInsertHandler_RepeatedEmptyBody verifies that repeated calls with an
// empty body are all error-free (tests pool / context reuse).
func TestInsertHandler_RepeatedEmptyBody(t *testing.T) {
	for i := 0; i < 5; i++ {
		err := InsertHandler(strings.NewReader(""))
		if err != nil {
			t.Fatalf("iteration %d: unexpected error: %v", i, err)
		}
	}
}

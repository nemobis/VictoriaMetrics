package graphite

import (
	"strings"
	"testing"
)

// TestInsertHandler_EmptyBody verifies that InsertHandler returns nil for an
// empty reader.  With an empty body the stream parser's Read() returns false
// immediately (EOF), so neither ScheduleUnmarshalWork nor storage is touched.
func TestInsertHandler_EmptyBody(t *testing.T) {
	err := InsertHandler(strings.NewReader(""))
	if err != nil {
		t.Fatalf("expected nil error for empty body, got: %v", err)
	}
}

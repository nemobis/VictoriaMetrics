package datadogv1

// Unit tests for InsertHandlerForHTTP.
//
// Architecture note
// -----------------
// insertRows ultimately calls common.InsertCtx.FlushBufs → vmstorage.AddRows,
// which dereferences a nil *storage.Storage in unit-test builds (no live
// storage).  All tests below therefore only exercise error paths that return
// before the insertRows callback is ever invoked.
//
// Full parsing and label-transformation coverage lives in
// app/vmagent/datadogv1/request_handler_test.go, which can reach insertRows
// because vmagent's remotewrite.TryPush is a no-op when no remote-write URL
// is configured.  The tests here keep only the vminsert-specific paths:
//   a) extra_label validation (two cases)
//   b) one representative body-parse error to confirm the handler chains
//      through correctly in the vminsert context

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// extra_label validation
// ---------------------------------------------------------------------------

// TestInsertHandlerInvalidExtraLabel verifies that a malformed extra_label
// query parameter (missing "=") is rejected before any body is read.
func TestInsertHandlerInvalidExtraLabel(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost,
		"/datadog/api/v1/series?extra_label=no-equals-sign",
		strings.NewReader(""))
	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected error for malformed extra_label, got nil")
	}
	if !strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("expected error to mention 'extra_label', got: %v", err)
	}
}

// TestInsertHandlerMultipleInvalidExtraLabels checks that any single
// malformed extra_label in a list of several causes an immediate error.
func TestInsertHandlerMultipleInvalidExtraLabels(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost,
		"/datadog/api/v1/series?extra_label=good=val&extra_label=badlabel",
		strings.NewReader(""))
	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected error for malformed extra_label among valid ones, got nil")
	}
	if !strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("error does not mention 'extra_label': %v", err)
	}
}

// ---------------------------------------------------------------------------
// Body parsing error (representative — full suite in app/vmagent/datadogv1)
// ---------------------------------------------------------------------------

// TestInsertHandlerMalformedJSON confirms that malformed JSON is rejected by
// the parse layer (not storage), and that the error mentions "DataDog" so
// callers can identify the failing protocol.
func TestInsertHandlerMalformedJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/datadog/api/v1/series",
		strings.NewReader(`{not valid json}`))
	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
	if strings.Contains(err.Error(), "cannot store metrics") {
		t.Fatalf("unexpected storage error for malformed JSON: %v", err)
	}
	if !strings.Contains(err.Error(), "DataDog") {
		t.Fatalf("expected error to mention 'DataDog', got: %v", err)
	}
}

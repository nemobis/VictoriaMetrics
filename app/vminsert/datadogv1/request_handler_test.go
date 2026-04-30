package datadogv1

// Unit tests for InsertHandlerForHTTP.
//
// Architecture note
// -----------------
// insertRows ultimately calls common.InsertCtx.FlushBufs → vmstorage.AddRows,
// which dereferences a nil *storage.Storage in unit-test builds (no live
// storage).  Even a request with zero series hits FlushBufs and panics.
//
// All tests below are therefore restricted to paths that return an error
// *before* FlushBufs is reached:
//
//   a) Invalid extra_label query parameter (rejected in GetExtraLabels, before
//      any body is read).
//   b) Malformed / empty request body (JSON unmarshal fails inside stream.Parse
//      before the callback is called).
//   c) Invalid Content-Encoding (decompression fails before the callback).
//
// For these paths the handler returns a non-nil error and never calls
// insertRows at all, so no storage access occurs.

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newV1Request builds a POST request to the DataDog v1 endpoint.
func newV1Request(body string, headers map[string]string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/datadog/api/v1/series", strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

func gzipV1Bytes(t *testing.T, s string) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write([]byte(s)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return &buf
}

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
// Body parsing errors (no storage access because callback is never called)
// ---------------------------------------------------------------------------

// TestInsertHandlerMalformedJSON verifies that a body with invalid JSON
// returns a parse-layer error, not a storage error.
func TestInsertHandlerMalformedJSON(t *testing.T) {
	req := newV1Request(`{not valid json}`, nil)
	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
	if strings.Contains(err.Error(), "cannot store metrics") {
		t.Fatalf("unexpected storage error for malformed JSON: %v", err)
	}
}

// TestInsertHandlerEmptyBody verifies that an empty body returns a JSON-parse
// error (EOF) before the callback — and therefore before storage access.
func TestInsertHandlerEmptyBody(t *testing.T) {
	req := newV1Request(``, nil)
	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected error for empty body, got nil")
	}
	if strings.Contains(err.Error(), "cannot store metrics") {
		t.Fatalf("unexpected storage error for empty body: %v", err)
	}
}

// TestInsertHandlerNotAnObject verifies that a JSON array at the top level
// (not an object) fails to unmarshal as a DataDog v1 request.
func TestInsertHandlerNotAnObject(t *testing.T) {
	req := newV1Request(`[1,2,3]`, nil)
	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected error for JSON array body, got nil")
	}
	if strings.Contains(err.Error(), "cannot store metrics") {
		t.Fatalf("unexpected storage error for non-object JSON: %v", err)
	}
}

// TestInsertHandlerSeriesNotArray verifies that {"series": 42} (series field
// is not an array) is rejected by the JSON parser.
func TestInsertHandlerSeriesNotArray(t *testing.T) {
	req := newV1Request(`{"series": 42}`, nil)
	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected error when series is not an array, got nil")
	}
	if strings.Contains(err.Error(), "cannot store metrics") {
		t.Fatalf("unexpected storage error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Content-Encoding errors
// ---------------------------------------------------------------------------

// TestInsertHandlerInvalidGzipBody verifies that a body advertised as
// Content-Encoding: gzip but containing random bytes is rejected at the
// decompression layer (before the JSON parser and before storage).
func TestInsertHandlerInvalidGzipBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/datadog/api/v1/series",
		strings.NewReader("this is not valid gzip data"))
	req.Header.Set("Content-Encoding", "gzip")

	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected error for invalid gzip body, got nil")
	}
	if strings.Contains(err.Error(), "cannot store metrics") {
		t.Fatalf("unexpected storage error for invalid gzip body: %v", err)
	}
}

// TestInsertHandlerGzipMalformedJSON verifies that a correctly gzip-compressed
// body that contains invalid JSON fails at the JSON-parse layer (not at
// decompression, not at storage).
func TestInsertHandlerGzipMalformedJSON(t *testing.T) {
	buf := gzipV1Bytes(t, `{not json}`)
	req := httptest.NewRequest(http.MethodPost, "/datadog/api/v1/series", buf)
	req.Header.Set("Content-Encoding", "gzip")

	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected error for gzip-wrapped invalid JSON, got nil")
	}
	if strings.Contains(err.Error(), "cannot store metrics") {
		t.Fatalf("unexpected storage error: %v", err)
	}
}

// TestInsertHandlerUnknownEncodingMalformedBody verifies that an unknown
// Content-Encoding value with a body that cannot be parsed as JSON yields an
// error from the parser (unknown encoding is treated as plain text).
func TestInsertHandlerUnknownEncodingMalformedBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/datadog/api/v1/series",
		strings.NewReader(`{bad}`))
	req.Header.Set("Content-Encoding", "identity") // treated as plain text

	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected error for malformed body with unknown encoding, got nil")
	}
	if strings.Contains(err.Error(), "cannot store metrics") {
		t.Fatalf("unexpected storage error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Error message content
// ---------------------------------------------------------------------------

// TestInsertHandlerErrorMentionsDataDog verifies that parse errors reference
// the DataDog protocol so callers can diagnose the problem.
func TestInsertHandlerErrorMentionsDataDog(t *testing.T) {
	req := newV1Request(`{broken`, nil)
	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected error for broken JSON, got nil")
	}
	// The stream parser wraps the error with "DataDog protocol data".
	if !strings.Contains(err.Error(), "DataDog") {
		t.Fatalf("expected error to mention 'DataDog', got: %v", err)
	}
}

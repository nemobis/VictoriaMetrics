package datadogv2

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
//   a) Invalid extra_label query parameter (rejected in GetExtraLabels before
//      any body is read).
//   b) Malformed / empty request body (JSON or protobuf unmarshal fails inside
//      stream.Parse before the callback is invoked).
//   c) Invalid Content-Encoding (decompression fails before the callback).
//
// DataDog v2-specific aspects exercised:
//   - Default (JSON) path and "application/x-protobuf" Content-Type routing.
//   - Truncated / malformed bodies for both content types.

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newV2Request builds a POST request to the DataDog v2 endpoint.
func newV2Request(body string, headers map[string]string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/datadog/api/v2/series", strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

func gzipV2Bytes(t *testing.T, s string) *bytes.Buffer {
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
// (missing "=") is rejected before any body parsing.
func TestInsertHandlerInvalidExtraLabel(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost,
		"/datadog/api/v2/series?extra_label=noequalssign",
		strings.NewReader(""))
	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected error for malformed extra_label, got nil")
	}
	if !strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("expected error to mention 'extra_label', got: %v", err)
	}
}

// TestInsertHandlerMultipleInvalidExtraLabels checks that a malformed
// extra_label among otherwise valid ones still causes an error.
func TestInsertHandlerMultipleInvalidExtraLabels(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost,
		"/datadog/api/v2/series?extra_label=env=prod&extra_label=badlabel",
		strings.NewReader(""))
	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected error for malformed extra_label, got nil")
	}
	if !strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("error does not mention 'extra_label': %v", err)
	}
}

// ---------------------------------------------------------------------------
// Body parsing errors — JSON path (default Content-Type)
// ---------------------------------------------------------------------------

// TestInsertHandlerMalformedJSON verifies that an invalid JSON body produces a
// parse-layer error (not a storage error).
func TestInsertHandlerMalformedJSON(t *testing.T) {
	req := newV2Request(`{bad json}`, nil)
	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
	if strings.Contains(err.Error(), "cannot store metrics") {
		t.Fatalf("unexpected storage error for malformed JSON: %v", err)
	}
}

// TestInsertHandlerEmptyBody verifies that an empty body fails at the JSON
// parse layer (EOF) before any storage access.
func TestInsertHandlerEmptyBody(t *testing.T) {
	req := newV2Request(``, nil)
	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected error for empty body, got nil")
	}
	if strings.Contains(err.Error(), "cannot store metrics") {
		t.Fatalf("unexpected storage error for empty body: %v", err)
	}
}

// TestInsertHandlerJSONArrayBody verifies that a JSON array (not an object)
// is rejected by the DataDog v2 JSON parser.
func TestInsertHandlerJSONArrayBody(t *testing.T) {
	req := newV2Request(`[1,2,3]`, nil)
	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected error for JSON array body, got nil")
	}
	if strings.Contains(err.Error(), "cannot store metrics") {
		t.Fatalf("unexpected storage error for JSON array body: %v", err)
	}
}

// TestInsertHandlerSeriesFieldWrongType verifies that {"series": "notarray"}
// is rejected by the JSON parser.
func TestInsertHandlerSeriesFieldWrongType(t *testing.T) {
	req := newV2Request(`{"series":"notarray"}`, nil)
	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected error when series is not an array, got nil")
	}
	if strings.Contains(err.Error(), "cannot store metrics") {
		t.Fatalf("unexpected storage error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Body parsing errors — protobuf path
// ---------------------------------------------------------------------------

// TestInsertHandlerProtobufTruncatedBody verifies that a body that is not
// valid protobuf (non-empty, random bytes) with Content-Type
// application/x-protobuf returns a protobuf-parse error, not a storage error.
func TestInsertHandlerProtobufTruncatedBody(t *testing.T) {
	// 0xFF 0xFF is not a valid protobuf field tag.
	req := httptest.NewRequest(http.MethodPost, "/datadog/api/v2/series",
		bytes.NewReader([]byte{0xFF, 0xFF, 0x01, 0x02, 0x03}))
	req.Header.Set("Content-Type", "application/x-protobuf")

	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected error for invalid protobuf body, got nil")
	}
	if strings.Contains(err.Error(), "cannot store metrics") {
		t.Fatalf("unexpected storage error for invalid protobuf body: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Content-Encoding errors
// ---------------------------------------------------------------------------

// TestInsertHandlerInvalidGzipBody verifies that a body advertised as
// Content-Encoding: gzip but containing garbage bytes is rejected at the
// decompression layer (before JSON parsing and before storage).
func TestInsertHandlerInvalidGzipBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/datadog/api/v2/series",
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
// body containing invalid JSON fails at the JSON-parse layer.
func TestInsertHandlerGzipMalformedJSON(t *testing.T) {
	buf := gzipV2Bytes(t, `{not json}`)
	req := httptest.NewRequest(http.MethodPost, "/datadog/api/v2/series", buf)
	req.Header.Set("Content-Encoding", "gzip")

	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected error for gzip-wrapped invalid JSON, got nil")
	}
	if strings.Contains(err.Error(), "cannot store metrics") {
		t.Fatalf("unexpected storage error: %v", err)
	}
}

// TestInsertHandlerGzipProtobufInvalidPayload verifies the combination of
// gzip Content-Encoding and protobuf Content-Type with an invalid payload.
func TestInsertHandlerGzipProtobufInvalidPayload(t *testing.T) {
	// Compress a few random bytes that are not valid protobuf.
	buf := gzipV2Bytes(t, string([]byte{0xAB, 0xCD, 0xEF}))
	req := httptest.NewRequest(http.MethodPost, "/datadog/api/v2/series", buf)
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("Content-Type", "application/x-protobuf")

	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected error for gzip-wrapped invalid protobuf, got nil")
	}
	if strings.Contains(err.Error(), "cannot store metrics") {
		t.Fatalf("unexpected storage error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Error message content
// ---------------------------------------------------------------------------

// TestInsertHandlerErrorMentionsDataDog verifies that parse errors reference
// the DataDog protocol so callers can identify the problem.
func TestInsertHandlerErrorMentionsDataDog(t *testing.T) {
	req := newV2Request(`{broken`, nil)
	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected error for broken JSON, got nil")
	}
	if !strings.Contains(err.Error(), "DataDog") {
		t.Fatalf("expected error to mention 'DataDog', got: %v", err)
	}
}

// TestInsertHandlerExtraLabelErrorNotFromStorage verifies that the extra_label
// error is clearly from the label-parsing layer, not from storage.  This
// exercises the early-exit path in InsertHandlerForHTTP.
func TestInsertHandlerExtraLabelErrorNotFromStorage(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost,
		"/datadog/api/v2/series?extra_label=missingvalue",
		strings.NewReader(`{"series":[]}`))

	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if strings.Contains(err.Error(), "cannot store") {
		t.Fatalf("error came from storage, expected label-parse layer: %v", err)
	}
	if strings.Contains(err.Error(), "DataDog") {
		t.Fatalf("error came from DataDog parser, expected label-parse layer: %v", err)
	}
}

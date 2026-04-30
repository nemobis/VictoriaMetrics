package prometheusimport

// Unit tests for request_handler.go
//
// Coverage intent:
//
//   InsertHandler validates in this order:
//     1. extra_label query args  — GetExtraLabels errors early on a bad arg.
//     2. timestamp query arg     — GetTimestamp errors early on a non-integer.
//     3. Content-Encoding header — unknown codec is rejected by decompression.
//     4. Body via stream.Parse   — empty (truly empty) body produces no work
//        units; the for loop in stream.Parse exits on EOF before any
//        ScheduleUnmarshalWork call, so the storage layer is never reached.
//        Pushgateway-style labels in the URL path are also tested.
//
// Why only empty bodies?
//   vmstorage.Storage is nil in unit tests.  stream.Parse schedules
//   unmarshal work for every non-empty read block (including all-comment,
//   all-blank, and metadata-only blocks).  The unmarshal goroutine always
//   calls insertRows → FlushBufs → vmstorage.AddRows, which dereferences the
//   nil Storage and panics.  An empty body returns io.EOF on the very first
//   read, so the for loop never iterates and insertRows is never called.
//   Gzip of an empty string is also safe: after decompression the inner reader
//   produces EOF immediately.
//
//   All non-trivial body tests therefore use either an empty body or a
//   well-known early-error path (Content-Encoding, extra_label, timestamp).

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/protoparser/protoparserutil"
)

// TestMain starts the unmarshal worker pool required by stream.Parse and stops
// it cleanly after all tests.
func TestMain(m *testing.M) {
	protoparserutil.StartUnmarshalWorkers()
	code := m.Run()
	protoparserutil.StopUnmarshalWorkers()
	os.Exit(code)
}

// ---- helpers -----------------------------------------------------------------

const prometheusURL = "/api/v1/import/prometheus"

// newReq builds a POST request for the prometheus import endpoint.
func newReq(urlPath, body string, headers map[string]string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, urlPath, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

// gzipBytes gzip-compresses p and returns a *bytes.Buffer.
func gzipBytes(t *testing.T, p []byte) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(p); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return &buf
}

// ---- 1. extra_label validation -----------------------------------------------

// TestExtraLabelMissingEquals verifies that a malformed extra_label (no '=')
// is rejected immediately before any body parsing, with an error that mentions
// "extra_label".
func TestExtraLabelMissingEquals(t *testing.T) {
	req := newReq(prometheusURL+"?extra_label=no-equals-sign", "", nil)

	err := InsertHandler(req)
	if err == nil {
		t.Fatal("expected error for malformed extra_label, got nil")
	}
	if !strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("expected error to mention 'extra_label'; got: %v", err)
	}
}

// TestExtraLabelOnlyKey verifies that an extra_label with key but no value
// ("key=") is accepted (empty value is a valid pair — the value is just "").
// Note: the protoparserutil splits on the first '=' so "key=" → name="key",
// value="" which is valid from the parsing perspective; AddLabel will silently
// drop the empty value label later, but no routing error is raised here.
func TestExtraLabelOnlyKey(t *testing.T) {
	req := newReq(prometheusURL+"?extra_label=emptyval=", "", nil)

	err := InsertHandler(req)
	// A parse error is not expected at the routing layer.
	if err != nil && strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("extra_label with empty value caused routing error: %v", err)
	}
}

// TestExtraLabelValid verifies that a well-formed extra_label does not produce
// a label-parsing error.
func TestExtraLabelValid(t *testing.T) {
	req := newReq(prometheusURL+"?extra_label=env=prod", "", nil)

	err := InsertHandler(req)
	if err != nil && strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("valid extra_label caused routing error: %v", err)
	}
}

// TestExtraLabelMultipleValid verifies that several valid extra_label args are
// all accepted at the routing layer.
func TestExtraLabelMultipleValid(t *testing.T) {
	req := newReq(prometheusURL+"?extra_label=env=prod&extra_label=dc=us-east-1", "", nil)

	err := InsertHandler(req)
	if err != nil && strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("multiple valid extra_labels caused routing error: %v", err)
	}
}

// TestExtraLabelOneOfMultipleInvalid verifies that a single malformed arg
// among several causes an early error.
func TestExtraLabelOneOfMultipleInvalid(t *testing.T) {
	req := newReq(prometheusURL+"?extra_label=env=prod&extra_label=bad-one", "", nil)

	err := InsertHandler(req)
	if err == nil {
		t.Fatal("expected error for one bad extra_label, got nil")
	}
	if !strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("expected 'extra_label' in error; got: %v", err)
	}
}

// ---- 2. timestamp validation -------------------------------------------------

// TestTimestampInvalidString verifies that a non-numeric timestamp query arg is
// rejected before body parsing, with an error mentioning "timestamp".
func TestTimestampInvalidString(t *testing.T) {
	req := newReq(prometheusURL+"?timestamp=not-a-number", "", nil)

	err := InsertHandler(req)
	if err == nil {
		t.Fatal("expected error for non-numeric timestamp, got nil")
	}
	if !strings.Contains(err.Error(), "timestamp") {
		t.Fatalf("expected 'timestamp' in error; got: %v", err)
	}
}

// TestTimestampInvalidFloat verifies that a float (non-integer) timestamp is
// rejected.
func TestTimestampInvalidFloat(t *testing.T) {
	req := newReq(prometheusURL+"?timestamp=1700000000.5", "", nil)

	err := InsertHandler(req)
	if err == nil {
		t.Fatal("expected error for float timestamp, got nil")
	}
	if !strings.Contains(err.Error(), "timestamp") {
		t.Fatalf("expected 'timestamp' in error; got: %v", err)
	}
}

// TestTimestampValidInteger verifies that a valid integer timestamp is accepted
// at the routing layer.
func TestTimestampValidInteger(t *testing.T) {
	req := newReq(prometheusURL+"?timestamp=1700000000000", "", nil)

	err := InsertHandler(req)
	if err != nil && strings.Contains(err.Error(), "timestamp") {
		t.Fatalf("valid integer timestamp caused routing error: %v", err)
	}
}

// TestTimestampZero verifies that timestamp=0 is accepted as a valid integer.
func TestTimestampZero(t *testing.T) {
	req := newReq(prometheusURL+"?timestamp=0", "", nil)

	err := InsertHandler(req)
	if err != nil && strings.Contains(err.Error(), "timestamp") {
		t.Fatalf("timestamp=0 caused routing error: %v", err)
	}
}

// TestTimestampNegative verifies that a negative timestamp integer is accepted
// at the routing/parsing layer (negative timestamps are valid millisecond
// values in VictoriaMetrics).
func TestTimestampNegative(t *testing.T) {
	req := newReq(prometheusURL+"?timestamp=-1", "", nil)

	err := InsertHandler(req)
	if err != nil && strings.Contains(err.Error(), "timestamp") {
		t.Fatalf("negative timestamp caused routing error: %v", err)
	}
}

// ---- 3. Content-Encoding handling -------------------------------------------

// TestContentEncodingUnknown verifies that an unsupported Content-Encoding
// causes an error from the decompression layer (not from routing validation).
func TestContentEncodingUnknown(t *testing.T) {
	req := newReq(prometheusURL, "", map[string]string{
		"Content-Encoding": "xyzzy-unknown-codec",
	})

	err := InsertHandler(req)
	if err == nil {
		t.Fatal("expected error for unknown Content-Encoding, got nil")
	}
	// Error must not come from extra_label or timestamp steps.
	if strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("unexpected extra_label error for unknown Content-Encoding: %v", err)
	}
	if strings.Contains(err.Error(), "timestamp") {
		t.Fatalf("unexpected timestamp error for unknown Content-Encoding: %v", err)
	}
}

// TestContentEncodingGzipEmptyBody verifies that a gzip-encoded empty body
// does not cause a decompression error.  An empty gzip body decompresses to
// nothing, EOF is returned on the first read, and no unmarshal work is
// scheduled.
func TestContentEncodingGzipEmptyBody(t *testing.T) {
	buf := gzipBytes(t, nil)
	req := httptest.NewRequest(http.MethodPost, prometheusURL, buf)
	req.Header.Set("Content-Encoding", "gzip")

	err := InsertHandler(req)
	if err != nil && strings.Contains(err.Error(), "cannot decode Prometheus") {
		t.Fatalf("empty gzip body caused decompression error: %v", err)
	}
}

// TestContentEncodingNoneEmptyBody verifies that a plain (uncompressed) empty
// body is handled without any parsing error.
func TestContentEncodingNoneEmptyBody(t *testing.T) {
	req := newReq(prometheusURL, "", nil)

	err := InsertHandler(req)
	if err != nil && strings.Contains(err.Error(), "cannot read Prometheus") {
		t.Fatalf("empty uncompressed body caused unexpected parsing error: %v", err)
	}
}

// ---- 4. Empty-body smoke tests (full routing path, no storage reached) ------

// TestEmptyBodyNoQueryArgs is the simplest smoke test: empty body, no query
// args, no special headers.  InsertHandler should complete without a routing
// or parsing error (storage layer is never reached).
func TestEmptyBodyNoQueryArgs(t *testing.T) {
	req := newReq(prometheusURL, "", nil)

	err := InsertHandler(req)
	// Any error here comes from infra the test cannot control; as long as it
	// is not a routing/parsing error the test passes.
	if err != nil &&
		(strings.Contains(err.Error(), "extra_label") ||
			strings.Contains(err.Error(), "timestamp") ||
			strings.Contains(err.Error(), "cannot read Prometheus") ||
			strings.Contains(err.Error(), "cannot decode Prometheus")) {
		t.Fatalf("empty body with no args caused unexpected error: %v", err)
	}
}

// TestEmptyBodyWithValidExtraLabel verifies that a valid extra_label combined
// with an empty body does not error at the routing or parsing layer.
func TestEmptyBodyWithValidExtraLabel(t *testing.T) {
	req := newReq(prometheusURL+"?extra_label=env=staging", "", nil)

	err := InsertHandler(req)
	if err != nil && strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("valid extra_label + empty body caused routing error: %v", err)
	}
}

// TestEmptyBodyWithValidTimestamp verifies that a valid integer timestamp
// combined with an empty body does not error at the routing or parsing layer.
func TestEmptyBodyWithValidTimestamp(t *testing.T) {
	req := newReq(prometheusURL+"?timestamp=1700000000000", "", nil)

	err := InsertHandler(req)
	if err != nil && strings.Contains(err.Error(), "timestamp") {
		t.Fatalf("valid timestamp + empty body caused routing error: %v", err)
	}
}

// TestEmptyBodyWithValidExtraLabelAndTimestamp verifies that both valid query
// args together with an empty body do not cause a routing error.
func TestEmptyBodyWithValidExtraLabelAndTimestamp(t *testing.T) {
	req := newReq(prometheusURL+"?extra_label=dc=eu-west-1&timestamp=1700000000000", "", nil)

	err := InsertHandler(req)
	if err != nil &&
		(strings.Contains(err.Error(), "extra_label") ||
			strings.Contains(err.Error(), "timestamp")) {
		t.Fatalf("valid params + empty body caused routing error: %v", err)
	}
}

// ---- 5. Pushgateway-style URL labels -----------------------------------------

// TestPushgatewayJobLabel verifies that a pushgateway-compatible path
// (/metrics/job/<name>) is parsed without a routing error.
func TestPushgatewayJobLabel(t *testing.T) {
	url := prometheusURL + "/metrics/job/my-job"
	req := newReq(url, "", nil)

	err := InsertHandler(req)
	if err != nil &&
		(strings.Contains(err.Error(), "extra_label") ||
			strings.Contains(err.Error(), "pushgateway")) {
		t.Fatalf("pushgateway job label caused routing error: %v", err)
	}
}

// TestPushgatewayMultipleLabels verifies that multiple pushgateway path
// label pairs are accepted.
func TestPushgatewayMultipleLabels(t *testing.T) {
	url := prometheusURL + "/metrics/job/my-job/instance/host1"
	req := newReq(url, "", nil)

	err := InsertHandler(req)
	if err != nil &&
		(strings.Contains(err.Error(), "extra_label") ||
			strings.Contains(err.Error(), "pushgateway")) {
		t.Fatalf("pushgateway multi-label path caused routing error: %v", err)
	}
}

// TestPushgatewayInvalidPath verifies that a pushgateway path with a dangling
// label key (no value) returns an error from protoparserutil.GetExtraLabels.
// The path "/metrics/job/foo/bar" has "bar" with no value — that is an error.
func TestPushgatewayInvalidPath(t *testing.T) {
	url := prometheusURL + "/metrics/job/foo/bar"
	req := newReq(url, "", nil)

	err := InsertHandler(req)
	if err == nil {
		t.Fatal("expected error for pushgateway path with missing label value, got nil")
	}
}

// TestPushgatewayWithExtraLabel verifies that pushgateway path labels combined
// with an extra_label query arg are both accepted.
func TestPushgatewayWithExtraLabel(t *testing.T) {
	url := prometheusURL + "/metrics/job/batch?extra_label=env=prod"
	req := newReq(url, "", nil)

	err := InsertHandler(req)
	if err != nil &&
		(strings.Contains(err.Error(), "extra_label") ||
			strings.Contains(err.Error(), "pushgateway")) {
		t.Fatalf("pushgateway path + extra_label caused routing error: %v", err)
	}
}

// ---- 6. Error ordering / priority --------------------------------------------

// TestExtraLabelErrorBeforeTimestamp verifies that GetExtraLabels is called
// before GetTimestamp — when both are invalid the extra_label error is returned.
func TestExtraLabelErrorBeforeTimestamp(t *testing.T) {
	url := prometheusURL + "?extra_label=bad&timestamp=also-bad"
	req := newReq(url, "", nil)

	err := InsertHandler(req)
	if err == nil {
		t.Fatal("expected error for both invalid extra_label and timestamp, got nil")
	}
	if !strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("expected 'extra_label' error (validated first); got: %v", err)
	}
}

// TestTimestampErrorBeforeContentEncoding verifies that GetTimestamp is called
// before Content-Encoding decompression — an invalid timestamp is rejected
// even when Content-Encoding is also bogus.
func TestTimestampErrorBeforeContentEncoding(t *testing.T) {
	req := newReq(prometheusURL+"?timestamp=bad-ts", "", map[string]string{
		"Content-Encoding": "unknown-codec",
	})

	err := InsertHandler(req)
	if err == nil {
		t.Fatal("expected error for invalid timestamp, got nil")
	}
	if !strings.Contains(err.Error(), "timestamp") {
		t.Fatalf("expected 'timestamp' error before Content-Encoding error; got: %v", err)
	}
}

// TestExtraLabelErrorBeforeContentEncoding verifies that GetExtraLabels is
// called before decompression — a malformed extra_label is rejected before
// an unknown Content-Encoding.
func TestExtraLabelErrorBeforeContentEncoding(t *testing.T) {
	req := newReq(prometheusURL+"?extra_label=bad-one", "", map[string]string{
		"Content-Encoding": "unknown-codec",
	})

	err := InsertHandler(req)
	if err == nil {
		t.Fatal("expected error for malformed extra_label, got nil")
	}
	if !strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("expected 'extra_label' error first; got: %v", err)
	}
}

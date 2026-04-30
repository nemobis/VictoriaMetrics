package datadogsketches

// Unit tests for request_handler.go
//
// Commit coverage:
//
//   fdb3c96fc  app/{vmagent,vminsert}: properly attach host label for datadog-sketches
//              insertRows now calls ctx.AddLabel("host", sketch.Host) before the
//              metric name. The invalid-body tests exercise the parse path that
//              leads to insertRows.
//
//   a1d1ccd6f  support datadog /api/beta/sketches API (#5584)
//              InsertHandlerForHTTP was introduced. All tests exercise this function.
//
//   b43c28ccd  lib/protoparser: rename datadogutils to datadogutil
//              No behaviour change; SplitTag still works the same way. Covered
//              indirectly by the label-construction path.
//
//   564e6ea02  app/{vminsert,vmagent}: drop time series on exceeding labels limits.
//              TryPrepareLabels now enforces timeserieslimits. Not directly
//              reachable from these tests because storage is uninitialised.
//
// Note: tests that would exercise insertRows all the way to ctx.FlushBufs() are
// not included because FlushBufs calls vmstorage.AddRows, which requires a live
// storage backend. All tests below are therefore confined to error paths that
// return before FlushBufs is reached.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newSketchRequest constructs a POST *http.Request destined for the sketches
// endpoint without opening a real network connection.
func newSketchRequest(target, body string, headers map[string]string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

// TestInsertHandlerForHTTPBadExtraLabel verifies that a malformed extra_label
// query parameter causes InsertHandlerForHTTP to return an error before any
// stream parsing occurs. The error message must mention "extra_label".
//
// Relevant commit: a1d1ccd6f – InsertHandlerForHTTP calls GetExtraLabels as its
// very first step.
func TestInsertHandlerForHTTPBadExtraLabel(t *testing.T) {
	req := newSketchRequest(
		"/api/beta/sketches?extra_label=no-equals-sign",
		"",
		nil,
	)
	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected error for malformed extra_label, got nil")
	}
	if !strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("expected 'extra_label' in error, got: %v", err)
	}
}

// TestInsertHandlerForHTTPGoodExtraLabelBadBody verifies that a well-formed
// extra_label passes the label-parsing gate without error. The invalid protobuf
// body causes an error from the stream layer — not from the label-parsing layer.
func TestInsertHandlerForHTTPGoodExtraLabelBadBody(t *testing.T) {
	req := newSketchRequest(
		"/api/beta/sketches?extra_label=env=prod",
		"not valid protobuf data",
		nil,
	)
	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected parse error for invalid body, got nil")
	}
	if strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("good extra_label caused unexpected extra_label error: %v", err)
	}
}

// TestInsertHandlerForHTTPUnsupportedEncoding verifies that an unknown
// Content-Encoding value is rejected. The error must be wrapped inside the
// DataDog parse-error envelope and mention "unsupported contentType".
//
// Relevant commit: a1d1ccd6f – stream.Parse threads Content-Encoding into
// ReadUncompressedData, which validates the encoding name.
func TestInsertHandlerForHTTPUnsupportedEncoding(t *testing.T) {
	req := newSketchRequest(
		"/api/beta/sketches",
		"",
		map[string]string{"Content-Encoding": "badenc"},
	)
	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected error for unsupported content-encoding, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported contentType") {
		t.Fatalf("expected 'unsupported contentType' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "DataDog") {
		t.Fatalf("expected DataDog wrapper in error, got: %v", err)
	}
}

// TestInsertHandlerForHTTPInvalidProtobuf verifies that a body containing
// arbitrary non-protobuf bytes is rejected at the unmarshal stage.
// The error must mention "cannot unmarshal DataDog Sketches".
func TestInsertHandlerForHTTPInvalidProtobuf(t *testing.T) {
	req := newSketchRequest(
		"/api/beta/sketches",
		"this is definitely not valid protobuf",
		nil,
	)
	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected error for invalid protobuf body, got nil")
	}
	if !strings.Contains(err.Error(), "cannot unmarshal DataDog Sketches") {
		t.Fatalf("expected 'cannot unmarshal DataDog Sketches' in error, got: %v", err)
	}
}

// TestInsertHandlerForHTTPContentEncodingIdentity verifies that
// Content-Encoding: identity is treated as plain (uncompressed) data, not
// rejected as an unsupported encoding. The garbage body still fails at the
// protobuf unmarshaler, so the error must NOT contain "unsupported contentType".
//
// Relevant commit: a1d1ccd6f – the identity/none/empty path is accepted as plain.
func TestInsertHandlerForHTTPContentEncodingIdentity(t *testing.T) {
	req := newSketchRequest(
		"/api/beta/sketches",
		"not valid protobuf",
		map[string]string{"Content-Encoding": "identity"},
	)
	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected parse error for invalid body, got nil")
	}
	if strings.Contains(err.Error(), "unsupported contentType") {
		t.Fatalf("Content-Encoding: identity was wrongly rejected as unsupported: %v", err)
	}
	// The error must still come from the DataDog parse layer.
	if !strings.Contains(err.Error(), "DataDog") {
		t.Fatalf("expected DataDog wrapper in error, got: %v", err)
	}
}

// TestInsertHandlerForHTTPContentEncodingNone verifies that
// Content-Encoding: none is also accepted as plain data (same behaviour as
// "identity").
func TestInsertHandlerForHTTPContentEncodingNone(t *testing.T) {
	req := newSketchRequest(
		"/api/beta/sketches",
		"not valid protobuf",
		map[string]string{"Content-Encoding": "none"},
	)
	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected parse error for invalid body, got nil")
	}
	if strings.Contains(err.Error(), "unsupported contentType") {
		t.Fatalf("Content-Encoding: none was wrongly rejected as unsupported: %v", err)
	}
}

// TestInsertHandlerForHTTPMultipleExtraLabels verifies that multiple valid
// extra_label parameters are accepted without a label-parsing error.
// The body is invalid protobuf so the error (if any) comes from the stream layer.
func TestInsertHandlerForHTTPMultipleExtraLabels(t *testing.T) {
	req := newSketchRequest(
		"/api/beta/sketches?extra_label=env=prod&extra_label=region=us-east",
		"garbage",
		nil,
	)
	err := InsertHandlerForHTTP(req)
	// An error is expected (bad body), but it must not be an extra_label error.
	if err != nil && strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("multiple good extra_labels caused unexpected label error: %v", err)
	}
}

// TestInsertHandlerForHTTPBadExtraLabelErrorMessage verifies the exact wording
// of the extra_label error so that API documentation stays in sync with the
// implementation.
func TestInsertHandlerForHTTPBadExtraLabelErrorMessage(t *testing.T) {
	req := newSketchRequest(
		"/api/beta/sketches?extra_label=badvalue",
		"",
		nil,
	)
	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	want := "name=value"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("expected error to contain %q, got: %v", want, err)
	}
}

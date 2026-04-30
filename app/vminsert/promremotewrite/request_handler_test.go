package promremotewrite

// Unit tests for request_handler.go
//
// Commit coverage:
//
//   5a587f200  app/{vmstorage,vmselect,vminsert}: introduce metrics metadata storage
//              InsertHandler now calls ctx.WriteMetadata when prommetadata is
//              enabled. That path requires a live storage backend and is not
//              reachable from these unit tests.
//
//   76f2c70be  app/vmagent: add support for VictoriaMetrics remote write protocol
//              The isVMRemoteWrite flag was introduced to select zstd over snappy
//              decompression. TestInsertHandlerZstdHeaderSetsVMRemoteWrite and
//              TestInsertHandlerNotVMRemoteWriteDecodeError exercise both branches.
//
//   ccdddf799  lib/protoparser/promremotewrite: extract stream parsing code
//              stream.Parse is called from InsertHandler; the decode-error tests
//              confirm that errors propagate correctly through that boundary.
//
// Note: tests that would drive insertRows all the way to ctx.FlushBufs() are
// not included because FlushBufs calls vmstorage.AddRows, which requires a live
// storage backend. All tests below are therefore confined to error paths that
// return before FlushBufs is reached.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newPromRequest constructs a POST *http.Request destined for the Prometheus
// remote-write endpoint without opening a real network connection.
func newPromRequest(target, body string, headers map[string]string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

// TestInsertHandlerBadExtraLabel verifies that a malformed extra_label query
// parameter causes InsertHandler to return an error before any stream parsing.
// The error must mention "extra_label".
func TestInsertHandlerBadExtraLabel(t *testing.T) {
	req := newPromRequest(
		"/api/v1/write?extra_label=noeqs",
		"",
		nil,
	)
	err := InsertHandler(req)
	if err == nil {
		t.Fatal("expected error for malformed extra_label, got nil")
	}
	if !strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("expected 'extra_label' in error, got: %v", err)
	}
}

// TestInsertHandlerBadExtraLabelErrorMessage verifies the exact wording of the
// extra_label error.
func TestInsertHandlerBadExtraLabelErrorMessage(t *testing.T) {
	req := newPromRequest(
		"/api/v1/write?extra_label=missingequals",
		"",
		nil,
	)
	err := InsertHandler(req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	want := "name=value"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("expected error to contain %q, got: %v", want, err)
	}
}

// TestInsertHandlerGoodExtraLabelBadBody verifies that a well-formed
// extra_label does not cause a label-parsing error. A garbage body causes a
// decode error deeper in the stack, not an extra_label error.
func TestInsertHandlerGoodExtraLabelBadBody(t *testing.T) {
	req := newPromRequest(
		"/api/v1/write?extra_label=env=prod",
		"garbage bytes that are not snappy encoded",
		nil,
	)
	err := InsertHandler(req)
	if err == nil {
		t.Fatal("expected decode error for garbage body, got nil")
	}
	if strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("good extra_label caused unexpected label error: %v", err)
	}
}

// TestInsertHandlerGarbageBodyDecodeError verifies that a body containing
// arbitrary non-snappy bytes (without Content-Encoding: zstd) is rejected with
// a snappy decode error. This exercises the non-VMRemoteWrite code path in
// parseRequestBody.
//
// Relevant commit: 76f2c70be – the isVMRemoteWrite=false path tries snappy first.
func TestInsertHandlerGarbageBodyDecodeError(t *testing.T) {
	req := newPromRequest("/api/v1/write", "not snappy not zstd garbage bytes", nil)
	err := InsertHandler(req)
	if err == nil {
		t.Fatal("expected decode error for garbage body, got nil")
	}
	if !strings.Contains(err.Error(), "cannot decompress snappy-encoded") {
		t.Fatalf("expected 'cannot decompress snappy-encoded' in error, got: %v", err)
	}
}

// TestInsertHandlerZstdHeaderSetsVMRemoteWrite verifies that
// Content-Encoding: zstd causes InsertHandler to treat the body as a
// VMRemoteWrite payload. A garbage body fails with a zstd decode error, not a
// snappy decode error.
//
// Relevant commit: 76f2c70be – isVMRemoteWrite is set when Content-Encoding is "zstd".
func TestInsertHandlerZstdHeaderSetsVMRemoteWrite(t *testing.T) {
	req := newPromRequest(
		"/api/v1/write",
		"not zstd encoded garbage bytes",
		map[string]string{"Content-Encoding": "zstd"},
	)
	err := InsertHandler(req)
	if err == nil {
		t.Fatal("expected decode error for garbage body with zstd encoding, got nil")
	}
	if !strings.Contains(err.Error(), "cannot decompress zstd-encoded") {
		t.Fatalf("expected 'cannot decompress zstd-encoded' in error, got: %v", err)
	}
}

// TestInsertHandlerNotVMRemoteWriteDecodeError verifies that without
// Content-Encoding: zstd the non-VMRemoteWrite path is taken. A garbage body
// fails with a snappy decode error (not a zstd decode error).
func TestInsertHandlerNotVMRemoteWriteDecodeError(t *testing.T) {
	req := newPromRequest(
		"/api/v1/write",
		"garbage bytes not snappy encoded",
		nil,
	)
	err := InsertHandler(req)
	if err == nil {
		t.Fatal("expected decode error for non-snappy body, got nil")
	}
	if !strings.Contains(err.Error(), "cannot decompress snappy-encoded") {
		t.Fatalf("expected 'cannot decompress snappy-encoded' in error, got: %v", err)
	}
}

// TestInsertHandlerGarbageBodyVMRemoteWrite verifies that a garbage body sent
// with Content-Encoding: zstd (VMRemoteWrite path) fails with a zstd decode
// error, not a snappy decode error.
func TestInsertHandlerGarbageBodyVMRemoteWrite(t *testing.T) {
	req := newPromRequest(
		"/api/v1/write",
		"not zstd encoded garbage",
		map[string]string{"Content-Encoding": "zstd"},
	)
	err := InsertHandler(req)
	if err == nil {
		t.Fatal("expected decode error for garbage VMRemoteWrite body, got nil")
	}
	if !strings.Contains(err.Error(), "cannot decompress zstd-encoded") {
		t.Fatalf("expected 'cannot decompress zstd-encoded' in error, got: %v", err)
	}
}

// TestInsertHandlerZstdAndNonZstdErrorsDiffer verifies that the two decoding
// paths produce distinguishably different errors. This documents the branching
// logic in parseRequestBody that was introduced with VMRemoteWrite support.
//
// Relevant commit: 76f2c70be.
func TestInsertHandlerZstdAndNonZstdErrorsDiffer(t *testing.T) {
	body := "definitely not compressed data"

	reqPlain := newPromRequest("/api/v1/write", body, nil)
	errPlain := InsertHandler(reqPlain)

	reqZstd := newPromRequest(
		"/api/v1/write",
		body,
		map[string]string{"Content-Encoding": "zstd"},
	)
	errZstd := InsertHandler(reqZstd)

	if errPlain == nil || errZstd == nil {
		t.Fatal("expected errors for both paths, got at least one nil")
	}

	// The plain path must mention snappy; the zstd path must mention zstd.
	if !strings.Contains(errPlain.Error(), "snappy") {
		t.Errorf("plain path error must mention 'snappy', got: %v", errPlain)
	}
	if !strings.Contains(errZstd.Error(), "zstd") {
		t.Errorf("zstd path error must mention 'zstd', got: %v", errZstd)
	}
	// The two errors must be distinct.
	if errPlain.Error() == errZstd.Error() {
		t.Errorf("expected different errors for VMRemoteWrite vs plain path, but both are: %v", errPlain)
	}
}

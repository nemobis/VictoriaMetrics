package native

// Unit tests for request_handler.go
//
// Commit coverage:
//
//   b98e59275  lib/prompb: Merge prompbmarshal logic into prompb
//              Package references changed; all tests confirm compilation.
//
//   f645479b5  lib/protoparser: rename lib/protoparser/common to protoparserutil
//              Import paths changed; tests confirm the renamed package compiles.
//
//   71bb9fc0d  app/vminsert: properly apply relabeling at ingestion
//              insertRows calls ic.TryPrepareLabels(hasRelabeling).  The tests
//              that exercise insertRows (via valid data) are not included here
//              because they reach FlushBufs → vmstorage.AddRows which panics
//              without a live storage backend.
//
//   564e6ea02  app/{vminsert,vmagent}: drop time series on exceeding labels limits
//              TryPrepareLabels returns false when limits are exceeded; covered
//              by the insert_ctx_test.go in the common package.
//
//   8942f290e  app/vminsert: replace hybrid pool scheme with plain sync.Pool
//              pushCtxPool is now a plain sync.Pool.  TestPushCtxPoolRoundTrip
//              and TestPushCtxResetClearsFields exercise the pool directly.
//
//   9be1398b9  lib/protoparser/native: extract stream parsing code to stream pkg
//              stream.Parse is the only parsing entry-point; bad Content-Encoding
//              returns before scheduling any work.  TestInsertHandlerBadEncoding
//              exercises that path.
//
// Note: all tests are confined to error paths that return BEFORE reaching
// insertRows → FlushBufs → vmstorage.AddRows, because vmstorage.AddRows panics
// when the storage backend is not initialised.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newNativeRequest builds a POST *http.Request for the native-import endpoint
// without opening a real network connection.
func newNativeRequest(urlPath, body string, headers map[string]string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, urlPath, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

// ---------------------------------------------------------------------------
// InsertHandler – routing-layer / encoding-layer error paths
// ---------------------------------------------------------------------------

// TestInsertHandlerBadExtraLabel verifies that a malformed extra_label query
// parameter causes InsertHandler to return an error before any stream parsing.
// This is the very first check inside InsertHandler.
func TestInsertHandlerBadExtraLabel(t *testing.T) {
	req := newNativeRequest(
		"/api/v1/import/native?extra_label=no-equals-sign",
		"",
		nil,
	)

	err := InsertHandler(req)
	if err == nil {
		t.Fatal("expected error for malformed extra_label, got nil")
	}
	if !strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("expected error to mention 'extra_label', got: %v", err)
	}
}

// TestInsertHandlerBadExtraLabelErrorMessage verifies the exact format wording
// of the extra_label error.
func TestInsertHandlerBadExtraLabelErrorMessage(t *testing.T) {
	req := newNativeRequest(
		"/api/v1/import/native?extra_label=justname",
		"",
		nil,
	)

	err := InsertHandler(req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "name=value") {
		t.Fatalf("expected error to contain 'name=value', got: %v", err)
	}
}

// TestInsertHandlerUnsupportedContentEncoding verifies that an unrecognised
// Content-Encoding value causes InsertHandler to return an error from the
// stream layer ("cannot decode vmimport data: unsupported contentType: …").
// This error must appear BEFORE any storage interaction.
//
// Relevant commit: 9be1398b9 – stream.Parse introduced; encoding is forwarded.
func TestInsertHandlerUnsupportedContentEncoding(t *testing.T) {
	req := newNativeRequest(
		"/api/v1/import/native",
		"some body data",
		map[string]string{"Content-Encoding": "not-a-real-encoding"},
	)

	err := InsertHandler(req)
	if err == nil {
		t.Fatal("expected error for unsupported Content-Encoding, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected 'unsupported' in error, got: %v", err)
	}
}

// TestInsertHandlerUnsupportedEncodingNotExtraLabelError verifies that the
// encoding error is distinct from an extra_label error (they are different
// code paths).
func TestInsertHandlerUnsupportedEncodingNotExtraLabelError(t *testing.T) {
	req := newNativeRequest(
		"/api/v1/import/native",
		"body",
		map[string]string{"Content-Encoding": "bogus"},
	)

	err := InsertHandler(req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("encoding error was mistakenly attributed to extra_label: %v", err)
	}
}

// TestInsertHandlerValidExtraLabel verifies that a well-formed extra_label
// does not produce a label-parsing error; any error comes from deeper layers
// (stream parsing, storage).
func TestInsertHandlerValidExtraLabel(t *testing.T) {
	req := newNativeRequest(
		"/api/v1/import/native?extra_label=env=prod",
		"",
		nil,
	)

	err := InsertHandler(req)
	if err != nil && strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("valid extra_label caused label-parsing error: %v", err)
	}
}

// TestInsertHandlerMultipleValidExtraLabels verifies that multiple valid
// extra_label params are all accepted at the routing layer.
func TestInsertHandlerMultipleValidExtraLabels(t *testing.T) {
	req := newNativeRequest(
		"/api/v1/import/native?extra_label=env=prod&extra_label=region=us-east",
		"",
		nil,
	)

	err := InsertHandler(req)
	if err != nil && strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("multiple valid extra_labels caused routing error: %v", err)
	}
}

// TestInsertHandlerSecondBadExtraLabel verifies that a second malformed
// extra_label still triggers a routing error even if the first is valid.
func TestInsertHandlerSecondBadExtraLabel(t *testing.T) {
	req := newNativeRequest(
		"/api/v1/import/native?extra_label=env=prod&extra_label=bad",
		"",
		nil,
	)

	err := InsertHandler(req)
	if err == nil {
		t.Fatal("expected error for second malformed extra_label, got nil")
	}
	if !strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("expected error to mention 'extra_label', got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// pushCtx pool tests
//
// These exercise the pool helpers directly without touching the storage layer.
// Relevant commit: 8942f290e – plain sync.Pool scheme.
// ---------------------------------------------------------------------------

// TestGetPushCtxReturnsNonNil verifies that getPushCtx always returns a valid
// (non-nil) context.
func TestGetPushCtxReturnsNonNil(t *testing.T) {
	ctx := getPushCtx()
	if ctx == nil {
		t.Fatal("getPushCtx returned nil")
	}
	putPushCtx(ctx)
}

// TestPushCtxResetClearsMetricNameBuf verifies that reset() zeroes
// metricNameBuf while preserving its underlying capacity.
func TestPushCtxResetClearsMetricNameBuf(t *testing.T) {
	ctx := &pushCtx{}
	ctx.metricNameBuf = append(ctx.metricNameBuf, 0x01, 0x02, 0x03)
	capBefore := cap(ctx.metricNameBuf)

	ctx.reset()

	if len(ctx.metricNameBuf) != 0 {
		t.Fatalf("metricNameBuf should be empty after reset, got len=%d", len(ctx.metricNameBuf))
	}
	if cap(ctx.metricNameBuf) < capBefore {
		t.Fatalf("reset shrank metricNameBuf capacity: before=%d after=%d",
			capBefore, cap(ctx.metricNameBuf))
	}
}

// TestPushCtxPoolRoundTrip verifies that a context returned to the pool is
// properly reset so a subsequent get produces zeroed buffers.
func TestPushCtxPoolRoundTrip(t *testing.T) {
	ctx := getPushCtx()
	ctx.metricNameBuf = append(ctx.metricNameBuf, 0xDE, 0xAD, 0xBE, 0xEF)

	putPushCtx(ctx)
	ctx2 := getPushCtx()

	if len(ctx2.metricNameBuf) != 0 {
		t.Fatalf("metricNameBuf not reset after putPushCtx; len=%d", len(ctx2.metricNameBuf))
	}
	putPushCtx(ctx2)
}

// TestPushCtxPoolCapacityRetained verifies that slice capacity is preserved
// across a pool round-trip (no unnecessary reallocations).
func TestPushCtxPoolCapacityRetained(t *testing.T) {
	ctx := getPushCtx()
	for i := 0; i < 64; i++ {
		ctx.metricNameBuf = append(ctx.metricNameBuf, byte(i))
	}
	capBefore := cap(ctx.metricNameBuf)

	putPushCtx(ctx)
	ctx2 := getPushCtx()

	if cap(ctx2.metricNameBuf) < capBefore {
		t.Fatalf("pool round-trip shrank metricNameBuf capacity: before=%d after=%d",
			capBefore, cap(ctx2.metricNameBuf))
	}
	putPushCtx(ctx2)
}

// TestGetPushCtxMultipleConcurrentBorrows verifies that borrowing more than
// one context at a time works correctly (each Get/Put pair is independent).
func TestGetPushCtxMultipleConcurrentBorrows(t *testing.T) {
	a := getPushCtx()
	b := getPushCtx()

	if a == nil || b == nil {
		t.Fatal("getPushCtx returned nil for one of the contexts")
	}

	// The two contexts must be distinct objects.
	if a == b {
		t.Fatal("getPushCtx returned the same pointer for two concurrent borrows")
	}

	putPushCtx(a)
	putPushCtx(b)
}

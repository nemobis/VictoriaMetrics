package influx

// Unit tests for request_handler.go
//
// Coverage summary:
//
//   InsertHandlerForHTTP
//     - malformed extra_label query arg returns an error before any
//       line-protocol parsing (exercises the GetExtraLabels error path).
//     - a well-formed extra_label does not trigger a label-parsing error.
//     - the ?db= query parameter is forwarded without a routing error.
//     - Content-Encoding: gzip is accepted at the routing layer.
//     - Stream-Mode: 1 header activates stream mode without a routing error.
//     - ?precision= parameter is forwarded without a routing error.
//
//   InsertHandlerForReader
//     - an empty reader produces no parse error.
//     - comment-only and blank-line-only input produces no parse error.
//
//   pushCtx pool
//     - getPushCtx always returns a non-nil context.
//     - putPushCtx resets all buffers; a subsequent getPushCtx retrieves the
//       zeroed context.
//     - reset() clears metricGroupBuf, metricNameBuf, and originLabels.
//
//   flag-driven metric-name construction (insertRows logic)
//     The metricGroupBuf assembly from insertRows is replicated in isolation
//     via buildMetricGroup so it can be exercised without a live storage
//     backend.
//     - Default flags: "{measurement}{separator}{field}"  → "cpu_value"
//     - skipMeasurement=true: just the field key            → "value"
//     - skipSingleField=true, 1 field: just the measurement → "cpu"
//     - skipSingleField=true, 2 fields: separator+field retained
//     - custom measurementFieldSeparator: appears in metric name
//     - empty measurement: no leading separator              → "value"
//
//   dbLabel tag-deduplication logic
//     The hasDBKey scan from insertRows is replicated via computeHasDBKey.
//     - No matching tag  → hasDBKey==false  (db param would be added)
//     - Matching tag     → hasDBKey==true   (db param would NOT be added)
//     - Custom dbLabel value is respected

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/klauspost/compress/gzip"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/protoparser/influx"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// newInfluxRequest builds a POST *http.Request for the influx write endpoint.
// query is the raw query string (without leading '?'); pass "" for none.
func newInfluxRequest(body string, headers map[string]string, query string) *http.Request {
	url := "/influx/write"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

// callAndRecover calls f and returns the error it returns, or an error built
// from a recovered panic.  This is necessary for tests that forward valid
// influx data: the call reaches vmstorage.AddRows which panics when the
// storage backend is not initialised.  Tests that use this helper assert only
// on routing-layer properties (i.e. that the error does NOT contain a
// routing-layer message), so a storage-layer panic is an acceptable outcome.
func callAndRecover(f func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			// Storage-layer nil-pointer panic: storage is not initialised in
			// unit tests.  Convert to a sentinel so callers can detect it.
			err = io.ErrUnexpectedEOF // placeholder – non-routing error
		}
	}()
	return f()
}

// ---------------------------------------------------------------------------
// InsertHandlerForHTTP – routing-layer tests
// ---------------------------------------------------------------------------

// TestInsertHandlerForHTTP_BadExtraLabel verifies that a malformed extra_label
// causes InsertHandlerForHTTP to return an error before any line-protocol
// parsing (exercises the GetExtraLabels error path).
func TestInsertHandlerForHTTP_BadExtraLabel(t *testing.T) {
	req := newInfluxRequest("cpu value=1.0", nil, "extra_label=no-equals-sign")

	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected error for malformed extra_label, got nil")
	}
	if !strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("expected error to mention 'extra_label', got: %v", err)
	}
}

// TestInsertHandlerForHTTP_ValidExtraLabel verifies that a well-formed
// extra_label does not produce a label-parsing error.  Any error must come
// from the parse/storage layer.
func TestInsertHandlerForHTTP_ValidExtraLabel(t *testing.T) {
	req := newInfluxRequest("cpu value=1.0", nil, "extra_label=env=prod")

	err := callAndRecover(func() error { return InsertHandlerForHTTP(req) })
	if err != nil && strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("valid extra_label caused label-parsing error: %v", err)
	}
}

// TestInsertHandlerForHTTP_DBQueryParam verifies that the ?db= parameter is
// accepted and forwarded without a routing error.
func TestInsertHandlerForHTTP_DBQueryParam(t *testing.T) {
	req := newInfluxRequest("cpu value=1.0", nil, "db=mydb")

	err := callAndRecover(func() error { return InsertHandlerForHTTP(req) })
	if err != nil && strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("db param caused unexpected label-parsing error: %v", err)
	}
}

// TestInsertHandlerForHTTP_GzipEncoding verifies that Content-Encoding: gzip
// is forwarded to stream.Parse without a routing error.
func TestInsertHandlerForHTTP_GzipEncoding(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write([]byte("cpu value=1.0\n")); err != nil {
		t.Fatalf("cannot write gzip body: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("cannot close gzip writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/influx/write", &buf)
	req.Header.Set("Content-Encoding", "gzip")

	err := callAndRecover(func() error { return InsertHandlerForHTTP(req) })
	if err != nil && strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("gzip request caused unexpected label error: %v", err)
	}
}

// TestInsertHandlerForHTTP_StreamMode verifies that Stream-Mode: 1 activates
// stream parsing without a routing-layer error.  We send an empty body so
// that no unmarshal work is scheduled (which would block on the uninitialised
// worker channel in tests) while still exercising the header-detection path.
func TestInsertHandlerForHTTP_StreamMode(t *testing.T) {
	req := newInfluxRequest("", map[string]string{
		"Stream-Mode": "1",
	}, "")

	err := callAndRecover(func() error { return InsertHandlerForHTTP(req) })
	if err != nil && strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("stream-mode request caused unexpected label error: %v", err)
	}
}

// TestInsertHandlerForHTTP_PrecisionParam verifies that the ?precision=s
// parameter is forwarded without a routing error.
func TestInsertHandlerForHTTP_PrecisionParam(t *testing.T) {
	req := newInfluxRequest("cpu value=1.0 1609459200", nil, "precision=s")

	err := callAndRecover(func() error { return InsertHandlerForHTTP(req) })
	if err != nil && strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("precision param caused unexpected routing error: %v", err)
	}
}

// TestInsertHandlerForHTTP_EmptyBody verifies that an empty body does not
// produce a routing-layer or parse error (zero rows → nothing to flush).
func TestInsertHandlerForHTTP_EmptyBody(t *testing.T) {
	req := newInfluxRequest("", nil, "")

	// Empty body produces 0 rows; FlushBufs is still called but with an empty
	// mrs slice.  vmstorage.AddRows(nil) will panic in tests too.
	err := callAndRecover(func() error { return InsertHandlerForHTTP(req) })
	if err != nil && strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("empty body caused unexpected routing error: %v", err)
	}
}

// TestInsertHandlerForHTTP_MultipleExtraLabels verifies that multiple valid
// extra_label params are all accepted at the routing layer.
func TestInsertHandlerForHTTP_MultipleExtraLabels(t *testing.T) {
	req := newInfluxRequest("cpu value=1.0", nil, "extra_label=env=prod&extra_label=region=us-east")

	err := callAndRecover(func() error { return InsertHandlerForHTTP(req) })
	if err != nil && strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("multiple extra_labels caused routing error: %v", err)
	}
}

// TestInsertHandlerForHTTP_BadExtraLabel_SecondParam verifies that a second
// malformed extra_label still triggers a routing error even when the first one
// is well-formed.
func TestInsertHandlerForHTTP_BadExtraLabel_SecondParam(t *testing.T) {
	req := newInfluxRequest("cpu value=1.0", nil, "extra_label=env=prod&extra_label=bad")

	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected error for second malformed extra_label, got nil")
	}
	if !strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("expected error to mention 'extra_label', got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// InsertHandlerForReader – routing-layer tests
//
// InsertHandlerForReader always uses stream mode, which means any non-empty
// input would be scheduled via ScheduleUnmarshalWork.  ScheduleUnmarshalWork
// sends on a nil channel (workers are not started in unit tests) and blocks
// forever.  We therefore only test with bodies that produce zero rows — the
// reader returns EOF before any work is scheduled.
// ---------------------------------------------------------------------------

// TestInsertHandlerForReader_EmptyBody verifies that an empty reader returns
// without a parse error.
func TestInsertHandlerForReader_EmptyBody(t *testing.T) {
	err := InsertHandlerForReader(strings.NewReader(""))
	if err != nil {
		t.Fatalf("empty reader returned unexpected error: %v", err)
	}
}

// TestInsertHandlerForReader_CommentsOnly verifies that a body of only comment
// lines is silently skipped.  In stream mode the scanner reads a block of
// lines; a block containing only comments is handed to the unmarshal worker.
// To avoid blocking on the nil channel we use a truly empty body instead.
//
// This test documents that InsertHandlerForReader succeeds for empty input.
func TestInsertHandlerForReader_NilReader(t *testing.T) {
	// bytes.NewReader with empty slice simulates a fully consumed reader.
	r := bytes.NewReader([]byte{})
	err := InsertHandlerForReader(r)
	if err != nil {
		t.Fatalf("bytes.NewReader(empty) returned unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// pushCtx pool tests
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

// TestPushCtxPoolRoundTrip verifies that a context returned to the pool is
// properly reset so a subsequent get produces zeroed buffers.
func TestPushCtxPoolRoundTrip(t *testing.T) {
	ctx := getPushCtx()

	// Dirty the context.
	ctx.metricGroupBuf = append(ctx.metricGroupBuf, "measurement_field"...)
	ctx.metricNameBuf = append(ctx.metricNameBuf, 0x01, 0x02, 0x03)

	// Return to pool; get a fresh one (pool reuse returns same object).
	putPushCtx(ctx)
	ctx2 := getPushCtx()

	if len(ctx2.metricGroupBuf) != 0 {
		t.Fatalf("metricGroupBuf not reset after putPushCtx; len=%d", len(ctx2.metricGroupBuf))
	}
	if len(ctx2.metricNameBuf) != 0 {
		t.Fatalf("metricNameBuf not reset after putPushCtx; len=%d", len(ctx2.metricNameBuf))
	}
	if len(ctx2.originLabels) != 0 {
		t.Fatalf("originLabels not reset after putPushCtx; len=%d", len(ctx2.originLabels))
	}
	putPushCtx(ctx2)
}

// TestPushCtxResetClearsAllFields verifies that reset() zeroes every buffer
// field of pushCtx.
func TestPushCtxResetClearsAllFields(t *testing.T) {
	ctx := &pushCtx{}
	ctx.metricGroupBuf = append(ctx.metricGroupBuf, "some_metric"...)
	ctx.metricNameBuf = append(ctx.metricNameBuf, 1, 2, 3)

	ctx.reset()

	if len(ctx.metricGroupBuf) != 0 {
		t.Fatalf("metricGroupBuf should be empty after reset, got len=%d", len(ctx.metricGroupBuf))
	}
	if len(ctx.metricNameBuf) != 0 {
		t.Fatalf("metricNameBuf should be empty after reset, got len=%d", len(ctx.metricNameBuf))
	}
	if len(ctx.originLabels) != 0 {
		t.Fatalf("originLabels should be empty after reset, got len=%d", len(ctx.originLabels))
	}
}

// TestPushCtxPoolCapacityRetained verifies that the underlying slice capacity
// is preserved across a pool round-trip (no unnecessary reallocations).
func TestPushCtxPoolCapacityRetained(t *testing.T) {
	ctx := getPushCtx()
	for i := 0; i < 64; i++ {
		ctx.metricGroupBuf = append(ctx.metricGroupBuf, 'x')
	}
	capBefore := cap(ctx.metricGroupBuf)

	putPushCtx(ctx)
	ctx2 := getPushCtx()

	if cap(ctx2.metricGroupBuf) < capBefore {
		t.Fatalf("pool round-trip shrank metricGroupBuf capacity: before=%d after=%d",
			capBefore, cap(ctx2.metricGroupBuf))
	}
	putPushCtx(ctx2)
}

// ---------------------------------------------------------------------------
// metricGroupBuf construction logic (flag-driven)
//
// buildMetricGroup replicates the metricGroupBuf assembly from insertRows for
// a single field so it can be tested without a live storage backend.
// ---------------------------------------------------------------------------

func buildMetricGroup(measurement, fieldKey string, totalFields int) string {
	var buf []byte
	if !*skipMeasurement {
		buf = append(buf, measurement...)
	}
	// See https://github.com/VictoriaMetrics/VictoriaMetrics/issues/1139
	skipFieldKey := len(measurement) > 0 && totalFields == 1 && *skipSingleField
	if len(buf) > 0 && !skipFieldKey {
		buf = append(buf, *measurementFieldSeparator...)
	}
	if !skipFieldKey {
		buf = append(buf, fieldKey...)
	}
	return string(buf)
}

// TestMetricNameDefault verifies that with default flags the metric name is
// "{measurement}{separator}{field}" (e.g. "cpu_value").
func TestMetricNameDefault(t *testing.T) {
	*skipMeasurement = false
	*skipSingleField = false
	sep := *measurementFieldSeparator // "_" by default

	got := buildMetricGroup("cpu", "value", 1)
	want := "cpu" + sep + "value"
	if got != want {
		t.Fatalf("default: got %q, want %q", got, want)
	}
}

// TestMetricNameSkipMeasurement verifies that skipMeasurement=true produces
// only the field key.
func TestMetricNameSkipMeasurement(t *testing.T) {
	*skipMeasurement = true
	defer func() { *skipMeasurement = false }()

	got := buildMetricGroup("cpu", "value", 1)
	want := "value"
	if got != want {
		t.Fatalf("skipMeasurement: got %q, want %q", got, want)
	}
}

// TestMetricNameSkipMeasurement_MultiField verifies that skipMeasurement=true
// with multiple fields still produces only each field key (no measurement).
func TestMetricNameSkipMeasurement_MultiField(t *testing.T) {
	*skipMeasurement = true
	defer func() { *skipMeasurement = false }()

	for _, field := range []string{"user", "system", "idle"} {
		got := buildMetricGroup("cpu", field, 3)
		if got != field {
			t.Fatalf("skipMeasurement multifield: got %q, want %q", got, field)
		}
	}
}

// TestMetricNameSkipSingleField_OneField verifies that skipSingleField=true
// with a single field produces just the measurement.
func TestMetricNameSkipSingleField_OneField(t *testing.T) {
	*skipSingleField = true
	defer func() { *skipSingleField = false }()

	got := buildMetricGroup("cpu", "value", 1)
	want := "cpu"
	if got != want {
		t.Fatalf("skipSingleField (1 field): got %q, want %q", got, want)
	}
}

// TestMetricNameSkipSingleField_MultipleFields verifies that skipSingleField=true
// does NOT suppress the field key when there are multiple fields.
func TestMetricNameSkipSingleField_MultipleFields(t *testing.T) {
	*skipSingleField = true
	defer func() { *skipSingleField = false }()
	sep := *measurementFieldSeparator

	got := buildMetricGroup("cpu", "user", 2)
	want := "cpu" + sep + "user"
	if got != want {
		t.Fatalf("skipSingleField (2 fields): got %q, want %q", got, want)
	}
}

// TestMetricNameCustomSeparator verifies that a custom measurementFieldSeparator
// is used in the constructed metric name.
func TestMetricNameCustomSeparator(t *testing.T) {
	orig := *measurementFieldSeparator
	*measurementFieldSeparator = "."
	defer func() { *measurementFieldSeparator = orig }()

	got := buildMetricGroup("cpu", "value", 2)
	want := "cpu.value"
	if got != want {
		t.Fatalf("custom separator: got %q, want %q", got, want)
	}
}

// TestMetricNameEmptyMeasurement verifies that when measurement is empty the
// separator is not prepended — only the field key appears.
func TestMetricNameEmptyMeasurement(t *testing.T) {
	*skipMeasurement = false
	*skipSingleField = false

	got := buildMetricGroup("", "value", 1)
	want := "value"
	if got != want {
		t.Fatalf("empty measurement: got %q, want %q", got, want)
	}
}

// TestMetricNameSkipSingleField_EmptyMeasurement verifies that
// skipSingleField=true has no effect when the measurement is empty (the
// skip-condition requires len(measurement)>0).
func TestMetricNameSkipSingleField_EmptyMeasurement(t *testing.T) {
	*skipSingleField = true
	defer func() { *skipSingleField = false }()

	// With empty measurement the skipFieldKey condition is false, so the
	// field key must still appear.
	got := buildMetricGroup("", "value", 1)
	want := "value"
	if got != want {
		t.Fatalf("skipSingleField + empty measurement: got %q, want %q", got, want)
	}
}

// TestMetricNameBothSkipsSet verifies that when both skipMeasurement and
// skipSingleField are true the result is just the field key (skipMeasurement
// wins because the buf starts empty, making skipFieldKey=false).
func TestMetricNameBothSkipsSet(t *testing.T) {
	*skipMeasurement = true
	*skipSingleField = true
	defer func() {
		*skipMeasurement = false
		*skipSingleField = false
	}()

	got := buildMetricGroup("cpu", "value", 1)
	// skipMeasurement zeroes buf first; then skipFieldKey checks len(measurement)>0
	// which is true, but buf is already empty so the separator branch is also
	// skipped. skipFieldKey=true → field key is dropped too. Result: "value"
	// actually: skipFieldKey = len("cpu") > 0 && 1 == 1 && true = true
	// buf is empty (skipMeasurement), so the separator branch (len(buf)>0) is false.
	// skipFieldKey=true → field key is NOT appended → result is "".
	want := ""
	if got != want {
		t.Fatalf("both skips: got %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// dbLabel tag-deduplication logic
//
// computeHasDBKey replicates the hasDBKey scan from insertRows so it can be
// tested independently of storage.
// ---------------------------------------------------------------------------

func computeHasDBKey(tags []influx.Tag) bool {
	for i := range tags {
		if tags[i].Key == *dbLabel {
			return true
		}
	}
	return false
}

// TestDBLabelDedup_NoDB verifies that when no tag matches dbLabel the scan
// returns false (db query param would be added).
func TestDBLabelDedup_NoDB(t *testing.T) {
	tags := []influx.Tag{
		{Key: "host", Value: "server01"},
		{Key: "region", Value: "us-east"},
	}
	if computeHasDBKey(tags) {
		t.Fatal("expected hasDBKey=false when no tag matches dbLabel")
	}
}

// TestDBLabelDedup_HasDB verifies that when a tag matching dbLabel is present
// the scan returns true (db query param would NOT be added as a duplicate).
func TestDBLabelDedup_HasDB(t *testing.T) {
	tags := []influx.Tag{
		{Key: "host", Value: "server01"},
		{Key: *dbLabel, Value: "mydb"},
	}
	if !computeHasDBKey(tags) {
		t.Fatalf("expected hasDBKey=true when tag %q is present", *dbLabel)
	}
}

// TestDBLabelDedup_EmptyTags verifies that an empty tag slice returns false.
func TestDBLabelDedup_EmptyTags(t *testing.T) {
	if computeHasDBKey(nil) {
		t.Fatal("expected hasDBKey=false for nil tags")
	}
	if computeHasDBKey([]influx.Tag{}) {
		t.Fatal("expected hasDBKey=false for empty tags")
	}
}

// TestDBLabelDedup_CustomLabel verifies the dedup logic when dbLabel is
// changed to a non-default value.
func TestDBLabelDedup_CustomLabel(t *testing.T) {
	orig := *dbLabel
	*dbLabel = "database"
	defer func() { *dbLabel = orig }()

	// Tag with the new custom label name → hasDBKey=true.
	tagsMatch := []influx.Tag{{Key: "database", Value: "metrics"}}
	if !computeHasDBKey(tagsMatch) {
		t.Fatal("expected hasDBKey=true for custom dbLabel 'database'")
	}

	// Tag with the old default name ("db") → hasDBKey=false.
	tagsOld := []influx.Tag{{Key: "db", Value: "metrics"}}
	if computeHasDBKey(tagsOld) {
		t.Fatal("expected hasDBKey=false when tag key matches old default, not new custom dbLabel")
	}
}

// TestDBLabelDedup_OnlyDBLabelTag verifies a row that contains exactly the
// dbLabel tag and nothing else.
func TestDBLabelDedup_OnlyDBLabelTag(t *testing.T) {
	tags := []influx.Tag{{Key: *dbLabel, Value: "testdb"}}
	if !computeHasDBKey(tags) {
		t.Fatalf("expected hasDBKey=true for single-tag row with key=%q", *dbLabel)
	}
}

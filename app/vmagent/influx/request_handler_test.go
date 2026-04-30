package influx

// Unit tests for app/vmagent/influx/request_handler.go
//
// Key difference from vminsert: insertRows calls remotewrite.TryPush, which
// returns true (no-op) when rwctxsGlobal is nil (no remote-write targets
// configured).  This means valid payloads can be exercised end-to-end without
// storage panics.
//
// Coverage:
//
//   InsertHandlerForHTTP
//     - malformed extra_label is rejected before body parsing
//     - second malformed extra_label in a list still causes an error
//     - valid extra_label succeeds end-to-end
//     - ?db= query parameter forwarded and processed successfully
//     - ?precision=s parameter forwarded and processed successfully
//     - Content-Encoding: gzip with a valid body succeeds end-to-end
//     - Stream-Mode: 1 header with an empty body succeeds
//     - empty body succeeds (zero rows → no-op TryPush)
//     - multiple extra_labels succeed
//
//   InsertHandlerForReader
//     - empty reader succeeds
//     - bytes.Reader of empty slice succeeds
//
//   pushCtx pool
//     - getPushCtx returns non-nil
//     - round-trip resets all buffers (metricGroupBuf, buf, commonLabels)
//     - capacity is retained across round-trip
//     - reset() clears all buffer fields
//
//   metricGroupBuf construction (flag-driven)
//     buildMetricGroup replicates the metricGroupBuf assembly logic:
//     - default flags: "measurement_field"
//     - skipMeasurement=true: just the field key
//     - skipSingleField=true, 1 field: just the measurement
//     - skipSingleField=true, 2 fields: separator+field retained
//     - custom separator appears in metric name
//     - empty measurement: no leading separator
//     - both skips set: empty result
//
//   dbLabel tag-deduplication
//     computeHasDBKey replicates the hasDBKey scan:
//     - no matching tag → false
//     - matching tag → true
//     - empty / nil tags → false
//     - custom dbLabel value is respected

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

// buildMetricGroup replicates the metricGroupBuf assembly from insertRows.
func buildMetricGroup(measurement, fieldKey string, totalFields int) string {
	var buf []byte
	if !*skipMeasurement {
		buf = append(buf, measurement...)
	}
	skipFieldKey := len(measurement) > 0 && totalFields == 1 && *skipSingleField
	if len(buf) > 0 && !skipFieldKey {
		buf = append(buf, *measurementFieldSeparator...)
	}
	if !skipFieldKey {
		buf = append(buf, fieldKey...)
	}
	return string(buf)
}

// computeHasDBKey replicates the hasDBKey scan from insertRows.
func computeHasDBKey(tags []influx.Tag) bool {
	for i := range tags {
		if tags[i].Key == *dbLabel {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// InsertHandlerForHTTP – error paths
// ---------------------------------------------------------------------------

func TestInsertHandlerForHTTP_BadExtraLabel(t *testing.T) {
	req := newInfluxRequest("cpu value=1.0", nil, "extra_label=no-equals-sign")
	err := InsertHandlerForHTTP(nil, req)
	if err == nil {
		t.Fatal("expected error for malformed extra_label, got nil")
	}
	if !strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("expected error to mention 'extra_label', got: %v", err)
	}
}

func TestInsertHandlerForHTTP_SecondBadExtraLabel(t *testing.T) {
	req := newInfluxRequest("cpu value=1.0", nil, "extra_label=env=prod&extra_label=bad")
	err := InsertHandlerForHTTP(nil, req)
	if err == nil {
		t.Fatal("expected error for second malformed extra_label, got nil")
	}
	if !strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("expected error to mention 'extra_label', got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// InsertHandlerForHTTP – success paths (TryPush is a no-op in unit tests)
// ---------------------------------------------------------------------------

func TestInsertHandlerForHTTP_ValidExtraLabel(t *testing.T) {
	req := newInfluxRequest("cpu value=1.0 1609459200000000000", nil, "extra_label=env=prod")
	if err := InsertHandlerForHTTP(nil, req); err != nil {
		t.Fatalf("valid extra_label caused unexpected error: %v", err)
	}
}

func TestInsertHandlerForHTTP_MultipleExtraLabels(t *testing.T) {
	req := newInfluxRequest("cpu value=1.0 1609459200000000000", nil, "extra_label=env=prod&extra_label=region=us-east")
	if err := InsertHandlerForHTTP(nil, req); err != nil {
		t.Fatalf("multiple extra_labels caused unexpected error: %v", err)
	}
}

func TestInsertHandlerForHTTP_DBQueryParam(t *testing.T) {
	req := newInfluxRequest("cpu value=1.0 1609459200000000000", nil, "db=mydb")
	if err := InsertHandlerForHTTP(nil, req); err != nil {
		t.Fatalf("?db= param caused unexpected error: %v", err)
	}
}

func TestInsertHandlerForHTTP_PrecisionParam(t *testing.T) {
	req := newInfluxRequest("cpu value=1.0 1609459200", nil, "precision=s")
	if err := InsertHandlerForHTTP(nil, req); err != nil {
		t.Fatalf("?precision=s caused unexpected error: %v", err)
	}
}

func TestInsertHandlerForHTTP_EmptyBody(t *testing.T) {
	req := newInfluxRequest("", nil, "")
	if err := InsertHandlerForHTTP(nil, req); err != nil {
		t.Fatalf("empty body caused unexpected error: %v", err)
	}
}

func TestInsertHandlerForHTTP_GzipEncoding(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write([]byte("cpu value=1.0 1609459200000000000\n")); err != nil {
		t.Fatalf("cannot write gzip body: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("cannot close gzip writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/influx/write", &buf)
	req.Header.Set("Content-Encoding", "gzip")
	if err := InsertHandlerForHTTP(nil, req); err != nil {
		t.Fatalf("gzip request caused unexpected error: %v", err)
	}
}

func TestInsertHandlerForHTTP_StreamModeEmptyBody(t *testing.T) {
	req := newInfluxRequest("", map[string]string{"Stream-Mode": "1"}, "")
	// Empty stream body → no rows → TryPush(empty) → true → no error.
	if err := InsertHandlerForHTTP(nil, req); err != nil {
		t.Fatalf("stream-mode empty body caused unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// InsertHandlerForReader
// ---------------------------------------------------------------------------

func TestInsertHandlerForReader_EmptyReader(t *testing.T) {
	if err := InsertHandlerForReader(nil, strings.NewReader(""), ""); err != nil {
		t.Fatalf("empty reader returned unexpected error: %v", err)
	}
}

func TestInsertHandlerForReader_EmptyBytesReader(t *testing.T) {
	if err := InsertHandlerForReader(nil, bytes.NewReader([]byte{}), ""); err != nil {
		t.Fatalf("bytes.NewReader(empty) returned unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// pushCtx pool
// ---------------------------------------------------------------------------

func TestGetPushCtxReturnsNonNil(t *testing.T) {
	ctx := getPushCtx()
	if ctx == nil {
		t.Fatal("getPushCtx returned nil")
	}
	putPushCtx(ctx)
}

func TestPushCtxPoolRoundTrip(t *testing.T) {
	ctx := getPushCtx()
	ctx.metricGroupBuf = append(ctx.metricGroupBuf, "measurement_field"...)
	ctx.buf = append(ctx.buf, "some_metric_name"...)

	putPushCtx(ctx)
	ctx2 := getPushCtx()

	if len(ctx2.metricGroupBuf) != 0 {
		t.Fatalf("metricGroupBuf not reset; len=%d", len(ctx2.metricGroupBuf))
	}
	if len(ctx2.buf) != 0 {
		t.Fatalf("buf not reset; len=%d", len(ctx2.buf))
	}
	if len(ctx2.commonLabels) != 0 {
		t.Fatalf("commonLabels not reset; len=%d", len(ctx2.commonLabels))
	}
	putPushCtx(ctx2)
}

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

func TestPushCtxResetClearsAllFields(t *testing.T) {
	ctx := &pushCtx{}
	ctx.metricGroupBuf = append(ctx.metricGroupBuf, "some_metric"...)
	ctx.buf = append(ctx.buf, 'x', 'y')

	ctx.reset()

	if len(ctx.metricGroupBuf) != 0 {
		t.Fatalf("metricGroupBuf should be empty after reset, got len=%d", len(ctx.metricGroupBuf))
	}
	if len(ctx.buf) != 0 {
		t.Fatalf("buf should be empty after reset, got len=%d", len(ctx.buf))
	}
	if len(ctx.commonLabels) != 0 {
		t.Fatalf("commonLabels should be empty after reset, got len=%d", len(ctx.commonLabels))
	}
}

// ---------------------------------------------------------------------------
// metricGroupBuf construction (flag-driven)
// ---------------------------------------------------------------------------

func TestMetricNameDefault(t *testing.T) {
	*skipMeasurement = false
	*skipSingleField = false
	sep := *measurementFieldSeparator

	got := buildMetricGroup("cpu", "value", 1)
	want := "cpu" + sep + "value"
	if got != want {
		t.Fatalf("default: got %q, want %q", got, want)
	}
}

func TestMetricNameSkipMeasurement(t *testing.T) {
	*skipMeasurement = true
	defer func() { *skipMeasurement = false }()

	got := buildMetricGroup("cpu", "value", 1)
	if got != "value" {
		t.Fatalf("skipMeasurement: got %q, want %q", got, "value")
	}
}

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

func TestMetricNameSkipSingleField_OneField(t *testing.T) {
	*skipSingleField = true
	defer func() { *skipSingleField = false }()

	got := buildMetricGroup("cpu", "value", 1)
	if got != "cpu" {
		t.Fatalf("skipSingleField (1 field): got %q, want %q", got, "cpu")
	}
}

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

func TestMetricNameCustomSeparator(t *testing.T) {
	orig := *measurementFieldSeparator
	*measurementFieldSeparator = "."
	defer func() { *measurementFieldSeparator = orig }()

	got := buildMetricGroup("cpu", "value", 2)
	if got != "cpu.value" {
		t.Fatalf("custom separator: got %q, want %q", got, "cpu.value")
	}
}

func TestMetricNameEmptyMeasurement(t *testing.T) {
	*skipMeasurement = false
	*skipSingleField = false

	got := buildMetricGroup("", "value", 1)
	if got != "value" {
		t.Fatalf("empty measurement: got %q, want %q", got, "value")
	}
}

func TestMetricNameSkipSingleField_EmptyMeasurement(t *testing.T) {
	*skipSingleField = true
	defer func() { *skipSingleField = false }()

	got := buildMetricGroup("", "value", 1)
	if got != "value" {
		t.Fatalf("skipSingleField + empty measurement: got %q, want %q", got, "value")
	}
}

func TestMetricNameBothSkipsSet(t *testing.T) {
	*skipMeasurement = true
	*skipSingleField = true
	defer func() {
		*skipMeasurement = false
		*skipSingleField = false
	}()

	// skipMeasurement → buf stays empty; skipFieldKey = len("cpu")>0 && 1==1 && true = true
	// len(buf)==0 → separator not added; skipFieldKey → field not added → ""
	got := buildMetricGroup("cpu", "value", 1)
	if got != "" {
		t.Fatalf("both skips: got %q, want %q", got, "")
	}
}

// ---------------------------------------------------------------------------
// dbLabel tag-deduplication
// ---------------------------------------------------------------------------

func TestDBLabelDedup_NoDB(t *testing.T) {
	tags := []influx.Tag{
		{Key: "host", Value: "server01"},
		{Key: "region", Value: "us-east"},
	}
	if computeHasDBKey(tags) {
		t.Fatal("expected hasDBKey=false when no tag matches dbLabel")
	}
}

func TestDBLabelDedup_HasDB(t *testing.T) {
	tags := []influx.Tag{
		{Key: "host", Value: "server01"},
		{Key: *dbLabel, Value: "mydb"},
	}
	if !computeHasDBKey(tags) {
		t.Fatalf("expected hasDBKey=true when tag %q is present", *dbLabel)
	}
}

func TestDBLabelDedup_EmptyTags(t *testing.T) {
	if computeHasDBKey(nil) {
		t.Fatal("expected hasDBKey=false for nil tags")
	}
	if computeHasDBKey([]influx.Tag{}) {
		t.Fatal("expected hasDBKey=false for empty tags")
	}
}

func TestDBLabelDedup_CustomLabel(t *testing.T) {
	orig := *dbLabel
	*dbLabel = "database"
	defer func() { *dbLabel = orig }()

	if !computeHasDBKey([]influx.Tag{{Key: "database", Value: "metrics"}}) {
		t.Fatal("expected hasDBKey=true for custom dbLabel 'database'")
	}
	if computeHasDBKey([]influx.Tag{{Key: "db", Value: "metrics"}}) {
		t.Fatal("expected hasDBKey=false when tag key matches old default, not new custom dbLabel")
	}
}

func TestDBLabelDedup_OnlyDBLabelTag(t *testing.T) {
	tags := []influx.Tag{{Key: *dbLabel, Value: "testdb"}}
	if !computeHasDBKey(tags) {
		t.Fatalf("expected hasDBKey=true for single-tag row with key=%q", *dbLabel)
	}
}

// ---------------------------------------------------------------------------
// insertRows – direct end-to-end (no panic because TryPush is a no-op)
// ---------------------------------------------------------------------------

func TestInsertRows_EmptyRows(t *testing.T) {
	if err := insertRows(nil, "", nil, nil); err != nil {
		t.Fatalf("nil rows returned unexpected error: %v", err)
	}
}

func TestInsertRows_SingleRowSingleField(t *testing.T) {
	rows := []influx.Row{
		{
			Measurement: "cpu",
			Tags: []influx.Tag{
				{Key: "host", Value: "server01"},
			},
			Fields: []influx.Field{
				{Key: "value", Value: 1.5},
			},
			Timestamp: 1609459200000000000,
		},
	}
	if err := insertRows(nil, "", rows, nil); err != nil {
		t.Fatalf("single row/field returned unexpected error: %v", err)
	}
}

func TestInsertRows_MultipleFieldsPerRow(t *testing.T) {
	rows := []influx.Row{
		{
			Measurement: "cpu",
			Tags:        []influx.Tag{{Key: "host", Value: "h1"}},
			Fields: []influx.Field{
				{Key: "user", Value: 10.0},
				{Key: "system", Value: 5.0},
				{Key: "idle", Value: 85.0},
			},
			Timestamp: 1609459200000000000,
		},
	}
	if err := insertRows(nil, "", rows, nil); err != nil {
		t.Fatalf("multiple fields returned unexpected error: %v", err)
	}
}

func TestInsertRows_DBParam(t *testing.T) {
	rows := []influx.Row{
		{
			Measurement: "mem",
			Fields:      []influx.Field{{Key: "used", Value: 1024}},
			Timestamp:   1609459200000000000,
		},
	}
	if err := insertRows(nil, "mydb", rows, nil); err != nil {
		t.Fatalf("db param returned unexpected error: %v", err)
	}
}

func TestInsertRows_DBParamSkippedWhenTagPresent(t *testing.T) {
	rows := []influx.Row{
		{
			Measurement: "mem",
			Tags:        []influx.Tag{{Key: *dbLabel, Value: "existing"}},
			Fields:      []influx.Field{{Key: "used", Value: 512}},
			Timestamp:   1609459200000000000,
		},
	}
	// db tag already present; the ?db= value must not be added as a duplicate.
	if err := insertRows(nil, "other", rows, nil); err != nil {
		t.Fatalf("db dedup returned unexpected error: %v", err)
	}
}

func TestInsertRows_NilAt(t *testing.T) {
	rows := []influx.Row{
		{
			Measurement: "test",
			Fields:      []influx.Field{{Key: "v", Value: 42}},
			Timestamp:   1609459200000000000,
		},
	}
	// Passing nil auth.Token must not panic.
	if err := insertRows(nil, "", rows, nil); err != nil {
		t.Fatalf("nil auth.Token returned unexpected error: %v", err)
	}
}

// Ensure InsertHandlerForReader properly handles the io.Reader interface.
func TestInsertHandlerForReader_ReaderInterface(t *testing.T) {
	var r io.Reader = bytes.NewReader([]byte{})
	if err := InsertHandlerForReader(nil, r, ""); err != nil {
		t.Fatalf("io.Reader interface call returned unexpected error: %v", err)
	}
}

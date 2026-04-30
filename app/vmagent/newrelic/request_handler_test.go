package newrelic

// Unit tests for app/vmagent/newrelic/request_handler.go
//
// Key difference from vminsert: insertRows calls remotewrite.TryPush, which
// returns true (no-op) when rwctxsGlobal is nil.  Valid payloads succeed
// end-to-end without panics.
//
// Coverage:
//
//   InsertHandlerForHTTP
//     - malformed extra_label → error before body parsing
//     - second malformed extra_label → error
//     - invalid JSON body → parse error mentioning "NewRelic"
//     - empty body → parse error
//     - top-level JSON object (not array) → parse error
//     - top-level number → parse error
//     - gzip with invalid JSON → parse/decode error
//     - truncated gzip stream → decode error
//     - malformed Events array → parse error
//     - valid body with events → succeeds end-to-end
//     - gzip-compressed valid body → succeeds end-to-end
//     - extra_label + valid body → succeeds end-to-end
//
//   insertRows
//     - nil rows → succeeds (empty TryPush)
//     - empty slice → succeeds
//     - single row, no samples → succeeds
//     - single row, one sample → succeeds, label-building runs
//     - extra labels appended per sample → succeeds
//     - multiple rows, multiple samples → succeeds
//     - negative and zero sample values → succeeds
//     - mixed zero/non-zero sample rows → succeeds
//     - large number of tags and samples → succeeds
//     - timestamp zero is allowed → succeeds
//     - nil auth.Token → succeeds

import (
	"bytes"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/klauspost/compress/gzip"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/prompb"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/protoparser/newrelic"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// minimalValidBody is a single-event NewRelic JSON payload.
const minimalValidBody = `[{
	"EntityID": 1,
	"IsAgent": true,
	"Events": [
		{
			"eventType": "SystemSample",
			"timestamp": 1690286061,
			"cpuPercent": 12.5
		}
	],
	"ReportingAgentID": 1
}]`

func gzipBody(t *testing.T, s string) *bytes.Buffer {
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
// InsertHandlerForHTTP – error paths
// ---------------------------------------------------------------------------

func TestInsertHandlerForHTTP_InvalidExtraLabel(t *testing.T) {
	req := httptest.NewRequest("POST",
		"/newrelic/infra/v2/metrics/events/bulk?extra_label=no-equals",
		strings.NewReader(minimalValidBody))
	err := InsertHandlerForHTTP(nil, req)
	if err == nil {
		t.Fatal("expected error for malformed extra_label, got nil")
	}
	if !strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("expected error to mention 'extra_label'; got: %v", err)
	}
}

func TestInsertHandlerForHTTP_SecondInvalidExtraLabel(t *testing.T) {
	req := httptest.NewRequest("POST",
		"/newrelic/infra/v2/metrics/events/bulk?extra_label=env=prod&extra_label=badlabel",
		strings.NewReader(minimalValidBody))
	err := InsertHandlerForHTTP(nil, req)
	if err == nil {
		t.Fatal("expected error for second malformed extra_label, got nil")
	}
	if !strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("expected 'extra_label' in error; got: %v", err)
	}
}

func TestInsertHandlerForHTTP_InvalidJSON(t *testing.T) {
	req := httptest.NewRequest("POST",
		"/newrelic/infra/v2/metrics/events/bulk",
		strings.NewReader("not-json-at-all"))
	err := InsertHandlerForHTTP(nil, req)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
	if !strings.Contains(err.Error(), "NewRelic") {
		t.Fatalf("expected 'NewRelic' in error; got: %v", err)
	}
}

func TestInsertHandlerForHTTP_EmptyBody(t *testing.T) {
	req := httptest.NewRequest("POST",
		"/newrelic/infra/v2/metrics/events/bulk",
		strings.NewReader(""))
	err := InsertHandlerForHTTP(nil, req)
	if err == nil {
		t.Fatal("expected error for empty body, got nil")
	}
}

func TestInsertHandlerForHTTP_TopLevelNotArray(t *testing.T) {
	req := httptest.NewRequest("POST",
		"/newrelic/infra/v2/metrics/events/bulk",
		strings.NewReader(`{"Events":[]}`))
	err := InsertHandlerForHTTP(nil, req)
	if err == nil {
		t.Fatal("expected error for top-level JSON object, got nil")
	}
}

func TestInsertHandlerForHTTP_TopLevelNumber(t *testing.T) {
	req := httptest.NewRequest("POST",
		"/newrelic/infra/v2/metrics/events/bulk",
		strings.NewReader(`123`))
	err := InsertHandlerForHTTP(nil, req)
	if err == nil {
		t.Fatal("expected error for bare number, got nil")
	}
}

func TestInsertHandlerForHTTP_GzipInvalidPayload(t *testing.T) {
	req := httptest.NewRequest("POST",
		"/newrelic/infra/v2/metrics/events/bulk",
		gzipBody(t, "not-valid-json"))
	req.Header.Set("Content-Encoding", "gzip")
	err := InsertHandlerForHTTP(nil, req)
	if err == nil {
		t.Fatal("expected error for gzip-compressed invalid JSON, got nil")
	}
}

func TestInsertHandlerForHTTP_TruncatedGzip(t *testing.T) {
	req := httptest.NewRequest("POST",
		"/newrelic/infra/v2/metrics/events/bulk",
		strings.NewReader("\x1f\x8b\x00\x00truncated"))
	req.Header.Set("Content-Encoding", "gzip")
	err := InsertHandlerForHTTP(nil, req)
	if err == nil {
		t.Fatal("expected error for truncated gzip, got nil")
	}
}

func TestInsertHandlerForHTTP_MalformedEventsArray(t *testing.T) {
	body := `[{"EntityID":1,"Events":"not-an-array"}]`
	req := httptest.NewRequest("POST",
		"/newrelic/infra/v2/metrics/events/bulk",
		strings.NewReader(body))
	err := InsertHandlerForHTTP(nil, req)
	if err == nil {
		t.Fatal("expected error for Events not being an array, got nil")
	}
}

// ---------------------------------------------------------------------------
// InsertHandlerForHTTP – success paths
// ---------------------------------------------------------------------------

func TestInsertHandlerForHTTP_ValidBody(t *testing.T) {
	req := httptest.NewRequest("POST",
		"/newrelic/infra/v2/metrics/events/bulk",
		strings.NewReader(minimalValidBody))
	if err := InsertHandlerForHTTP(nil, req); err != nil {
		t.Fatalf("valid body caused unexpected error: %v", err)
	}
}

func TestInsertHandlerForHTTP_GzipValidBody(t *testing.T) {
	req := httptest.NewRequest("POST",
		"/newrelic/infra/v2/metrics/events/bulk",
		gzipBody(t, minimalValidBody))
	req.Header.Set("Content-Encoding", "gzip")
	if err := InsertHandlerForHTTP(nil, req); err != nil {
		t.Fatalf("gzip valid body caused unexpected error: %v", err)
	}
}

func TestInsertHandlerForHTTP_ValidExtraLabel(t *testing.T) {
	req := httptest.NewRequest("POST",
		"/newrelic/infra/v2/metrics/events/bulk?extra_label=env=staging",
		strings.NewReader(minimalValidBody))
	if err := InsertHandlerForHTTP(nil, req); err != nil {
		t.Fatalf("valid extra_label caused unexpected error: %v", err)
	}
}

func TestInsertHandlerForHTTP_EmptyEventsArray(t *testing.T) {
	body := `[{"EntityID":1,"Events":[],"ReportingAgentID":1}]`
	req := httptest.NewRequest("POST",
		"/newrelic/infra/v2/metrics/events/bulk",
		strings.NewReader(body))
	if err := InsertHandlerForHTTP(nil, req); err != nil {
		t.Fatalf("empty Events array caused unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// insertRows – direct end-to-end tests
// ---------------------------------------------------------------------------

func TestInsertRows_NilRows(t *testing.T) {
	if err := insertRows(nil, nil, nil); err != nil {
		t.Fatalf("nil rows returned unexpected error: %v", err)
	}
}

func TestInsertRows_EmptySlice(t *testing.T) {
	if err := insertRows(nil, []newrelic.Row{}, nil); err != nil {
		t.Fatalf("empty slice returned unexpected error: %v", err)
	}
}

func TestInsertRows_RowWithNoSamples(t *testing.T) {
	rows := []newrelic.Row{
		{
			Tags:      []newrelic.Tag{{Key: []byte("eventType"), Value: []byte("SystemSample")}},
			Samples:   nil,
			Timestamp: 1690286061000,
		},
	}
	if err := insertRows(nil, rows, nil); err != nil {
		t.Fatalf("row with no samples returned unexpected error: %v", err)
	}
}

func TestInsertRows_SingleSampleNoExtraLabels(t *testing.T) {
	rows := []newrelic.Row{
		{
			Tags: []newrelic.Tag{
				{Key: []byte("eventType"), Value: []byte("SystemSample")},
				{Key: []byte("host"), Value: []byte("myhost")},
			},
			Samples: []newrelic.Sample{
				{Name: []byte("cpuPercent"), Value: 55.0},
			},
			Timestamp: 1690286061000,
		},
	}
	if err := insertRows(nil, rows, nil); err != nil {
		t.Fatalf("single sample returned unexpected error: %v", err)
	}
}

func TestInsertRows_ExtraLabelsAppended(t *testing.T) {
	rows := []newrelic.Row{
		{
			Tags:    []newrelic.Tag{{Key: []byte("eventType"), Value: []byte("SystemSample")}},
			Samples: []newrelic.Sample{{Name: []byte("memFree"), Value: 1024}},
			Timestamp: 1690286061000,
		},
	}
	extraLabels := []prompb.Label{
		{Name: "env", Value: "prod"},
		{Name: "region", Value: "us-east-1"},
	}
	if err := insertRows(nil, rows, extraLabels); err != nil {
		t.Fatalf("extra labels returned unexpected error: %v", err)
	}
}

func TestInsertRows_MultipleRowsMultipleSamples(t *testing.T) {
	rows := []newrelic.Row{
		{
			Tags: []newrelic.Tag{
				{Key: []byte("eventType"), Value: []byte("SystemSample")},
				{Key: []byte("host"), Value: []byte("host1")},
			},
			Samples: []newrelic.Sample{
				{Name: []byte("cpuPercent"), Value: 10.0},
				{Name: []byte("memFree"), Value: 2048},
				{Name: []byte("diskWritesPerSecond"), Value: -1.5},
			},
			Timestamp: 1690286061000,
		},
		{
			Tags: []newrelic.Tag{
				{Key: []byte("eventType"), Value: []byte("ProcessSample")},
				{Key: []byte("host"), Value: []byte("host2")},
			},
			Samples: []newrelic.Sample{
				{Name: []byte("uptime"), Value: 999},
				{Name: []byte("threadCount"), Value: 42},
			},
			Timestamp: 1690286062000,
		},
		{
			Samples: []newrelic.Sample{
				{Name: []byte("bare_metric"), Value: 0},
			},
			Timestamp: 1690286063000,
		},
	}
	if err := insertRows(nil, rows, nil); err != nil {
		t.Fatalf("multiple rows returned unexpected error: %v", err)
	}
}

func TestInsertRows_NegativeAndZeroValues(t *testing.T) {
	rows := []newrelic.Row{
		{
			Tags:    []newrelic.Tag{{Key: []byte("eventType"), Value: []byte("StorageSample")}},
			Samples: []newrelic.Sample{
				{Name: []byte("ioReadBytesPerSecond"), Value: 0},
				{Name: []byte("diskWritesPerSecond"), Value: -34.21},
			},
			Timestamp: 1690286061000,
		},
	}
	if err := insertRows(nil, rows, nil); err != nil {
		t.Fatalf("negative/zero values returned unexpected error: %v", err)
	}
}

func TestInsertRows_MixedZeroAndNonZeroSampleRows(t *testing.T) {
	rows := []newrelic.Row{
		{Samples: nil, Timestamp: 1000},
		{
			Samples:   []newrelic.Sample{{Name: []byte("m1"), Value: 1}},
			Timestamp: 2000,
		},
		{Samples: nil, Timestamp: 3000},
		{
			Samples: []newrelic.Sample{
				{Name: []byte("m2"), Value: 2},
				{Name: []byte("m3"), Value: 3},
			},
			Timestamp: 4000,
		},
	}
	if err := insertRows(nil, rows, nil); err != nil {
		t.Fatalf("mixed zero/non-zero sample rows returned unexpected error: %v", err)
	}
}

func TestInsertRows_LargeNumberOfTagsAndSamples(t *testing.T) {
	tags := make([]newrelic.Tag, 10)
	for i := range tags {
		tags[i] = newrelic.Tag{
			Key:   []byte(fmt.Sprintf("tag%d", i)),
			Value: []byte(fmt.Sprintf("val%d", i)),
		}
	}
	samples := make([]newrelic.Sample, 20)
	for i := range samples {
		samples[i] = newrelic.Sample{
			Name:  []byte(fmt.Sprintf("metric%d", i)),
			Value: float64(i) * 1.5,
		}
	}
	rows := []newrelic.Row{
		{Tags: tags, Samples: samples, Timestamp: 1690286061000},
	}
	if err := insertRows(nil, rows, nil); err != nil {
		t.Fatalf("large payload returned unexpected error: %v", err)
	}
}

func TestInsertRows_TimestampZeroIsAllowed(t *testing.T) {
	rows := []newrelic.Row{
		{
			Tags:    []newrelic.Tag{{Key: []byte("eventType"), Value: []byte("NetworkSample")}},
			Samples: []newrelic.Sample{{Name: []byte("receivePacketsPerSecond"), Value: 100}},
			Timestamp: 0,
		},
	}
	if err := insertRows(nil, rows, nil); err != nil {
		t.Fatalf("zero timestamp returned unexpected error: %v", err)
	}
}

func TestInsertRows_NilAuthToken(t *testing.T) {
	rows := []newrelic.Row{
		{
			Samples:   []newrelic.Sample{{Name: []byte("m"), Value: 1}},
			Timestamp: 1000,
		},
	}
	if err := insertRows(nil, rows, nil); err != nil {
		t.Fatalf("nil auth.Token returned unexpected error: %v", err)
	}
}

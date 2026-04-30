package datadogv2

// Unit tests for app/vmagent/datadogv2/request_handler.go
//
// Key difference from vminsert: insertRows calls remotewrite.TryPush, which
// returns true (no-op) when rwctxsGlobal is nil.  Valid payloads succeed
// end-to-end without storage panics.
//
// Coverage:
//
//   InsertHandlerForHTTP
//     - malformed extra_label → error before body parsing
//     - multiple malformed extra_labels → error
//     - malformed JSON body → parse error
//     - empty body → parse error
//     - JSON array at top level → parse error
//     - series field not an array → parse error
//     - invalid protobuf body → parse error
//     - invalid gzip body → decompression error
//     - gzip-compressed malformed JSON → parse error
//     - gzip + protobuf invalid payload → error
//     - error message mentions "DataDog"
//     - valid JSON body → succeeds end-to-end
//     - valid gzip-compressed JSON body → succeeds end-to-end
//     - valid extra_label + valid body → succeeds
//     - empty series array → succeeds
//
//   insertRows
//     - nil series → succeeds
//     - empty series slice → succeeds
//     - single series, no points → succeeds
//     - single series with points → succeeds (timestamps multiplied ×1000)
//     - series with resources → succeeds
//     - series with source_type_name → succeeds
//     - series with tags including "host" rename to "exported_host" → succeeds
//     - extra labels forwarded → succeeds
//     - multiple series, multiple points → succeeds
//     - nil auth.Token → succeeds
//     - negative and zero values → succeeds

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/prompb"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/protoparser/datadogv2"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newV2Request(body string, headers map[string]string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/datadog/api/v2/series", strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

func gzipV2(t *testing.T, s string) *bytes.Buffer {
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

const validV2Body = `{
	"series": [
		{
			"metric": "system.cpu.user",
			"points": [{"timestamp": 1609459200, "value": 42.5}],
			"resources": [{"type": "host", "name": "web-01"}],
			"source_type_name": "System",
			"tags": ["env:prod", "region:us-east-1"]
		}
	]
}`

// ---------------------------------------------------------------------------
// InsertHandlerForHTTP – error paths
// ---------------------------------------------------------------------------

func TestInsertHandlerForHTTP_InvalidExtraLabel(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost,
		"/datadog/api/v2/series?extra_label=noequalssign",
		strings.NewReader(""))
	err := InsertHandlerForHTTP(nil, req)
	if err == nil {
		t.Fatal("expected error for malformed extra_label, got nil")
	}
	if !strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("expected error to mention 'extra_label', got: %v", err)
	}
}

func TestInsertHandlerForHTTP_MultipleInvalidExtraLabels(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost,
		"/datadog/api/v2/series?extra_label=env=prod&extra_label=badlabel",
		strings.NewReader(""))
	err := InsertHandlerForHTTP(nil, req)
	if err == nil {
		t.Fatal("expected error for malformed extra_label, got nil")
	}
	if !strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("error does not mention 'extra_label': %v", err)
	}
}

func TestInsertHandlerForHTTP_MalformedJSON(t *testing.T) {
	req := newV2Request(`{bad json}`, nil)
	err := InsertHandlerForHTTP(nil, req)
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

func TestInsertHandlerForHTTP_EmptyBody(t *testing.T) {
	req := newV2Request(``, nil)
	err := InsertHandlerForHTTP(nil, req)
	if err == nil {
		t.Fatal("expected error for empty body, got nil")
	}
}

func TestInsertHandlerForHTTP_JSONArrayBody(t *testing.T) {
	req := newV2Request(`[1,2,3]`, nil)
	err := InsertHandlerForHTTP(nil, req)
	if err == nil {
		t.Fatal("expected error for JSON array body, got nil")
	}
}

func TestInsertHandlerForHTTP_SeriesFieldWrongType(t *testing.T) {
	req := newV2Request(`{"series":"notarray"}`, nil)
	err := InsertHandlerForHTTP(nil, req)
	if err == nil {
		t.Fatal("expected error when series is not an array, got nil")
	}
}

func TestInsertHandlerForHTTP_ProtobufInvalidBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/datadog/api/v2/series",
		bytes.NewReader([]byte{0xFF, 0xFF, 0x01, 0x02, 0x03}))
	req.Header.Set("Content-Type", "application/x-protobuf")
	err := InsertHandlerForHTTP(nil, req)
	if err == nil {
		t.Fatal("expected error for invalid protobuf body, got nil")
	}
}

func TestInsertHandlerForHTTP_InvalidGzipBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/datadog/api/v2/series",
		strings.NewReader("this is not valid gzip data"))
	req.Header.Set("Content-Encoding", "gzip")
	err := InsertHandlerForHTTP(nil, req)
	if err == nil {
		t.Fatal("expected error for invalid gzip body, got nil")
	}
}

func TestInsertHandlerForHTTP_GzipMalformedJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/datadog/api/v2/series", gzipV2(t, `{not json}`))
	req.Header.Set("Content-Encoding", "gzip")
	err := InsertHandlerForHTTP(nil, req)
	if err == nil {
		t.Fatal("expected error for gzip-wrapped invalid JSON, got nil")
	}
}

func TestInsertHandlerForHTTP_GzipProtobufInvalidPayload(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/datadog/api/v2/series",
		gzipV2(t, string([]byte{0xAB, 0xCD, 0xEF})))
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("Content-Type", "application/x-protobuf")
	err := InsertHandlerForHTTP(nil, req)
	if err == nil {
		t.Fatal("expected error for gzip-wrapped invalid protobuf, got nil")
	}
}

func TestInsertHandlerForHTTP_ErrorMentionsDataDog(t *testing.T) {
	req := newV2Request(`{broken`, nil)
	err := InsertHandlerForHTTP(nil, req)
	if err == nil {
		t.Fatal("expected error for broken JSON, got nil")
	}
	if !strings.Contains(err.Error(), "DataDog") {
		t.Fatalf("expected error to mention 'DataDog', got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// InsertHandlerForHTTP – success paths
// ---------------------------------------------------------------------------

func TestInsertHandlerForHTTP_ValidBody(t *testing.T) {
	req := newV2Request(validV2Body, nil)
	if err := InsertHandlerForHTTP(nil, req); err != nil {
		t.Fatalf("valid body caused unexpected error: %v", err)
	}
}

func TestInsertHandlerForHTTP_GzipValidBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/datadog/api/v2/series", gzipV2(t, validV2Body))
	req.Header.Set("Content-Encoding", "gzip")
	if err := InsertHandlerForHTTP(nil, req); err != nil {
		t.Fatalf("gzip valid body caused unexpected error: %v", err)
	}
}

func TestInsertHandlerForHTTP_ValidExtraLabel(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost,
		"/datadog/api/v2/series?extra_label=env=staging",
		strings.NewReader(validV2Body))
	if err := InsertHandlerForHTTP(nil, req); err != nil {
		t.Fatalf("valid extra_label caused unexpected error: %v", err)
	}
}

func TestInsertHandlerForHTTP_EmptySeriesArray(t *testing.T) {
	req := newV2Request(`{"series":[]}`, nil)
	if err := InsertHandlerForHTTP(nil, req); err != nil {
		t.Fatalf("empty series array caused unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// insertRows – direct end-to-end tests
// ---------------------------------------------------------------------------

func TestInsertRows_NilSeries(t *testing.T) {
	if err := insertRows(nil, nil, nil); err != nil {
		t.Fatalf("nil series returned unexpected error: %v", err)
	}
}

func TestInsertRows_EmptySlice(t *testing.T) {
	if err := insertRows(nil, []datadogv2.Series{}, nil); err != nil {
		t.Fatalf("empty slice returned unexpected error: %v", err)
	}
}

func TestInsertRows_SingleSeriesNoPoints(t *testing.T) {
	series := []datadogv2.Series{
		{Metric: "test.metric", Points: nil},
	}
	if err := insertRows(nil, series, nil); err != nil {
		t.Fatalf("series with no points returned unexpected error: %v", err)
	}
}

func TestInsertRows_SingleSeriesWithPoints(t *testing.T) {
	series := []datadogv2.Series{
		{
			Metric: "system.cpu.user",
			Points: []datadogv2.Point{
				{Timestamp: 1609459200, Value: 42.5},
				{Timestamp: 1609459260, Value: 43.1},
			},
		},
	}
	if err := insertRows(nil, series, nil); err != nil {
		t.Fatalf("series with points returned unexpected error: %v", err)
	}
}

func TestInsertRows_WithResources(t *testing.T) {
	series := []datadogv2.Series{
		{
			Metric: "net.bytes_sent",
			Resources: []datadogv2.Resource{
				{Type: "host", Name: "web-01"},
				{Type: "service", Name: "nginx"},
			},
			Points: []datadogv2.Point{{Timestamp: 1609459200, Value: 1000}},
		},
	}
	if err := insertRows(nil, series, nil); err != nil {
		t.Fatalf("series with resources returned unexpected error: %v", err)
	}
}

func TestInsertRows_WithSourceTypeName(t *testing.T) {
	series := []datadogv2.Series{
		{
			Metric:         "k8s.pod.cpu",
			SourceTypeName: "kubernetes",
			Points:         []datadogv2.Point{{Timestamp: 1609459200, Value: 0.5}},
		},
	}
	if err := insertRows(nil, series, nil); err != nil {
		t.Fatalf("series with source_type_name returned unexpected error: %v", err)
	}
}

func TestInsertRows_TagsWithHostRename(t *testing.T) {
	// "host" tag in Tags must be renamed to "exported_host".
	series := []datadogv2.Series{
		{
			Metric: "system.mem.used",
			Tags:   []string{"env:prod", "host:tag-host", "region:eu-west"},
			Points: []datadogv2.Point{{Timestamp: 1609459200, Value: 512.0}},
		},
	}
	if err := insertRows(nil, series, nil); err != nil {
		t.Fatalf("tags with host rename returned unexpected error: %v", err)
	}
}

func TestInsertRows_ExtraLabels(t *testing.T) {
	series := []datadogv2.Series{
		{
			Metric: "custom.metric",
			Points: []datadogv2.Point{{Timestamp: 1609459200, Value: 7.0}},
		},
	}
	extraLabels := []prompb.Label{
		{Name: "datacenter", Value: "dc1"},
		{Name: "team", Value: "ops"},
	}
	if err := insertRows(nil, series, extraLabels); err != nil {
		t.Fatalf("extra labels returned unexpected error: %v", err)
	}
}

func TestInsertRows_MultipleSeriesMultiplePoints(t *testing.T) {
	series := []datadogv2.Series{
		{
			Metric: "net.bytes_sent",
			Resources: []datadogv2.Resource{{Type: "host", Name: "host1"}},
			Tags:      []string{"interface:eth0"},
			Points: []datadogv2.Point{
				{Timestamp: 1609459200, Value: 1000},
				{Timestamp: 1609459260, Value: 2000},
				{Timestamp: 1609459320, Value: 3000},
			},
		},
		{
			Metric: "disk.free",
			Points: []datadogv2.Point{
				{Timestamp: 1609459200, Value: 10000},
			},
		},
		{
			Metric: "no.points",
		},
	}
	if err := insertRows(nil, series, nil); err != nil {
		t.Fatalf("multiple series/points returned unexpected error: %v", err)
	}
}

func TestInsertRows_NilAuthToken(t *testing.T) {
	series := []datadogv2.Series{
		{Metric: "m", Points: []datadogv2.Point{{Timestamp: 1609459200, Value: 1.0}}},
	}
	if err := insertRows(nil, series, nil); err != nil {
		t.Fatalf("nil auth.Token returned unexpected error: %v", err)
	}
}

func TestInsertRows_NegativeAndZeroValues(t *testing.T) {
	series := []datadogv2.Series{
		{
			Metric: "delta.metric",
			Points: []datadogv2.Point{
				{Timestamp: 1609459200, Value: 0},
				{Timestamp: 1609459260, Value: -42.0},
			},
		},
	}
	if err := insertRows(nil, series, nil); err != nil {
		t.Fatalf("negative/zero point values returned unexpected error: %v", err)
	}
}

func TestInsertRows_LargeNumberOfTagsAndPoints(t *testing.T) {
	tags := make([]string, 10)
	for i := range tags {
		tags[i] = fmt.Sprintf("tag%d:val%d", i, i)
	}
	points := make([]datadogv2.Point, 20)
	for i := range points {
		points[i] = datadogv2.Point{Timestamp: int64(1609459200 + i*60), Value: float64(i)}
	}
	series := []datadogv2.Series{
		{Metric: "big.metric", Tags: tags, Points: points},
	}
	if err := insertRows(nil, series, nil); err != nil {
		t.Fatalf("large payload returned unexpected error: %v", err)
	}
}

func TestInsertRows_TimestampMultipliedBy1000(t *testing.T) {
	// datadogv2 timestamps are in seconds; insertRows multiplies by 1000.
	// This test verifies no panic occurs and the call succeeds.
	series := []datadogv2.Series{
		{
			Metric: "ts.check",
			Points: []datadogv2.Point{
				{Timestamp: 0, Value: 1},     // zero ts → multiplied stays 0
				{Timestamp: 1609459200, Value: 2}, // normal ts
			},
		},
	}
	if err := insertRows(nil, series, nil); err != nil {
		t.Fatalf("timestamp multiplication returned unexpected error: %v", err)
	}
}

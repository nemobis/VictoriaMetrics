package datadogv1

// Unit tests for app/vmagent/datadogv1/request_handler.go
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
//     - invalid gzip body → decompression error
//     - gzip-compressed malformed JSON → parse error
//     - unknown encoding + malformed body → parse error
//     - error message mentions "DataDog"
//     - valid body (series with points) → succeeds end-to-end
//     - gzip-compressed valid body → succeeds end-to-end
//     - valid extra_label + valid body → succeeds
//
//   insertRows
//     - nil series → succeeds (empty TryPush)
//     - empty series slice → succeeds
//     - single series, no points → succeeds
//     - single series with points → succeeds
//     - series with host and device fields → succeeds
//     - series with tags → succeeds (tag splitting via datadogutil.SplitTag)
//     - series with "host" tag renamed to "exported_host" → succeeds
//     - extra labels forwarded → succeeds
//     - multiple series, multiple points → succeeds
//     - nil auth.Token → succeeds

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/prompb"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/protoparser/datadogv1"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newV1Request(body string, headers map[string]string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/datadog/api/v1/series", strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

func gzipV1(t *testing.T, s string) *bytes.Buffer {
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

// captureFirstSeriesLabels installs insertRowsHook to capture a copy of the
// labels from the first TimeSeries in the WriteRequest.  The hook is cleared
// via t.Cleanup.  Call the returned getter after insertRows to retrieve them.
func captureFirstSeriesLabels(t *testing.T) func() []prompb.Label {
	t.Helper()
	var captured []prompb.Label
	insertRowsHook = func(wr *prompb.WriteRequest) {
		if len(wr.Timeseries) > 0 {
			lbls := make([]prompb.Label, len(wr.Timeseries[0].Labels))
			copy(lbls, wr.Timeseries[0].Labels)
			captured = lbls
		}
	}
	t.Cleanup(func() { insertRowsHook = nil })
	return func() []prompb.Label { return captured }
}

// toLabelMap converts a label slice to a name→value map for easy assertion.
func toLabelMap(labels []prompb.Label) map[string]string {
	m := make(map[string]string, len(labels))
	for _, l := range labels {
		m[l.Name] = l.Value
	}
	return m
}

const validV1Body = `{
	"series": [
		{
			"metric": "system.cpu.user",
			"host": "web-01",
			"tags": ["env:prod", "region:us-east-1"],
			"points": [[1609459200, 42.5], [1609459260, 43.1]]
		}
	]
}`

// ---------------------------------------------------------------------------
// InsertHandlerForHTTP – error paths
// ---------------------------------------------------------------------------

func TestInsertHandlerForHTTP_InvalidExtraLabel(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost,
		"/datadog/api/v1/series?extra_label=no-equals-sign",
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
		"/datadog/api/v1/series?extra_label=good=val&extra_label=badlabel",
		strings.NewReader(""))
	err := InsertHandlerForHTTP(nil, req)
	if err == nil {
		t.Fatal("expected error for malformed extra_label among valid ones, got nil")
	}
	if !strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("error does not mention 'extra_label': %v", err)
	}
}

func TestInsertHandlerForHTTP_MalformedJSON(t *testing.T) {
	req := newV1Request(`{not valid json}`, nil)
	err := InsertHandlerForHTTP(nil, req)
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

func TestInsertHandlerForHTTP_EmptyBody(t *testing.T) {
	req := newV1Request(``, nil)
	err := InsertHandlerForHTTP(nil, req)
	if err == nil {
		t.Fatal("expected error for empty body, got nil")
	}
}

func TestInsertHandlerForHTTP_NotAnObject(t *testing.T) {
	req := newV1Request(`[1,2,3]`, nil)
	err := InsertHandlerForHTTP(nil, req)
	if err == nil {
		t.Fatal("expected error for JSON array body, got nil")
	}
}

func TestInsertHandlerForHTTP_SeriesNotArray(t *testing.T) {
	req := newV1Request(`{"series": 42}`, nil)
	err := InsertHandlerForHTTP(nil, req)
	if err == nil {
		t.Fatal("expected error when series is not an array, got nil")
	}
}

func TestInsertHandlerForHTTP_InvalidGzipBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/datadog/api/v1/series",
		strings.NewReader("this is not valid gzip data"))
	req.Header.Set("Content-Encoding", "gzip")
	err := InsertHandlerForHTTP(nil, req)
	if err == nil {
		t.Fatal("expected error for invalid gzip body, got nil")
	}
}

func TestInsertHandlerForHTTP_GzipMalformedJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/datadog/api/v1/series", gzipV1(t, `{not json}`))
	req.Header.Set("Content-Encoding", "gzip")
	err := InsertHandlerForHTTP(nil, req)
	if err == nil {
		t.Fatal("expected error for gzip-wrapped invalid JSON, got nil")
	}
}

func TestInsertHandlerForHTTP_UnknownEncodingMalformedBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/datadog/api/v1/series",
		strings.NewReader(`{bad}`))
	req.Header.Set("Content-Encoding", "identity")
	err := InsertHandlerForHTTP(nil, req)
	if err == nil {
		t.Fatal("expected error for malformed body with unknown encoding, got nil")
	}
}

func TestInsertHandlerForHTTP_ErrorMentionsDataDog(t *testing.T) {
	req := newV1Request(`{broken`, nil)
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
	req := newV1Request(validV1Body, nil)
	if err := InsertHandlerForHTTP(nil, req); err != nil {
		t.Fatalf("valid body caused unexpected error: %v", err)
	}
}

func TestInsertHandlerForHTTP_GzipValidBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/datadog/api/v1/series", gzipV1(t, validV1Body))
	req.Header.Set("Content-Encoding", "gzip")
	if err := InsertHandlerForHTTP(nil, req); err != nil {
		t.Fatalf("gzip valid body caused unexpected error: %v", err)
	}
}

func TestInsertHandlerForHTTP_ValidExtraLabel(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost,
		"/datadog/api/v1/series?extra_label=env=staging",
		strings.NewReader(validV1Body))
	if err := InsertHandlerForHTTP(nil, req); err != nil {
		t.Fatalf("valid extra_label caused unexpected error: %v", err)
	}
}

func TestInsertHandlerForHTTP_EmptySeriesArray(t *testing.T) {
	req := newV1Request(`{"series":[]}`, nil)
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
	if err := insertRows(nil, []datadogv1.Series{}, nil); err != nil {
		t.Fatalf("empty slice returned unexpected error: %v", err)
	}
}

func TestInsertRows_SingleSeriesNoPoints(t *testing.T) {
	series := []datadogv1.Series{
		{Metric: "test.metric", Points: nil},
	}
	if err := insertRows(nil, series, nil); err != nil {
		t.Fatalf("series with no points returned unexpected error: %v", err)
	}
}

func TestInsertRows_SingleSeriesWithPoints(t *testing.T) {
	series := []datadogv1.Series{
		{
			Metric: "system.cpu.user",
			Points: []datadogv1.Point{
				{1609459200, 42.5},
				{1609459260, 43.1},
			},
		},
	}
	if err := insertRows(nil, series, nil); err != nil {
		t.Fatalf("series with points returned unexpected error: %v", err)
	}
}

func TestInsertRows_HostAndDeviceFields(t *testing.T) {
	getLabels := captureFirstSeriesLabels(t)

	series := []datadogv1.Series{
		{
			Metric: "disk.io",
			Host:   "web-01",
			Device: "/dev/sda1",
			Points: []datadogv1.Point{{1609459200, 100.0}},
		},
	}
	if err := insertRows(nil, series, nil); err != nil {
		t.Fatalf("host/device fields returned unexpected error: %v", err)
	}

	got := toLabelMap(getLabels())
	if got["__name__"] != "disk.io" {
		t.Errorf("__name__: got %q, want %q", got["__name__"], "disk.io")
	}
	if got["host"] != "web-01" {
		t.Errorf("host: got %q, want %q", got["host"], "web-01")
	}
	if got["device"] != "/dev/sda1" {
		t.Errorf("device: got %q, want %q", got["device"], "/dev/sda1")
	}
}

func TestInsertRows_TagSplitting(t *testing.T) {
	getLabels := captureFirstSeriesLabels(t)

	series := []datadogv1.Series{
		{
			Metric: "system.load.1",
			Tags:   []string{"env:prod", "region:us-east-1", "notag"},
			Points: []datadogv1.Point{{1609459200, 1.5}},
		},
	}
	if err := insertRows(nil, series, nil); err != nil {
		t.Fatalf("tag splitting returned unexpected error: %v", err)
	}

	got := toLabelMap(getLabels())
	if got["env"] != "prod" {
		t.Errorf("env: got %q, want %q", got["env"], "prod")
	}
	if got["region"] != "us-east-1" {
		t.Errorf("region: got %q, want %q", got["region"], "us-east-1")
	}
	// A tag with no ":" separator produces an empty value.
	if _, ok := got["notag"]; !ok {
		t.Errorf("expected label 'notag' to be present (split with empty value)")
	}
}

func TestInsertRows_HostTagRenamedToExportedHost(t *testing.T) {
	// A "host" tag in Tags must be renamed to "exported_host" to avoid
	// collision with the top-level Host field (verified via insertRowsHook).
	getLabels := captureFirstSeriesLabels(t)

	series := []datadogv1.Series{
		{
			Metric: "system.mem.used",
			Host:   "real-host",
			Tags:   []string{"host:tag-host"},
			Points: []datadogv1.Point{{1609459200, 512.0}},
		},
	}
	if err := insertRows(nil, series, nil); err != nil {
		t.Fatalf("host tag rename returned unexpected error: %v", err)
	}

	captured := getLabels()
	got := toLabelMap(captured)
	// Top-level Host field must appear as "host".
	if got["host"] != "real-host" {
		t.Errorf("host: got %q, want %q", got["host"], "real-host")
	}
	// Tags["host:tag-host"] must be renamed to "exported_host".
	if got["exported_host"] != "tag-host" {
		t.Errorf("exported_host: got %q, want %q", got["exported_host"], "tag-host")
	}
	// The raw "host" key from Tags must NOT appear twice (it was renamed).
	count := 0
	for _, l := range captured {
		if l.Name == "host" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly one 'host' label (from Host field), got %d", count)
	}
}

func TestInsertRows_ExtraLabels(t *testing.T) {
	getLabels := captureFirstSeriesLabels(t)

	series := []datadogv1.Series{
		{
			Metric: "custom.metric",
			Points: []datadogv1.Point{{1609459200, 7.0}},
		},
	}
	extraLabels := []prompb.Label{
		{Name: "datacenter", Value: "dc1"},
		{Name: "team", Value: "ops"},
	}
	if err := insertRows(nil, series, extraLabels); err != nil {
		t.Fatalf("extra labels returned unexpected error: %v", err)
	}

	got := toLabelMap(getLabels())
	if got["datacenter"] != "dc1" {
		t.Errorf("datacenter: got %q, want %q", got["datacenter"], "dc1")
	}
	if got["team"] != "ops" {
		t.Errorf("team: got %q, want %q", got["team"], "ops")
	}
}

func TestInsertRows_MultipleSeriesMultiplePoints(t *testing.T) {
	series := []datadogv1.Series{
		{
			Metric: "net.bytes_sent",
			Host:   "host1",
			Tags:   []string{"interface:eth0"},
			Points: []datadogv1.Point{
				{1609459200, 1000},
				{1609459260, 2000},
				{1609459320, 3000},
			},
		},
		{
			Metric: "net.bytes_recv",
			Host:   "host1",
			Tags:   []string{"interface:eth0"},
			Points: []datadogv1.Point{
				{1609459200, 500},
				{1609459260, 600},
			},
		},
		{
			Metric: "disk.free",
			Device: "/dev/sda1",
			Tags:   nil,
			Points: []datadogv1.Point{{1609459200, 10000}},
		},
	}
	if err := insertRows(nil, series, nil); err != nil {
		t.Fatalf("multiple series/points returned unexpected error: %v", err)
	}
}

func TestInsertRows_NilAuthToken(t *testing.T) {
	series := []datadogv1.Series{
		{Metric: "m", Points: []datadogv1.Point{{1609459200, 1.0}}},
	}
	if err := insertRows(nil, series, nil); err != nil {
		t.Fatalf("nil auth.Token returned unexpected error: %v", err)
	}
}

func TestInsertRows_NegativeAndZeroValues(t *testing.T) {
	series := []datadogv1.Series{
		{
			Metric: "delta.metric",
			Points: []datadogv1.Point{
				{1609459200, 0},
				{1609459260, -42.0},
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
	points := make([]datadogv1.Point, 20)
	for i := range points {
		points[i] = datadogv1.Point{float64(1609459200 + i*60), float64(i)}
	}
	series := []datadogv1.Series{
		{Metric: "big.metric", Tags: tags, Points: points},
	}
	if err := insertRows(nil, series, nil); err != nil {
		t.Fatalf("large payload returned unexpected error: %v", err)
	}
}

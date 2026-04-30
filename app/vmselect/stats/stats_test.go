package stats

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/querytracer"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/storage/metricnamestats"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newTracer() *querytracer.Tracer {
	return querytracer.New(false, "test")
}

func makeStatsResult(records []metricnamestats.StatRecord) *metricnamestats.StatsResult {
	return &metricnamestats.StatsResult{
		CollectedSinceTs: 1000,
		TotalRecords:     uint64(len(records)),
		MaxSizeBytes:     1 << 20,
		CurrentSizeBytes: 512,
		Records:          records,
	}
}

// responseBody runs MetricNamesStatsResponse and returns the JSON string.
func responseBody(stats *metricnamestats.StatsResult) string {
	qt := newTracer()
	return MetricNamesStatsResponse(stats, qt)
}

// ---------------------------------------------------------------------------
// MetricNamesStatsResponse / WriteMetricNamesStatsResponse
// ---------------------------------------------------------------------------

// TestMetricNamesStatsResponse_StatusSuccess verifies the top-level "status"
// field is "success".
func TestMetricNamesStatsResponse_StatusSuccess(t *testing.T) {
	body := responseBody(makeStatsResult(nil))
	if !strings.Contains(body, `"status":"success"`) {
		t.Errorf("expected status:success in response\ngot: %s", body)
	}
}

// TestMetricNamesStatsResponse_ValidJSON verifies the output is valid JSON.
func TestMetricNamesStatsResponse_ValidJSON(t *testing.T) {
	body := responseBody(makeStatsResult(nil))
	var v map[string]any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Errorf("response is not valid JSON: %v\nbody: %s", err, body)
	}
}

// TestMetricNamesStatsResponse_TopLevelFields verifies all expected top-level
// keys are present.
func TestMetricNamesStatsResponse_TopLevelFields(t *testing.T) {
	body := responseBody(makeStatsResult(nil))
	var v map[string]any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	for _, key := range []string{
		"status",
		"statsCollectedSince",
		"statsCollectedRecordsTotal",
		"trackerMemoryMaxSizeBytes",
		"trackerCurrentMemoryUsageBytes",
		"records",
	} {
		if _, ok := v[key]; !ok {
			t.Errorf("missing key %q in response\nbody: %s", key, body)
		}
	}
}

// TestMetricNamesStatsResponse_EmptyRecords verifies that with no records the
// "records" field is an empty JSON array.
func TestMetricNamesStatsResponse_EmptyRecords(t *testing.T) {
	body := responseBody(makeStatsResult(nil))
	if !strings.Contains(body, `"records":[]`) {
		t.Errorf("expected empty records array\ngot: %s", body)
	}
}

// TestMetricNamesStatsResponse_RecordFields verifies each record has the
// expected fields.
func TestMetricNamesStatsResponse_RecordFields(t *testing.T) {
	records := []metricnamestats.StatRecord{
		{MetricName: "http_requests_total", RequestsCount: 42, LastRequestTs: 9999},
	}
	body := responseBody(makeStatsResult(records))

	var v map[string]any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	recs, ok := v["records"].([]any)
	if !ok || len(recs) != 1 {
		t.Fatalf("expected 1 record, got: %v", v["records"])
	}
	rec, ok := recs[0].(map[string]any)
	if !ok {
		t.Fatalf("record is not a JSON object: %v", recs[0])
	}
	for _, field := range []string{"metricName", "queryRequestsCount", "lastQueryRequestTimestamp"} {
		if _, ok := rec[field]; !ok {
			t.Errorf("record missing field %q\nrecord: %v", field, rec)
		}
	}
}

// TestMetricNamesStatsResponse_RecordValues verifies numeric and string values
// are correctly serialised.
func TestMetricNamesStatsResponse_RecordValues(t *testing.T) {
	records := []metricnamestats.StatRecord{
		{MetricName: "go_goroutines", RequestsCount: 7, LastRequestTs: 12345},
	}
	body := responseBody(makeStatsResult(records))

	if !strings.Contains(body, `"go_goroutines"`) {
		t.Errorf("metric name missing from response\ngot: %s", body)
	}
	if !strings.Contains(body, "7") {
		t.Errorf("requests count 7 missing from response\ngot: %s", body)
	}
	if !strings.Contains(body, "12345") {
		t.Errorf("last request ts 12345 missing from response\ngot: %s", body)
	}
}

// TestMetricNamesStatsResponse_MultipleRecords verifies all records are present
// and separated by commas in the JSON array.
func TestMetricNamesStatsResponse_MultipleRecords(t *testing.T) {
	records := []metricnamestats.StatRecord{
		{MetricName: "alpha", RequestsCount: 1, LastRequestTs: 100},
		{MetricName: "beta", RequestsCount: 2, LastRequestTs: 200},
		{MetricName: "gamma", RequestsCount: 3, LastRequestTs: 300},
	}
	body := responseBody(makeStatsResult(records))

	var v map[string]any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	recs, ok := v["records"].([]any)
	if !ok {
		t.Fatalf("records field is not an array")
	}
	if len(recs) != 3 {
		t.Errorf("expected 3 records, got %d\nbody: %s", len(recs), body)
	}
}

// TestMetricNamesStatsResponse_StatsFields verifies the numeric metadata fields
// are serialised with the values from the StatsResult.
func TestMetricNamesStatsResponse_StatsFields(t *testing.T) {
	sr := &metricnamestats.StatsResult{
		CollectedSinceTs: 55555,
		TotalRecords:     77,
		MaxSizeBytes:     1048576,
		CurrentSizeBytes: 2048,
	}
	body := MetricNamesStatsResponse(sr, newTracer())

	var v map[string]any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	check := func(key string, want float64) {
		t.Helper()
		got, ok := v[key].(float64)
		if !ok {
			t.Errorf("key %q not found or not a number in response\nbody: %s", key, body)
			return
		}
		if got != want {
			t.Errorf("key %q: want %v, got %v", key, want, got)
		}
	}
	check("statsCollectedSince", 55555)
	check("statsCollectedRecordsTotal", 77)
	check("trackerMemoryMaxSizeBytes", 1048576)
	check("trackerCurrentMemoryUsageBytes", 2048)
}

// TestMetricNamesStatsResponse_WriteVsString verifies WriteMetricNamesStatsResponse
// produces identical output to MetricNamesStatsResponse.
func TestMetricNamesStatsResponse_WriteVsString(t *testing.T) {
	sr := makeStatsResult([]metricnamestats.StatRecord{
		{MetricName: "m1", RequestsCount: 3, LastRequestTs: 111},
	})
	qt := newTracer()

	fromString := MetricNamesStatsResponse(sr, qt)

	// Reset qt for second call (it's disabled so Done() is a no-op).
	qt2 := newTracer()
	var sb strings.Builder
	WriteMetricNamesStatsResponse(&sb, sr, qt2)
	fromWrite := sb.String()

	if fromString != fromWrite {
		t.Errorf("MetricNamesStatsResponse and WriteMetricNamesStatsResponse differ\nString: %s\nWrite:  %s", fromString, fromWrite)
	}
}

// ---------------------------------------------------------------------------
// MetricNamesStatsHandler — argument parsing
// ---------------------------------------------------------------------------

// TestMetricNamesStatsHandler_InvalidLimit verifies that a non-numeric "limit"
// query parameter causes an error response.
func TestMetricNamesStatsHandler_InvalidLimit(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/?limit=abc", nil)
	w := httptest.NewRecorder()
	qt := newTracer()

	err := MetricNamesStatsHandler(qt, w, req)
	if err == nil {
		t.Fatal("expected error for invalid limit, got nil")
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Errorf("error should mention 'limit', got: %v", err)
	}
}

// TestMetricNamesStatsHandler_InvalidLe verifies that a non-numeric "le"
// query parameter causes an error response.
func TestMetricNamesStatsHandler_InvalidLe(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/?le=notanumber", nil)
	w := httptest.NewRecorder()
	qt := newTracer()

	err := MetricNamesStatsHandler(qt, w, req)
	if err == nil {
		t.Fatal("expected error for invalid le, got nil")
	}
	if !strings.Contains(err.Error(), "le") {
		t.Errorf("error should mention 'le', got: %v", err)
	}
}

// TestMetricNamesStatsHandler_InvalidMatchPattern verifies that an invalid
// regex in "match_pattern" causes an error response.
func TestMetricNamesStatsHandler_InvalidMatchPattern(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/?match_pattern=%5B%5Binvalid", nil)
	w := httptest.NewRecorder()
	qt := newTracer()

	err := MetricNamesStatsHandler(qt, w, req)
	if err == nil {
		t.Fatal("expected error for invalid match_pattern regex, got nil")
	}
	if !strings.Contains(err.Error(), "match_pattern") {
		t.Errorf("error should mention 'match_pattern', got: %v", err)
	}
}


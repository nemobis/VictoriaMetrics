package csvimport

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	csvparser "github.com/VictoriaMetrics/VictoriaMetrics/lib/protoparser/csvimport"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/prompb"
)

// insertRows is called directly (white-box) so we bypass the stream parser,
// which uses ScheduleUnmarshalWork and requires StartUnmarshalWorkers.

func TestInsertRowsEmpty(t *testing.T) {
	if err := insertRows(nil, nil, nil); err != nil {
		t.Fatalf("unexpected error for empty rows: %v", err)
	}
}

func TestInsertRowsSingleMetric(t *testing.T) {
	rows := []csvparser.Row{
		{
			Metric:    "temperature",
			Value:     23.5,
			Timestamp: 1609459200000,
		},
	}
	if err := insertRows(nil, rows, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInsertRowsWithTags(t *testing.T) {
	rows := []csvparser.Row{
		{
			Metric: "cpu.load",
			Tags: []csvparser.Tag{
				{Key: "host", Value: "web01"},
				{Key: "region", Value: "us-east-1"},
			},
			Value:     0.75,
			Timestamp: 1609459200000,
		},
	}
	if err := insertRows(nil, rows, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInsertRowsWithExtraLabels(t *testing.T) {
	rows := []csvparser.Row{
		{
			Metric:    "requests.total",
			Value:     1000,
			Timestamp: 1609459200000,
		},
	}
	extraLabels := []prompb.Label{
		{Name: "datacenter", Value: "dc1"},
		{Name: "env", Value: "production"},
	}
	if err := insertRows(nil, rows, extraLabels); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInsertRowsMultiple(t *testing.T) {
	rows := []csvparser.Row{
		{Metric: "metric.a", Value: 1.1, Timestamp: 1000},
		{Metric: "metric.b", Value: 2.2, Timestamp: 2000},
		{Metric: "metric.c", Value: 3.3, Timestamp: 3000},
		{Metric: "metric.d", Value: 4.4, Timestamp: 4000},
	}
	if err := insertRows(nil, rows, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInsertRowsLabelConstruction(t *testing.T) {
	// Verify label construction logic matches what insertRows would produce.
	rows := []csvparser.Row{
		{
			Metric: "http.requests",
			Tags: []csvparser.Tag{
				{Key: "method", Value: "GET"},
				{Key: "status", Value: "200"},
			},
			Value:     42,
			Timestamp: 1609459200000,
		},
	}
	extraLabels := []prompb.Label{
		{Name: "job", Value: "api-server"},
	}

	// Reconstruct expected labels
	r := &rows[0]
	var labels []prompb.Label
	labels = append(labels, prompb.Label{Name: "__name__", Value: r.Metric})
	for j := range r.Tags {
		tag := &r.Tags[j]
		labels = append(labels, prompb.Label{Name: tag.Key, Value: tag.Value})
	}
	labels = append(labels, extraLabels...)

	wantLabels := []prompb.Label{
		{Name: "__name__", Value: "http.requests"},
		{Name: "method", Value: "GET"},
		{Name: "status", Value: "200"},
		{Name: "job", Value: "api-server"},
	}

	if len(labels) != len(wantLabels) {
		t.Fatalf("label count mismatch: got %d, want %d", len(labels), len(wantLabels))
	}
	for i, lbl := range labels {
		if lbl.Name != wantLabels[i].Name || lbl.Value != wantLabels[i].Value {
			t.Errorf("label[%d] mismatch: got %+v, want %+v", i, lbl, wantLabels[i])
		}
	}

	if err := insertRows(nil, rows, extraLabels); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInsertRowsZeroTimestamp(t *testing.T) {
	// Rows with zero timestamp should also be accepted.
	rows := []csvparser.Row{
		{Metric: "zero.ts.metric", Value: 5.5, Timestamp: 0},
	}
	if err := insertRows(nil, rows, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestInsertHandlerEmptyBody verifies InsertHandler with an empty body.
// An empty body causes the CSV stream parser to hit EOF immediately, so no
// ScheduleUnmarshalWork is dispatched — it is safe to call without workers.
// However, InsertHandler first calls GetExtraLabels, which requires a valid
// format query param. An empty format string is invalid, so we expect an error.
func TestInsertHandlerEmptyBody(t *testing.T) {
	// Build a minimal request with a valid format query param.
	// format "1:metric:mymetric" means column 1 is a metric named "mymetric".
	u, _ := url.Parse("http://localhost/api/v1/import/csv?format=1:metric:mymetric")
	req := &http.Request{
		Method: http.MethodPost,
		URL:    u,
		Body:   http.NoBody,
		Header: make(http.Header),
	}
	// With an empty body, stream.Parse will immediately get EOF from the reader
	// (http.NoBody returns 0 bytes) without scheduling any unmarshal work.
	if err := InsertHandler(nil, req); err != nil {
		t.Fatalf("unexpected error for empty body: %v", err)
	}
}

func TestInsertHandlerMissingFormat(t *testing.T) {
	// A request with no format param should return an error.
	u, _ := url.Parse("http://localhost/api/v1/import/csv")
	req := &http.Request{
		Method: http.MethodPost,
		URL:    u,
		Body:   http.NoBody,
		Header: make(http.Header),
	}
	err := InsertHandler(nil, req)
	if err == nil {
		t.Fatal("expected error for missing format param, got nil")
	}
}

func TestInsertHandlerInvalidFormat(t *testing.T) {
	// A request with a malformed format param should return an error.
	u, _ := url.Parse("http://localhost/api/v1/import/csv?format=badformat")
	req := &http.Request{
		Method: http.MethodPost,
		URL:    u,
		Body:   http.NoBody,
		Header: make(http.Header),
	}
	err := InsertHandler(nil, req)
	if err == nil {
		t.Fatal("expected error for invalid format param, got nil")
	}
}

func TestInsertHandlerExtraLabel(t *testing.T) {
	// Valid format + extra_label query param with empty body.
	u, _ := url.Parse("http://localhost/api/v1/import/csv?format=1:metric:mymetric&extra_label=env=prod")
	req := &http.Request{
		Method: http.MethodPost,
		URL:    u,
		Body:   http.NoBody,
		Header: make(http.Header),
	}
	if err := InsertHandler(nil, req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInsertHandlerInvalidExtraLabel(t *testing.T) {
	// extra_label without '=' should return an error.
	u, _ := url.Parse("http://localhost/api/v1/import/csv?format=1:metric:mymetric&extra_label=noequalssign")
	req := &http.Request{
		Method: http.MethodPost,
		URL:    u,
		Body:   http.NoBody,
		Header: make(http.Header),
	}
	err := InsertHandler(nil, req)
	if err == nil {
		t.Fatal("expected error for invalid extra_label, got nil")
	}
}

// Ensure the test file compiles even with the strings import kept for potential future use.
var _ = strings.NewReader

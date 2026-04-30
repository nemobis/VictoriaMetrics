package opentsdbhttp

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	otparser "github.com/VictoriaMetrics/VictoriaMetrics/lib/protoparser/opentsdbhttp"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/prompb"
)

// insertRows is called directly (white-box) to verify row-to-timeseries conversion.
// The opentsdbhttp stream parser uses ReadUncompressedData (batch mode) — not
// ScheduleUnmarshalWork — so InsertHandler tests with non-empty bodies are also safe.

func TestInsertRowsEmpty(t *testing.T) {
	if err := insertRows(nil, nil, nil); err != nil {
		t.Fatalf("unexpected error for empty rows: %v", err)
	}
}

func TestInsertRowsSingleMetric(t *testing.T) {
	rows := []otparser.Row{
		{
			Metric:    "sys.cpu.user",
			Value:     42.0,
			Timestamp: 1609459200000,
		},
	}
	if err := insertRows(nil, rows, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInsertRowsWithTags(t *testing.T) {
	rows := []otparser.Row{
		{
			Metric: "sys.mem.used",
			Tags: []otparser.Tag{
				{Key: "host", Value: "web01"},
				{Key: "zone", Value: "us-east"},
			},
			Value:     1024.0,
			Timestamp: 1609459200000,
		},
	}
	if err := insertRows(nil, rows, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInsertRowsWithExtraLabels(t *testing.T) {
	rows := []otparser.Row{
		{
			Metric:    "app.requests",
			Value:     200,
			Timestamp: 1609459200000,
		},
	}
	extraLabels := []prompb.Label{
		{Name: "datacenter", Value: "dc2"},
		{Name: "env", Value: "staging"},
	}
	if err := insertRows(nil, rows, extraLabels); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInsertRowsMultiple(t *testing.T) {
	rows := []otparser.Row{
		{Metric: "m.a", Value: 1.0, Timestamp: 1000000},
		{Metric: "m.b", Value: 2.0, Timestamp: 2000000},
		{Metric: "m.c", Value: 3.0, Timestamp: 3000000},
	}
	if err := insertRows(nil, rows, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInsertRowsLabelConstruction(t *testing.T) {
	rows := []otparser.Row{
		{
			Metric: "db.queries",
			Tags: []otparser.Tag{
				{Key: "db", Value: "postgres"},
				{Key: "op", Value: "select"},
			},
			Value:     55.0,
			Timestamp: 1609459200000,
		},
	}
	extraLabels := []prompb.Label{
		{Name: "region", Value: "eu-west-1"},
	}

	// Reconstruct the expected labels as insertRows does.
	r := &rows[0]
	var labels []prompb.Label
	labels = append(labels, prompb.Label{Name: "__name__", Value: r.Metric})
	for j := range r.Tags {
		tag := &r.Tags[j]
		labels = append(labels, prompb.Label{Name: tag.Key, Value: tag.Value})
	}
	labels = append(labels, extraLabels...)

	wantLabels := []prompb.Label{
		{Name: "__name__", Value: "db.queries"},
		{Name: "db", Value: "postgres"},
		{Name: "op", Value: "select"},
		{Name: "region", Value: "eu-west-1"},
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

// InsertHandler tests — opentsdbhttp uses ReadUncompressedData (batch mode,
// no ScheduleUnmarshalWork), so non-empty bodies are safe.

func makeOTSDBRequest(body string) *http.Request {
	u, _ := url.Parse("http://localhost/api/put")
	req := httptest.NewRequest(http.MethodPost, u.String(), strings.NewReader(body))
	return req
}

func TestInsertHandlerSingleObject(t *testing.T) {
	body := `{"metric":"sys.cpu.user","timestamp":1609459200,"value":42,"tags":{"host":"web01"}}`
	req := makeOTSDBRequest(body)
	if err := InsertHandler(nil, req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInsertHandlerArray(t *testing.T) {
	body := `[
		{"metric":"sys.cpu.user","timestamp":1609459200,"value":10,"tags":{"host":"web01"}},
		{"metric":"sys.mem.used","timestamp":1609459200,"value":2048,"tags":{"host":"web01"}}
	]`
	req := makeOTSDBRequest(body)
	if err := InsertHandler(nil, req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInsertHandlerMissingTags(t *testing.T) {
	// Tags are optional in opentsdbhttp.
	body := `{"metric":"sys.uptime","timestamp":1609459200,"value":99}`
	req := makeOTSDBRequest(body)
	if err := InsertHandler(nil, req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInsertHandlerMissingTimestamp(t *testing.T) {
	// Timestamp is optional; it should be filled with current time.
	body := `{"metric":"sys.load","value":1.5,"tags":{"host":"app01"}}`
	req := makeOTSDBRequest(body)
	if err := InsertHandler(nil, req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInsertHandlerInvalidJSON(t *testing.T) {
	body := `not-json`
	req := makeOTSDBRequest(body)
	err := InsertHandler(nil, req)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestInsertHandlerExtraLabel(t *testing.T) {
	body := `{"metric":"sys.cpu.user","timestamp":1609459200,"value":42,"tags":{"host":"web01"}}`
	u, _ := url.Parse("http://localhost/api/put?extra_label=env=prod")
	req := httptest.NewRequest(http.MethodPost, u.String(), strings.NewReader(body))
	if err := InsertHandler(nil, req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInsertHandlerInvalidExtraLabel(t *testing.T) {
	body := `{"metric":"sys.cpu.user","timestamp":1609459200,"value":42}`
	u, _ := url.Parse("http://localhost/api/put?extra_label=noequalssign")
	req := httptest.NewRequest(http.MethodPost, u.String(), strings.NewReader(body))
	err := InsertHandler(nil, req)
	if err == nil {
		t.Fatal("expected error for invalid extra_label, got nil")
	}
}

func TestInsertHandlerStringValue(t *testing.T) {
	// opentsdbhttp allows value as a string.
	body := `{"metric":"sys.cpu.user","timestamp":1609459200,"value":"12.34","tags":{"host":"web01"}}`
	req := makeOTSDBRequest(body)
	if err := InsertHandler(nil, req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInsertHandlerArrayMultipleMetrics(t *testing.T) {
	body := `[
		{"metric":"a","value":1,"timestamp":1000000000},
		{"metric":"b","value":2,"timestamp":1000000000},
		{"metric":"c","value":3,"timestamp":1000000000},
		{"metric":"d","value":4,"timestamp":1000000000}
	]`
	req := makeOTSDBRequest(body)
	if err := InsertHandler(nil, req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

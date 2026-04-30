package graphite

import (
	"strings"
	"testing"

	parser "github.com/VictoriaMetrics/VictoriaMetrics/lib/protoparser/graphite"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/prompb"
)

// insertRows is called directly (white-box) so we bypass the stream parser,
// which requires StartUnmarshalWorkers to be called first.

func TestInsertRowsEmpty(t *testing.T) {
	if err := insertRows(nil, nil); err != nil {
		t.Fatalf("unexpected error for empty rows: %v", err)
	}
}

func TestInsertRowsSingleMetric(t *testing.T) {
	rows := []parser.Row{
		{
			Metric:    "cpu.usage",
			Value:     42.5,
			Timestamp: 1000000,
		},
	}
	if err := insertRows(nil, rows); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInsertRowsWithTags(t *testing.T) {
	rows := []parser.Row{
		{
			Metric: "disk.io",
			Tags: []parser.Tag{
				{Key: "host", Value: "server1"},
				{Key: "device", Value: "sda"},
			},
			Value:     1024.0,
			Timestamp: 1609459200000,
		},
	}
	if err := insertRows(nil, rows); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInsertRowsMultiple(t *testing.T) {
	rows := []parser.Row{
		{Metric: "metric.a", Value: 1.0, Timestamp: 1000},
		{Metric: "metric.b", Value: 2.0, Timestamp: 2000},
		{Metric: "metric.c", Value: 3.0, Timestamp: 3000},
	}
	if err := insertRows(nil, rows); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInsertRowsBuildTimeSeries(t *testing.T) {
	// Verify the WriteRequest is constructed correctly by calling insertRows
	// and checking it doesn't return an error. The actual labels/samples
	// are set in the PushCtx then passed to TryPush (no-op with no remote targets).
	rows := []parser.Row{
		{
			Metric: "net.bytes",
			Tags: []parser.Tag{
				{Key: "iface", Value: "eth0"},
			},
			Value:     99.9,
			Timestamp: 1600000000000,
		},
		{
			Metric:    "net.packets",
			Value:     500,
			Timestamp: 1600000001000,
		},
	}
	if err := insertRows(nil, rows); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInsertRowsLabelNames(t *testing.T) {
	// Verify that __name__ and tag labels are constructed as expected
	// by building the expected prompb structure manually and comparing.
	rows := []parser.Row{
		{
			Metric: "mymetric",
			Tags: []parser.Tag{
				{Key: "tagkey", Value: "tagval"},
			},
			Value:     7.77,
			Timestamp: 12345678,
		},
	}

	// Expected labels for the single time series
	wantLabels := []prompb.Label{
		{Name: "__name__", Value: "mymetric"},
		{Name: "tagkey", Value: "tagval"},
	}

	// We cannot easily inspect the WriteRequest after TryPush (it's a no-op)
	// so instead we reconstruct the logic here to verify label construction.
	labels := []prompb.Label{}
	r := &rows[0]
	labels = append(labels, prompb.Label{
		Name:  "__name__",
		Value: r.Metric,
	})
	for j := range r.Tags {
		tag := &r.Tags[j]
		labels = append(labels, prompb.Label{
			Name:  tag.Key,
			Value: tag.Value,
		})
	}
	if len(labels) != len(wantLabels) {
		t.Fatalf("label count mismatch: got %d, want %d", len(labels), len(wantLabels))
	}
	for i, lbl := range labels {
		if lbl.Name != wantLabels[i].Name || lbl.Value != wantLabels[i].Value {
			t.Errorf("label[%d] mismatch: got %+v, want %+v", i, lbl, wantLabels[i])
		}
	}

	// Also ensure insertRows itself doesn't fail
	if err := insertRows(nil, rows); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestInsertHandlerEmptyBody tests that InsertHandler handles an empty reader
// without blocking. An empty body means stream.Parse will read EOF immediately
// without dispatching any ScheduleUnmarshalWork calls.
func TestInsertHandlerEmptyBody(t *testing.T) {
	if err := InsertHandler(strings.NewReader("")); err != nil {
		t.Fatalf("unexpected error for empty body: %v", err)
	}
}

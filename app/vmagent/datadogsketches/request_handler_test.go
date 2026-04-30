package datadogsketches

import (
	"testing"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/auth"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/prompb"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/protoparser/datadogsketches"
)

// insertRows calls remotewrite.TryPush which returns true when no remote write URLs are
// configured (rwctxsGlobal is empty), so these tests exercise the label-building logic
// without requiring a real remote storage backend.

func TestInsertRowsEmpty(t *testing.T) {
	if err := insertRows(nil, nil, nil); err != nil {
		t.Fatalf("unexpected error on empty sketches: %v", err)
	}
}

func TestInsertRowsSingleSketchNoExtraLabels(t *testing.T) {
	sketch := &datadogsketches.Sketch{
		Metric: "test.metric",
		Host:   "myhost",
		Tags:   []string{"env:prod", "service:web"},
		Dogsketches: []*datadogsketches.Dogsketch{
			{
				Ts:  1000,
				Cnt: 3,
				Min: 1.0,
				Max: 5.0,
				Sum: 9.0,
				K:   []int32{1, 2, 3},
				N:   []uint32{1, 1, 1},
			},
		},
	}
	if err := insertRows(nil, []*datadogsketches.Sketch{sketch}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInsertRowsWithExtraLabels(t *testing.T) {
	sketch := &datadogsketches.Sketch{
		Metric: "requests.count",
		Host:   "host1",
		Tags:   []string{"dc:us-east"},
		Dogsketches: []*datadogsketches.Dogsketch{
			{
				Ts:  2000,
				Cnt: 2,
				Min: 0.5,
				Max: 1.5,
				Sum: 2.0,
				K:   []int32{10, 20},
				N:   []uint32{1, 1},
			},
		},
	}
	extraLabels := []prompb.Label{
		{Name: "region", Value: "us-east-1"},
		{Name: "cluster", Value: "prod"},
	}
	if err := insertRows(nil, []*datadogsketches.Sketch{sketch}, extraLabels); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInsertRowsWithAuthToken(t *testing.T) {
	sketch := &datadogsketches.Sketch{
		Metric: "latency",
		Host:   "backend",
		Tags:   []string{"version:1.0"},
		Dogsketches: []*datadogsketches.Dogsketch{
			{
				Ts:  3000,
				Cnt: 5,
				Min: 10.0,
				Max: 100.0,
				Sum: 300.0,
				K:   []int32{5, 10, 20, 30, 40},
				N:   []uint32{1, 1, 1, 1, 1},
			},
		},
	}
	at := &auth.Token{AccountID: 42, ProjectID: 7}
	if err := insertRows(at, []*datadogsketches.Sketch{sketch}, nil); err != nil {
		t.Fatalf("unexpected error with auth token: %v", err)
	}
}

func TestInsertRowsMultipleSketches(t *testing.T) {
	sketches := []*datadogsketches.Sketch{
		{
			Metric: "metric.a",
			Host:   "hostA",
			Tags:   []string{"k:v"},
			Dogsketches: []*datadogsketches.Dogsketch{
				{Ts: 100, Cnt: 1, Min: 1, Max: 1, Sum: 1, K: []int32{1}, N: []uint32{1}},
			},
		},
		{
			Metric: "metric.b",
			Host:   "hostB",
			Tags:   []string{"env:staging"},
			Dogsketches: []*datadogsketches.Dogsketch{
				{Ts: 200, Cnt: 2, Min: 2, Max: 4, Sum: 6, K: []int32{2, 3}, N: []uint32{1, 1}},
			},
		},
	}
	if err := insertRows(nil, sketches, nil); err != nil {
		t.Fatalf("unexpected error with multiple sketches: %v", err)
	}
}

func TestInsertRowsSketchWithEmptyDogsketches(t *testing.T) {
	// A sketch with no Dogsketches should still work (produces metrics with no points).
	sketch := &datadogsketches.Sketch{
		Metric:      "empty.sketch",
		Host:        "host",
		Tags:        nil,
		Dogsketches: nil,
	}
	if err := insertRows(nil, []*datadogsketches.Sketch{sketch}, nil); err != nil {
		t.Fatalf("unexpected error with empty Dogsketches: %v", err)
	}
}

func TestInsertRowsTagWithoutValue(t *testing.T) {
	// Tags without ":" separator should result in tag name with empty value.
	sketch := &datadogsketches.Sketch{
		Metric: "notag.metric",
		Host:   "h",
		Tags:   []string{"standalone"},
		Dogsketches: []*datadogsketches.Dogsketch{
			{Ts: 500, Cnt: 1, Min: 0, Max: 0, Sum: 0, K: []int32{0}, N: []uint32{1}},
		},
	}
	if err := insertRows(nil, []*datadogsketches.Sketch{sketch}, nil); err != nil {
		t.Fatalf("unexpected error with tag without value: %v", err)
	}
}

func TestInsertRowsZeroCntSketch(t *testing.T) {
	// Cnt=0 means the quantile function returns 0; should not panic.
	sketch := &datadogsketches.Sketch{
		Metric: "zero.cnt",
		Host:   "h",
		Dogsketches: []*datadogsketches.Dogsketch{
			{Ts: 1, Cnt: 0, Min: 0, Max: 0, Sum: 0},
		},
	}
	if err := insertRows(nil, []*datadogsketches.Sketch{sketch}, nil); err != nil {
		t.Fatalf("unexpected error with zero-count sketch: %v", err)
	}
}

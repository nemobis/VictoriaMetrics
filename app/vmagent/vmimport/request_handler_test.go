package vmimport

import (
	"testing"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/auth"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/prompb"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/protoparser/vmimport"
)

// insertRows calls remotewrite.TryPush which returns true when no remote write URLs
// are configured (rwctxsGlobal is empty).

func TestInsertRowsEmpty(t *testing.T) {
	if err := insertRows(nil, nil, nil); err != nil {
		t.Fatalf("unexpected error on empty rows: %v", err)
	}
}

func TestInsertRowsSingleRow(t *testing.T) {
	rows := []vmimport.Row{
		{
			Tags: []vmimport.Tag{
				{Key: []byte("__name__"), Value: []byte("cpu_usage")},
				{Key: []byte("host"), Value: []byte("localhost")},
			},
			Values:     []float64{1.0, 2.0, 3.0},
			Timestamps: []int64{1000, 2000, 3000},
		},
	}
	if err := insertRows(nil, rows, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInsertRowsWithExtraLabels(t *testing.T) {
	rows := []vmimport.Row{
		{
			Tags: []vmimport.Tag{
				{Key: []byte("__name__"), Value: []byte("mem_free")},
			},
			Values:     []float64{512.0},
			Timestamps: []int64{5000},
		},
	}
	extraLabels := []prompb.Label{
		{Name: "env", Value: "prod"},
		{Name: "datacenter", Value: "eu-west"},
	}
	if err := insertRows(nil, rows, extraLabels); err != nil {
		t.Fatalf("unexpected error with extra labels: %v", err)
	}
}

func TestInsertRowsWithAuthToken(t *testing.T) {
	rows := []vmimport.Row{
		{
			Tags: []vmimport.Tag{
				{Key: []byte("__name__"), Value: []byte("requests_total")},
				{Key: []byte("job"), Value: []byte("api")},
			},
			Values:     []float64{100.0},
			Timestamps: []int64{9000},
		},
	}
	at := &auth.Token{AccountID: 1, ProjectID: 2}
	if err := insertRows(at, rows, nil); err != nil {
		t.Fatalf("unexpected error with auth token: %v", err)
	}
}

func TestInsertRowsMultipleRows(t *testing.T) {
	rows := []vmimport.Row{
		{
			Tags: []vmimport.Tag{
				{Key: []byte("__name__"), Value: []byte("metric_a")},
			},
			Values:     []float64{1.0, 2.0},
			Timestamps: []int64{100, 200},
		},
		{
			Tags: []vmimport.Tag{
				{Key: []byte("__name__"), Value: []byte("metric_b")},
				{Key: []byte("region"), Value: []byte("us")},
			},
			Values:     []float64{3.0},
			Timestamps: []int64{300},
		},
	}
	if err := insertRows(nil, rows, nil); err != nil {
		t.Fatalf("unexpected error with multiple rows: %v", err)
	}
}

func TestInsertRowsEmptyTagsAndValues(t *testing.T) {
	// A row with a single value/timestamp but no tag keys.
	rows := []vmimport.Row{
		{
			Tags:       []vmimport.Tag{},
			Values:     []float64{42.0},
			Timestamps: []int64{7000},
		},
	}
	if err := insertRows(nil, rows, nil); err != nil {
		t.Fatalf("unexpected error with empty tags: %v", err)
	}
}

func TestInsertRowsMultipleTagsSingleSample(t *testing.T) {
	rows := []vmimport.Row{
		{
			Tags: []vmimport.Tag{
				{Key: []byte("__name__"), Value: []byte("disk_io")},
				{Key: []byte("device"), Value: []byte("sda")},
				{Key: []byte("host"), Value: []byte("server1")},
				{Key: []byte("datacenter"), Value: []byte("dc1")},
			},
			Values:     []float64{99.9},
			Timestamps: []int64{8000},
		},
	}
	extraLabels := []prompb.Label{
		{Name: "cluster", Value: "k8s"},
	}
	if err := insertRows(nil, rows, extraLabels); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

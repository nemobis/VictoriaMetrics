package promremotewrite

import (
	"testing"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/auth"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/prompb"
)

// insertRows calls remotewrite.TryPush which returns true when no remote write URLs
// are configured (rwctxsGlobal is empty).

func TestInsertRowsEmpty(t *testing.T) {
	if err := insertRows(nil, nil, nil, nil); err != nil {
		t.Fatalf("unexpected error on empty input: %v", err)
	}
}

func TestInsertRowsSingleTimeSeries(t *testing.T) {
	tss := []prompb.TimeSeries{
		{
			Labels: []prompb.Label{
				{Name: "__name__", Value: "up"},
				{Name: "job", Value: "node"},
				{Name: "instance", Value: "localhost:9100"},
			},
			Samples: []prompb.Sample{
				{Value: 1.0, Timestamp: 1000},
				{Value: 1.0, Timestamp: 2000},
			},
		},
	}
	if err := insertRows(nil, tss, nil, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInsertRowsWithExtraLabels(t *testing.T) {
	tss := []prompb.TimeSeries{
		{
			Labels: []prompb.Label{
				{Name: "__name__", Value: "http_requests_total"},
				{Name: "method", Value: "POST"},
			},
			Samples: []prompb.Sample{
				{Value: 42.0, Timestamp: 5000},
			},
		},
	}
	extraLabels := []prompb.Label{
		{Name: "env", Value: "prod"},
		{Name: "region", Value: "us-west-2"},
	}
	if err := insertRows(nil, tss, nil, extraLabels); err != nil {
		t.Fatalf("unexpected error with extra labels: %v", err)
	}
}

func TestInsertRowsWithAuthToken(t *testing.T) {
	tss := []prompb.TimeSeries{
		{
			Labels: []prompb.Label{
				{Name: "__name__", Value: "memory_usage_bytes"},
			},
			Samples: []prompb.Sample{
				{Value: 1024.0 * 1024.0, Timestamp: 9000},
			},
		},
	}
	at := &auth.Token{AccountID: 100, ProjectID: 200}
	if err := insertRows(at, tss, nil, nil); err != nil {
		t.Fatalf("unexpected error with auth token: %v", err)
	}
}

func TestInsertRowsMultipleTimeSeries(t *testing.T) {
	tss := []prompb.TimeSeries{
		{
			Labels: []prompb.Label{
				{Name: "__name__", Value: "cpu_usage"},
				{Name: "cpu", Value: "0"},
			},
			Samples: []prompb.Sample{
				{Value: 0.5, Timestamp: 1000},
				{Value: 0.6, Timestamp: 2000},
			},
		},
		{
			Labels: []prompb.Label{
				{Name: "__name__", Value: "cpu_usage"},
				{Name: "cpu", Value: "1"},
			},
			Samples: []prompb.Sample{
				{Value: 0.3, Timestamp: 1000},
				{Value: 0.4, Timestamp: 2000},
			},
		},
	}
	if err := insertRows(nil, tss, nil, nil); err != nil {
		t.Fatalf("unexpected error with multiple time series: %v", err)
	}
}

func TestInsertRowsWithMetadata(t *testing.T) {
	tss := []prompb.TimeSeries{
		{
			Labels: []prompb.Label{
				{Name: "__name__", Value: "go_goroutines"},
			},
			Samples: []prompb.Sample{
				{Value: 42.0, Timestamp: 3000},
			},
		},
	}
	mms := []prompb.MetricMetadata{
		{
			MetricFamilyName: "go_goroutines",
			Help:             "Number of goroutines.",
			Type:             prompb.MetricTypeGauge,
		},
	}
	if err := insertRows(nil, tss, mms, nil); err != nil {
		t.Fatalf("unexpected error with metadata: %v", err)
	}
}

func TestInsertRowsEmptyTimeSeriesLabels(t *testing.T) {
	// A time series with no labels is unusual but should not panic.
	tss := []prompb.TimeSeries{
		{
			Labels:  []prompb.Label{},
			Samples: []prompb.Sample{{Value: 1.0, Timestamp: 100}},
		},
	}
	if err := insertRows(nil, tss, nil, nil); err != nil {
		t.Fatalf("unexpected error with empty labels: %v", err)
	}
}

func TestInsertRowsNoSamples(t *testing.T) {
	// A time series with labels but no samples.
	tss := []prompb.TimeSeries{
		{
			Labels: []prompb.Label{
				{Name: "__name__", Value: "some_metric"},
			},
			Samples: nil,
		},
	}
	if err := insertRows(nil, tss, nil, nil); err != nil {
		t.Fatalf("unexpected error with no samples: %v", err)
	}
}

func TestInsertRowsWithAuthTokenAndExtraLabels(t *testing.T) {
	tss := []prompb.TimeSeries{
		{
			Labels: []prompb.Label{
				{Name: "__name__", Value: "network_rx_bytes"},
				{Name: "interface", Value: "eth0"},
			},
			Samples: []prompb.Sample{
				{Value: 1024.0, Timestamp: 4000},
				{Value: 2048.0, Timestamp: 5000},
				{Value: 4096.0, Timestamp: 6000},
			},
		},
	}
	extraLabels := []prompb.Label{
		{Name: "cluster", Value: "prod"},
	}
	at := &auth.Token{AccountID: 7, ProjectID: 3}
	if err := insertRows(at, tss, nil, extraLabels); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInsertRowsOnlyMetadata(t *testing.T) {
	// No time series, only metadata.
	mms := []prompb.MetricMetadata{
		{
			MetricFamilyName: "process_cpu_seconds_total",
			Help:             "Total user and system CPU time spent in seconds.",
			Type:             prompb.MetricTypeCounter,
			Unit:             "seconds",
		},
	}
	if err := insertRows(nil, nil, mms, nil); err != nil {
		t.Fatalf("unexpected error with only metadata: %v", err)
	}
}

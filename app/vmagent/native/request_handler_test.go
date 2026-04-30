package native

import (
	"testing"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/auth"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/prompb"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/protoparser/native/stream"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/storage"
)

// insertRows calls remotewrite.TryPush which returns true when no remote write URLs
// are configured (rwctxsGlobal is empty).

func TestInsertRowsEmptyBlock(t *testing.T) {
	block := &stream.Block{}
	if err := insertRows(nil, block, nil); err != nil {
		t.Fatalf("unexpected error on empty block: %v", err)
	}
}

func TestInsertRowsSingleSample(t *testing.T) {
	block := &stream.Block{}
	block.MetricName.MetricGroup = []byte("cpu_seconds_total")
	block.Values = []float64{1.5}
	block.Timestamps = []int64{1000}
	if err := insertRows(nil, block, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInsertRowsWithTags(t *testing.T) {
	block := &stream.Block{}
	block.MetricName.MetricGroup = []byte("http_requests_total")
	block.MetricName.Tags = []storage.Tag{
		{Key: []byte("method"), Value: []byte("GET")},
		{Key: []byte("status"), Value: []byte("200")},
	}
	block.Values = []float64{100.0, 200.0}
	block.Timestamps = []int64{1000, 2000}
	if err := insertRows(nil, block, nil); err != nil {
		t.Fatalf("unexpected error with tags: %v", err)
	}
}

func TestInsertRowsWithExtraLabels(t *testing.T) {
	block := &stream.Block{}
	block.MetricName.MetricGroup = []byte("disk_reads_total")
	block.Values = []float64{50.0}
	block.Timestamps = []int64{5000}
	extraLabels := []prompb.Label{
		{Name: "env", Value: "staging"},
		{Name: "region", Value: "eu"},
	}
	if err := insertRows(nil, block, extraLabels); err != nil {
		t.Fatalf("unexpected error with extra labels: %v", err)
	}
}

func TestInsertRowsWithAuthToken(t *testing.T) {
	block := &stream.Block{}
	block.MetricName.MetricGroup = []byte("mem_free_bytes")
	block.Values = []float64{1024.0 * 1024.0}
	block.Timestamps = []int64{9000}
	at := &auth.Token{AccountID: 10, ProjectID: 20}
	if err := insertRows(at, block, nil); err != nil {
		t.Fatalf("unexpected error with auth token: %v", err)
	}
}

func TestInsertRowsManySamples(t *testing.T) {
	const n = 1000
	values := make([]float64, n)
	timestamps := make([]int64, n)
	for i := 0; i < n; i++ {
		values[i] = float64(i)
		timestamps[i] = int64(i * 1000)
	}
	block := &stream.Block{}
	block.MetricName.MetricGroup = []byte("big_metric")
	block.MetricName.Tags = []storage.Tag{
		{Key: []byte("host"), Value: []byte("server99")},
	}
	block.Values = values
	block.Timestamps = timestamps
	if err := insertRows(nil, block, nil); err != nil {
		t.Fatalf("unexpected error with many samples: %v", err)
	}
}

func TestInsertRowsWithTagsAndExtraLabels(t *testing.T) {
	block := &stream.Block{}
	block.MetricName.MetricGroup = []byte("network_bytes")
	block.MetricName.Tags = []storage.Tag{
		{Key: []byte("interface"), Value: []byte("eth0")},
	}
	block.Values = []float64{1.0, 2.0, 3.0}
	block.Timestamps = []int64{100, 200, 300}
	extraLabels := []prompb.Label{
		{Name: "cluster", Value: "prod-cluster"},
	}
	at := &auth.Token{AccountID: 5, ProjectID: 0}
	if err := insertRows(at, block, extraLabels); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

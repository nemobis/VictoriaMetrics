package stream

import (
	"bytes"
	"testing"

	"github.com/golang/snappy"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/encoding"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/prompb"
)

// makeSnappyWriteRequest builds a minimal WriteRequest and returns its snappy-encoded
// protobuf wire bytes (the format expected by non-VMRemoteWrite Parse calls).
func makeSnappyWriteRequest(tss []prompb.TimeSeries) []byte {
	wr := &prompb.WriteRequest{Timeseries: tss}
	raw := wr.MarshalProtobuf(nil)
	return snappy.Encode(nil, raw)
}

// makeZSTDWriteRequest builds a WriteRequest and returns its ZSTD-encoded
// protobuf wire bytes (the format used by VMRemoteWrite).
func makeZSTDWriteRequest(tss []prompb.TimeSeries) []byte {
	wr := &prompb.WriteRequest{Timeseries: tss}
	raw := wr.MarshalProtobuf(nil)
	return encoding.CompressZSTDLevel(nil, raw, 1)
}

// copyTimeSeries makes an independent deep copy of tss.
//
// Parse's WriteRequestUnmarshaler uses pooled backing slices (labelsPool,
// samplesPool) that are zeroed via clear() when the unmarshaler is returned to
// the pool — i.e. immediately after the callback returns.  A shallow copy of the
// slice header would therefore point at zeroed memory.  This helper allocates
// independent Label/Sample slices so the test can inspect them after Parse returns.
func copyTimeSeries(tss []prompb.TimeSeries) []prompb.TimeSeries {
	out := make([]prompb.TimeSeries, len(tss))
	for i, ts := range tss {
		labels := make([]prompb.Label, len(ts.Labels))
		copy(labels, ts.Labels)
		samples := make([]prompb.Sample, len(ts.Samples))
		copy(samples, ts.Samples)
		out[i] = prompb.TimeSeries{Labels: labels, Samples: samples}
	}
	return out
}

// TestParseSnappyRoundtrip verifies that Parse correctly decodes a
// snappy-compressed Prometheus remote_write payload and delivers the
// time series to the callback.
func TestParseSnappyRoundtrip(t *testing.T) {
	ts := prompb.TimeSeries{
		Labels: []prompb.Label{
			{Name: "__name__", Value: "http_requests_total"},
			{Name: "job", Value: "test"},
		},
		Samples: []prompb.Sample{
			{Value: 42, Timestamp: 1000},
		},
	}
	data := makeSnappyWriteRequest([]prompb.TimeSeries{ts})

	var gotSeries []prompb.TimeSeries
	err := Parse(bytes.NewReader(data), false, func(tss []prompb.TimeSeries, _ []prompb.MetricMetadata) error {
		gotSeries = append(gotSeries, copyTimeSeries(tss)...)
		return nil
	})
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}
	if len(gotSeries) != 1 {
		t.Fatalf("expected 1 time series, got %d", len(gotSeries))
	}
	got := gotSeries[0]
	if len(got.Labels) != len(ts.Labels) {
		t.Fatalf("label count: got %d, want %d", len(got.Labels), len(ts.Labels))
	}
	for i, wl := range ts.Labels {
		if got.Labels[i].Name != wl.Name || got.Labels[i].Value != wl.Value {
			t.Errorf("label[%d]: got {%q,%q}, want {%q,%q}",
				i, got.Labels[i].Name, got.Labels[i].Value, wl.Name, wl.Value)
		}
	}
	if len(got.Samples) != 1 {
		t.Fatalf("sample count: got %d, want %d", len(got.Samples), 1)
	}
	if got.Samples[0].Value != ts.Samples[0].Value {
		t.Errorf("sample value: got %v, want %v", got.Samples[0].Value, ts.Samples[0].Value)
	}
}

// TestParseVMRemoteWriteZSTDRoundtrip verifies that Parse correctly decodes a
// ZSTD-compressed VMRemoteWrite payload.
func TestParseVMRemoteWriteZSTDRoundtrip(t *testing.T) {
	ts := prompb.TimeSeries{
		Labels: []prompb.Label{
			{Name: "__name__", Value: "cpu_seconds_total"},
			{Name: "mode", Value: "idle"},
		},
		Samples: []prompb.Sample{
			{Value: 99.9, Timestamp: 2000},
		},
	}
	data := makeZSTDWriteRequest([]prompb.TimeSeries{ts})

	var gotSeries []prompb.TimeSeries
	err := Parse(bytes.NewReader(data), true, func(tss []prompb.TimeSeries, _ []prompb.MetricMetadata) error {
		gotSeries = append(gotSeries, copyTimeSeries(tss)...)
		return nil
	})
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}
	if len(gotSeries) != 1 {
		t.Fatalf("expected 1 time series, got %d", len(gotSeries))
	}
	if gotSeries[0].Samples[0].Value != 99.9 {
		t.Errorf("sample value: got %v, want %v", gotSeries[0].Samples[0].Value, 99.9)
	}
}

// TestParseVMRemoteWriteSnappyFallback verifies the backwards-compatibility
// fallback: a VMRemoteWrite request that is snappy-encoded (not zstd) is still
// accepted.  This exercises the zstd-fail→snappy-retry path added in issue #5301.
func TestParseVMRemoteWriteSnappyFallback(t *testing.T) {
	ts := prompb.TimeSeries{
		Labels: []prompb.Label{
			{Name: "__name__", Value: "fallback_metric"},
		},
		Samples: []prompb.Sample{
			{Value: 7, Timestamp: 3000},
		},
	}
	// Encode with snappy instead of zstd — this simulates old vmagent behaviour
	// where snappy was used even for VMRemoteWrite before the fix.
	data := makeSnappyWriteRequest([]prompb.TimeSeries{ts})

	var gotSeries []prompb.TimeSeries
	err := Parse(bytes.NewReader(data), true, func(tss []prompb.TimeSeries, _ []prompb.MetricMetadata) error {
		gotSeries = append(gotSeries, copyTimeSeries(tss)...)
		return nil
	})
	if err != nil {
		t.Fatalf("Parse returned error on snappy fallback: %v", err)
	}
	if len(gotSeries) != 1 {
		t.Fatalf("expected 1 time series, got %d", len(gotSeries))
	}
	if gotSeries[0].Samples[0].Value != 7 {
		t.Errorf("sample value: got %v, want %v", gotSeries[0].Samples[0].Value, 7)
	}
}

// TestParseNonVMRemoteWriteZSTDFallback verifies that a non-VMRemoteWrite
// request encoded with ZSTD (not snappy) is still accepted via the fallback
// path added in issue #5301.
func TestParseNonVMRemoteWriteZSTDFallback(t *testing.T) {
	ts := prompb.TimeSeries{
		Labels: []prompb.Label{
			{Name: "__name__", Value: "zstd_fallback_metric"},
		},
		Samples: []prompb.Sample{
			{Value: 3, Timestamp: 4000},
		},
	}
	data := makeZSTDWriteRequest([]prompb.TimeSeries{ts})

	var gotSeries []prompb.TimeSeries
	err := Parse(bytes.NewReader(data), false, func(tss []prompb.TimeSeries, _ []prompb.MetricMetadata) error {
		gotSeries = append(gotSeries, copyTimeSeries(tss)...)
		return nil
	})
	if err != nil {
		t.Fatalf("Parse returned error on zstd fallback: %v", err)
	}
	if len(gotSeries) != 1 {
		t.Fatalf("expected 1 time series, got %d", len(gotSeries))
	}
}

// TestParseMultipleTimeSeries verifies that multiple time series within a
// single request are all delivered to the callback.
func TestParseMultipleTimeSeries(t *testing.T) {
	const n = 10
	tss := make([]prompb.TimeSeries, n)
	for i := range tss {
		tss[i] = prompb.TimeSeries{
			Labels:  []prompb.Label{{Name: "__name__", Value: "m"}},
			Samples: []prompb.Sample{{Value: float64(i), Timestamp: int64(i * 1000)}},
		}
	}
	data := makeSnappyWriteRequest(tss)

	var total int
	err := Parse(bytes.NewReader(data), false, func(tss []prompb.TimeSeries, _ []prompb.MetricMetadata) error {
		total += len(tss)
		return nil
	})
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if total != n {
		t.Fatalf("expected %d time series, got %d", n, total)
	}
}

// TestParseEmptyRequest verifies that a valid empty WriteRequest (no time
// series) is handled without error.
func TestParseEmptyRequest(t *testing.T) {
	data := makeSnappyWriteRequest(nil)
	called := false
	err := Parse(bytes.NewReader(data), false, func(tss []prompb.TimeSeries, _ []prompb.MetricMetadata) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("Parse error on empty request: %v", err)
	}
	if !called {
		t.Fatal("callback was not called for empty request")
	}
}

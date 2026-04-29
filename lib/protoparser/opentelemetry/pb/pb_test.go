package pb

import (
	"strings"
	"testing"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/prompb"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/promutil"
)

// testMetricPusher captures all calls to PushSample and PushMetricMetadata.
type testMetricPusher struct {
	samples  []testSample
	metadata []MetricMetadata
}

type testSample struct {
	mm        MetricMetadata
	suffix    string
	labels    []prompb.Label
	timestamp uint64
	value     float64
	flags     uint32
}

func (p *testMetricPusher) PushSample(mm *MetricMetadata, suffix string, ls *promutil.Labels, timestampNsecs uint64, value float64, flags uint32) {
	labelsCopy := make([]prompb.Label, len(ls.Labels))
	copy(labelsCopy, ls.Labels)
	p.samples = append(p.samples, testSample{
		mm:        *mm,
		suffix:    suffix,
		labels:    labelsCopy,
		timestamp: timestampNsecs,
		value:     value,
		flags:     flags,
	})
}

func (p *testMetricPusher) PushMetricMetadata(mm *MetricMetadata) {
	p.metadata = append(p.metadata, *mm)
}

// TestDecodeMetricsDataUnitNotBleeding is a regression test for commit 89c0b1c1a (issue #10889).
//
// Before commit 89c0b1c1a (lib/opentelemetry: properly reset metric metadata, issue #10889),
// dctx.mm.reset() was not called before parsing each new metric, causing the Unit field of a
// preceding metric to persist into the next metric with no Unit.
//
// Concretely: marshalProtobuf used to always write the Unit field (even when empty), so the
// decoder always read it and overwrote dctx.mm.Unit. The bug was on the marshal side — an empty
// Unit was still serialised, meaning a preceding metric's Unit would leak into the next metric
// when the decoder didn't reset the metadata struct.
//
// After the fix, marshalProtobuf skips writing Unit when it is empty (omitempty-style), and
// decodeMetric calls dctx.mm.reset() before parsing each metric, ensuring that a missing Unit
// field in the wire encoding results in an empty Unit on the decoded side.
func TestDecodeMetricsDataUnitNotBleeding(t *testing.T) {
	doubleVal := 1.0
	md := &MetricsData{
		ResourceMetrics: []*ResourceMetrics{
			{
				ScopeMetrics: []*ScopeMetrics{
					{
						Metrics: []*Metric{
							{
								Name: "http_requests",
								Unit: "seconds",
								Gauge: &Gauge{
									DataPoints: []*NumberDataPoint{
										{
											TimeUnixNano: 1000,
											DoubleValue:  &doubleVal,
										},
									},
								},
							},
							{
								Name: "cpu_usage",
								Unit: "", // no unit — must NOT inherit "seconds" from the previous metric
								Gauge: &Gauge{
									DataPoints: []*NumberDataPoint{
										{
											TimeUnixNano: 2000,
											DoubleValue:  &doubleVal,
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	encoded := md.MarshalProtobuf(nil)

	pusher := &testMetricPusher{}
	if err := DecodeMetricsData(encoded, pusher); err != nil {
		t.Fatalf("DecodeMetricsData returned unexpected error: %v", err)
	}

	// Expect exactly two metadata entries: one per metric.
	if len(pusher.metadata) != 2 {
		t.Fatalf("expected 2 metadata entries, got %d", len(pusher.metadata))
	}

	// Verify the first metric has Unit "seconds".
	if pusher.metadata[0].Name != "http_requests" {
		t.Errorf("expected first metadata name to be %q, got %q", "http_requests", pusher.metadata[0].Name)
	}
	if pusher.metadata[0].Unit != "seconds" {
		t.Errorf("expected first metadata unit to be %q, got %q", "seconds", pusher.metadata[0].Unit)
	}

	// Verify that the second metric has an empty Unit — NOT the "seconds" from the first metric.
	if pusher.metadata[1].Name != "cpu_usage" {
		t.Errorf("expected second metadata name to be %q, got %q", "cpu_usage", pusher.metadata[1].Name)
	}
	if pusher.metadata[1].Unit != "" {
		t.Errorf("unit bleed detected: expected second metadata unit to be empty, got %q (regression of issue #10889)", pusher.metadata[1].Unit)
	}
}

// TestDecodeMetricsDataExponentialHistogramNegativeBuckets is a regression test for
// commit 2d6cf8827 (PR #10669, issue #9896).
//
// Before commit 2d6cf8827, negative buckets of an ExponentialHistogram data point were never
// decoded: the ExponentialHistogramDataPoint struct had no Negative field, marshalProtobuf did
// not serialise field 9, and decodeExponentialHistogramDataPoint had no case 9 handler.
// As a result, observations that fell in negative ranges were silently dropped.
//
// After the fix, both Positive (field 8) and Negative (field 9) buckets are encoded and decoded,
// and pushSamples emits _bucket samples with vmrange labels that cover the negative range
// (i.e. the label value has the form "-X.XXXe+YY...-Z.ZZZe+WW").
func TestDecodeMetricsDataExponentialHistogramNegativeBuckets(t *testing.T) {
	sum := 0.5
	posCount := uint64(3)
	negCount := uint64(7)

	md := &MetricsData{
		ResourceMetrics: []*ResourceMetrics{
			{
				ScopeMetrics: []*ScopeMetrics{
					{
						Metrics: []*Metric{
							{
								Name: "latency",
								ExponentialHistogram: &ExponentialHistogram{
									DataPoints: []*ExponentialHistogramDataPoint{
										{
											TimeUnixNano: 1000,
											Count:        posCount + negCount,
											Sum:          &sum,
											Scale:        0,
											Positive: &Buckets{
												Offset:       0,
												BucketCounts: []uint64{posCount},
											},
											Negative: &Buckets{
												Offset:       0,
												BucketCounts: []uint64{negCount},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	encoded := md.MarshalProtobuf(nil)

	pusher := &testMetricPusher{}
	if err := DecodeMetricsData(encoded, pusher); err != nil {
		t.Fatalf("DecodeMetricsData returned unexpected error: %v", err)
	}

	// Collect vmrange label values from _bucket samples.
	var vmranges []string
	for _, s := range pusher.samples {
		if s.suffix != "_bucket" {
			continue
		}
		for _, l := range s.labels {
			if l.Name == "vmrange" {
				vmranges = append(vmranges, l.Value)
			}
		}
	}

	if len(vmranges) == 0 {
		t.Fatal("no _bucket samples with vmrange label were emitted")
	}

	// There must be at least one vmrange covering a negative range.
	// Negative ranges are formatted as "-<upper>...-<lower>", so both parts of the
	// "..." separator start with '-'. Check that the first character is '-' and that
	// there is a second '-' after the "..." separator.
	foundNegative := false
	for _, vr := range vmranges {
		// A negative range has the pattern "-<upper>...-<lower>", so both parts of the
		// "..." separator start with '-'.
		if strings.HasPrefix(vr, "-") && strings.Contains(vr, "...-") {
			foundNegative = true
			break
		}
	}
	if !foundNegative {
		t.Errorf("no negative vmrange bucket found in emitted samples (regression of PR #10669); vmranges observed: %v", vmranges)
	}

	// Also verify that positive buckets are still emitted.
	foundPositive := false
	for _, vr := range vmranges {
		if !strings.HasPrefix(vr, "-") {
			foundPositive = true
			break
		}
	}
	if !foundPositive {
		t.Errorf("no positive vmrange bucket found in emitted samples; vmranges observed: %v", vmranges)
	}

	// Verify the count for the negative bucket matches what was encoded.
	for _, s := range pusher.samples {
		if s.suffix != "_bucket" {
			continue
		}
		for _, l := range s.labels {
			if l.Name == "vmrange" && strings.HasPrefix(l.Value, "-") && strings.Contains(l.Value, "...-") {
				if uint64(s.value) != negCount {
					t.Errorf("expected negative bucket count %d, got %v", negCount, s.value)
				}
			}
		}
	}
}

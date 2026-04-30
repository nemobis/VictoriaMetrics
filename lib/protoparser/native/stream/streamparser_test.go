package stream

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/encoding"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/protoparser/protoparserutil"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/storage"
)

// buildNativePayload serialises a time range followed by one or more (metricName, block) pairs
// into the wire format expected by Parse.
func buildNativePayload(minTS, maxTS int64, entries []nativeEntry) []byte {
	var buf []byte

	// 16-byte time range header
	buf = encoding.MarshalInt64(buf, minTS)
	buf = encoding.MarshalInt64(buf, maxTS)

	for _, e := range entries {
		// marshal metric name
		mnBuf := e.mn.Marshal(nil)
		buf = encoding.MarshalUint32(buf, uint32(len(mnBuf)))
		buf = append(buf, mnBuf...)

		// marshal block
		var blk storage.Block
		blk.Init(&storage.TSID{}, e.timestamps, e.values, 0, 64)
		blockBuf := blk.MarshalPortable(nil)
		buf = encoding.MarshalUint32(buf, uint32(len(blockBuf)))
		buf = append(buf, blockBuf...)
	}

	return buf
}

type nativeEntry struct {
	mn         storage.MetricName
	timestamps []int64
	values     []int64
}

func makeMetricName(name string) storage.MetricName {
	var mn storage.MetricName
	mn.MetricGroup = []byte(name)
	return mn
}

func TestNativeParse_EmptyPayload(t *testing.T) {
	protoparserutil.StartUnmarshalWorkers()
	defer protoparserutil.StopUnmarshalWorkers()

	// Empty body: not even the 16-byte header — Parse should return an error.
	err := Parse(bytes.NewReader(nil), "", func(b *Block) error { return nil })
	if err == nil {
		t.Fatal("expected error on empty payload, got nil")
	}
}

func TestNativeParse_HeaderOnlyNoBlocks(t *testing.T) {
	protoparserutil.StartUnmarshalWorkers()
	defer protoparserutil.StopUnmarshalWorkers()

	// Valid 16-byte header, no blocks — should succeed with zero callbacks.
	payload := buildNativePayload(1000, 2000, nil)
	var called int
	err := Parse(bytes.NewReader(payload), "", func(b *Block) error {
		called++
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called != 0 {
		t.Fatalf("expected 0 callbacks, got %d", called)
	}
}

func TestNativeParse_SingleBlock(t *testing.T) {
	protoparserutil.StartUnmarshalWorkers()
	defer protoparserutil.StopUnmarshalWorkers()

	mn := makeMetricName("cpu_usage")
	timestamps := []int64{1000, 2000, 3000}
	values := []int64{10, 20, 30}

	payload := buildNativePayload(500, 5000, []nativeEntry{{mn: mn, timestamps: timestamps, values: values}})

	var mu sync.Mutex
	var blocks []blockSnapshot
	err := Parse(bytes.NewReader(payload), "", func(b *Block) error {
		mu.Lock()
		blocks = append(blocks, snapshotBlock(b))
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if string(blocks[0].MetricGroup) != "cpu_usage" {
		t.Fatalf("unexpected metric group: %s", blocks[0].MetricGroup)
	}
	if len(blocks[0].Timestamps) != 3 {
		t.Fatalf("expected 3 timestamps, got %d", len(blocks[0].Timestamps))
	}
	if len(blocks[0].Values) != 3 {
		t.Fatalf("expected 3 values, got %d", len(blocks[0].Values))
	}
}

func TestNativeParse_MultipleBlocks(t *testing.T) {
	protoparserutil.StartUnmarshalWorkers()
	defer protoparserutil.StopUnmarshalWorkers()

	entries := []nativeEntry{
		{mn: makeMetricName("metric_a"), timestamps: []int64{1000}, values: []int64{1}},
		{mn: makeMetricName("metric_b"), timestamps: []int64{2000}, values: []int64{2}},
		{mn: makeMetricName("metric_c"), timestamps: []int64{3000}, values: []int64{3}},
	}
	payload := buildNativePayload(500, 5000, entries)

	var mu sync.Mutex
	var count int
	err := Parse(bytes.NewReader(payload), "", func(b *Block) error {
		mu.Lock()
		count++
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected 3 blocks, got %d", count)
	}
}

func TestNativeParse_TimeRangeFilter(t *testing.T) {
	protoparserutil.StartUnmarshalWorkers()
	defer protoparserutil.StopUnmarshalWorkers()

	// Block has timestamps 1000, 2000, 3000, 4000, 5000 ms.
	// Time-range filter in the header covers only 2000–4000.
	mn := makeMetricName("filtered_metric")
	timestamps := []int64{1000, 2000, 3000, 4000, 5000}
	values := []int64{1, 2, 3, 4, 5}

	payload := buildNativePayload(2000, 4000, []nativeEntry{{mn: mn, timestamps: timestamps, values: values}})

	var mu sync.Mutex
	var blocks []blockSnapshot
	err := Parse(bytes.NewReader(payload), "", func(b *Block) error {
		mu.Lock()
		blocks = append(blocks, snapshotBlock(b))
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	// Only the rows within [2000, 4000] should survive the filter.
	for _, ts := range blocks[0].Timestamps {
		if ts < 2000 || ts > 4000 {
			t.Fatalf("timestamp %d is outside the time range [2000, 4000]", ts)
		}
	}
}

func TestNativeParse_TruncatedMetricNameSize(t *testing.T) {
	protoparserutil.StartUnmarshalWorkers()
	defer protoparserutil.StopUnmarshalWorkers()

	// Valid 16-byte header, then only 2 bytes of the 4-byte metricName size field.
	var buf []byte
	buf = encoding.MarshalInt64(buf, 1000)
	buf = encoding.MarshalInt64(buf, 2000)
	buf = append(buf, 0x00, 0x01) // incomplete uint32

	err := Parse(bytes.NewReader(buf), "", func(b *Block) error { return nil })
	if err == nil {
		t.Fatal("expected error on truncated metricName size, got nil")
	}
}

func TestNativeParse_TruncatedMetricNameBody(t *testing.T) {
	protoparserutil.StartUnmarshalWorkers()
	defer protoparserutil.StopUnmarshalWorkers()

	// Valid header + metricName size=10 but only 3 bytes of body follow.
	var buf []byte
	buf = encoding.MarshalInt64(buf, 1000)
	buf = encoding.MarshalInt64(buf, 2000)
	buf = encoding.MarshalUint32(buf, 10) // claims 10 bytes
	buf = append(buf, 0x01, 0x02, 0x03)  // only 3 bytes

	err := Parse(bytes.NewReader(buf), "", func(b *Block) error { return nil })
	if err == nil {
		t.Fatal("expected error on truncated metricName body, got nil")
	}
}

func TestNativeParse_MetricNameTooLarge(t *testing.T) {
	protoparserutil.StartUnmarshalWorkers()
	defer protoparserutil.StopUnmarshalWorkers()

	var buf []byte
	buf = encoding.MarshalInt64(buf, 1000)
	buf = encoding.MarshalInt64(buf, 2000)
	buf = encoding.MarshalUint32(buf, 2*1024*1024) // exceeds 1 MB limit

	err := Parse(bytes.NewReader(buf), "", func(b *Block) error { return nil })
	if err == nil {
		t.Fatal("expected error for too-large metricName size, got nil")
	}
	if !strings.Contains(err.Error(), "too big metricName size") {
		t.Fatalf("unexpected error text: %v", err)
	}
}

func TestNativeParse_BlockTooLarge(t *testing.T) {
	protoparserutil.StartUnmarshalWorkers()
	defer protoparserutil.StopUnmarshalWorkers()

	mn := makeMetricName("x")
	mnBuf := mn.Marshal(nil)

	var buf []byte
	buf = encoding.MarshalInt64(buf, 1000)
	buf = encoding.MarshalInt64(buf, 2000)
	buf = encoding.MarshalUint32(buf, uint32(len(mnBuf)))
	buf = append(buf, mnBuf...)
	buf = encoding.MarshalUint32(buf, 2*1024*1024) // exceeds 1 MB limit

	err := Parse(bytes.NewReader(buf), "", func(b *Block) error { return nil })
	if err == nil {
		t.Fatal("expected error for too-large block size, got nil")
	}
	if !strings.Contains(err.Error(), "too big native block size") {
		t.Fatalf("unexpected error text: %v", err)
	}
}

func TestNativeParse_CallbackError(t *testing.T) {
	protoparserutil.StartUnmarshalWorkers()
	defer protoparserutil.StopUnmarshalWorkers()

	mn := makeMetricName("cpu")
	payload := buildNativePayload(500, 5000, []nativeEntry{
		{mn: mn, timestamps: []int64{1000}, values: []int64{1}},
	})

	wantErr := fmt.Errorf("callback failure")
	err := Parse(bytes.NewReader(payload), "", func(b *Block) error {
		return wantErr
	})
	if err == nil {
		t.Fatal("expected error propagated from callback, got nil")
	}
	if !strings.Contains(err.Error(), wantErr.Error()) {
		t.Fatalf("unexpected error text: %v", err)
	}
}

func TestBlock_Reset(t *testing.T) {
	b := &Block{}
	b.MetricName.MetricGroup = []byte("test")
	b.Values = []float64{1, 2, 3}
	b.Timestamps = []int64{10, 20, 30}
	b.reset()
	if len(b.MetricName.MetricGroup) != 0 {
		t.Fatalf("expected empty MetricGroup after reset, got %q", b.MetricName.MetricGroup)
	}
	if len(b.Values) != 0 {
		t.Fatalf("expected empty Values after reset, got %v", b.Values)
	}
	if len(b.Timestamps) != 0 {
		t.Fatalf("expected empty Timestamps after reset, got %v", b.Timestamps)
	}
}

func TestGetPutUnmarshalWorkNative(t *testing.T) {
	uw := getUnmarshalWork()
	if uw == nil {
		t.Fatal("expected non-nil unmarshalWork")
	}
	putUnmarshalWork(uw)
	uw2 := getUnmarshalWork()
	if uw2 == nil {
		t.Fatal("expected non-nil unmarshalWork on second get")
	}
	putUnmarshalWork(uw2)
}

func TestGetPutBufferedReader(t *testing.T) {
	r := bytes.NewReader([]byte("hello"))
	br := getBufferedReader(r)
	if br == nil {
		t.Fatal("expected non-nil buffered reader")
	}
	putBufferedReader(br)
	br2 := getBufferedReader(r)
	if br2 == nil {
		t.Fatal("expected non-nil buffered reader on second get")
	}
	putBufferedReader(br2)
}

// blockSnapshot is a deep copy of Block fields used for inspection after the callback returns.
type blockSnapshot struct {
	MetricGroup []byte
	Timestamps  []int64
	Values      []float64
}

func snapshotBlock(b *Block) blockSnapshot {
	return blockSnapshot{
		MetricGroup: append([]byte(nil), b.MetricName.MetricGroup...),
		Timestamps:  append([]int64(nil), b.Timestamps...),
		Values:      append([]float64(nil), b.Values...),
	}
}

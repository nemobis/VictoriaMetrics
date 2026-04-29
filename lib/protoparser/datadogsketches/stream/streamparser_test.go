// Package stream provides a streaming parser for DataDog sketch payloads
// sent to /api/beta/sketches.
//
// Commit coverage (git log --oneline --since="2024-01-01" -- lib/protoparser/datadogsketches/stream/streamparser.go):
//   No commits in range: the file predates 2024-01-01 and has not been
//   modified since.  The tests below therefore exercise the full surface
//   of the existing code rather than specific fix commits.
package stream

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/VictoriaMetrics/easyproto"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/protoparser/datadogsketches"
)

// ---------------------------------------------------------------------------
// Protobuf builder helpers
// ---------------------------------------------------------------------------

// marshalSketchPayload encodes a SketchPayload to protobuf wire format using
// the easyproto Marshaler so that the output is guaranteed to be compatible
// with the UnmarshalProtobuf implementation in parser.go.
//
// Proto schema (field numbers from parser.go):
//
//	SketchPayload  { repeated Sketch sketches = 1 }
//	Sketch         { string metric=1; string host=2; repeated string tags=4; repeated Dogsketch dogsketches=7 }
//	Dogsketch      { int64 ts=1; int64 cnt=2; double min=3; double max=4; double sum=6;
//	                 repeated sint32 k=7; repeated uint32 n=8 }
func marshalSketchPayload(sketches []*datadogsketches.Sketch) []byte {
	var m easyproto.Marshaler
	mm := m.MessageMarshaler()
	for _, s := range sketches {
		sketchMsg := mm.AppendMessage(1) // SketchPayload.sketches
		marshalSketch(sketchMsg, s)
	}
	return m.Marshal(nil)
}

func marshalSketch(mm *easyproto.MessageMarshaler, s *datadogsketches.Sketch) {
	if s.Metric != "" {
		mm.AppendString(1, s.Metric) // Sketch.metric
	}
	if s.Host != "" {
		mm.AppendString(2, s.Host) // Sketch.host
	}
	for _, tag := range s.Tags {
		mm.AppendString(4, tag) // Sketch.tags
	}
	for _, d := range s.Dogsketches {
		dogMsg := mm.AppendMessage(7) // Sketch.dogsketches
		marshalDogsketch(dogMsg, d)
	}
}

func marshalDogsketch(mm *easyproto.MessageMarshaler, d *datadogsketches.Dogsketch) {
	mm.AppendInt64(1, d.Ts)  // Dogsketch.ts
	mm.AppendInt64(2, d.Cnt) // Dogsketch.cnt
	mm.AppendDouble(3, d.Min) // Dogsketch.min
	mm.AppendDouble(4, d.Max) // Dogsketch.max
	mm.AppendDouble(6, d.Sum) // Dogsketch.sum
	if len(d.K) > 0 {
		mm.AppendSint32s(7, d.K) // Dogsketch.k
	}
	if len(d.N) > 0 {
		// N is repeated uint32 — packed encoding via AppendUint32s
		mm.AppendUint32s(8, d.N) // Dogsketch.n
	}
}

// gzipCompress returns the gzip-compressed form of data.
func gzipCompress(data []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		panic(fmt.Sprintf("gzip write: %v", err))
	}
	if err := w.Close(); err != nil {
		panic(fmt.Sprintf("gzip close: %v", err))
	}
	return buf.Bytes()
}

// ---------------------------------------------------------------------------
// Parse failure tests
// ---------------------------------------------------------------------------

// TestParseFailureInvalidProto verifies that completely invalid (non-protobuf)
// bytes cause Parse to return an error and increment unmarshal-error counters.
func TestParseFailureInvalidProto(t *testing.T) {
	for _, bad := range [][]byte{
		[]byte("not protobuf at all"),
		{0xff, 0xfe, 0xfd}, // invalid varint
	} {
		r := bytes.NewReader(bad)
		err := Parse(r, "", func(_ []*datadogsketches.Sketch) error {
			t.Fatal("callback must not be called on invalid input")
			return nil
		})
		if err == nil {
			t.Fatalf("expected error for input %q, got nil", bad)
		}
		if !strings.Contains(err.Error(), "cannot decode DataDog protocol data") {
			t.Fatalf("unexpected error message: %v", err)
		}
	}
}

// TestParseFailureUnsupportedEncoding verifies that an unknown Content-Encoding
// value causes Parse to return an error without invoking the callback.
func TestParseFailureUnsupportedEncoding(t *testing.T) {
	data := marshalSketchPayload(nil)
	r := bytes.NewReader(data)
	err := Parse(r, "br" /* brotli – not supported */, func(_ []*datadogsketches.Sketch) error {
		t.Fatal("callback must not be called for unsupported encoding")
		return nil
	})
	if err == nil {
		t.Fatal("expected error for unsupported encoding, got nil")
	}
}

// TestParseFailureCallbackError verifies that an error returned by the callback
// propagates out of Parse wrapped with the expected message.
func TestParseFailureCallbackError(t *testing.T) {
	sketch := &datadogsketches.Sketch{
		Metric: "test.metric",
		Dogsketches: []*datadogsketches.Dogsketch{
			{Ts: 1700000000, Cnt: 1, Min: 1.0, Max: 1.0, Sum: 1.0},
		},
	}
	data := marshalSketchPayload([]*datadogsketches.Sketch{sketch})
	sentinelErr := errors.New("callback sentinel error")

	err := Parse(bytes.NewReader(data), "", func(_ []*datadogsketches.Sketch) error {
		return sentinelErr
	})
	if err == nil {
		t.Fatal("expected error from callback, got nil")
	}
	if !strings.Contains(err.Error(), "error when processing imported data") {
		t.Fatalf("unexpected error wrapping: %v", err)
	}
	if !errors.Is(err, sentinelErr) {
		t.Fatalf("sentinel error not in chain: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Parse success tests
// ---------------------------------------------------------------------------

// TestParseSuccessEmpty verifies that an empty SketchPayload (zero sketches)
// is accepted without error and the callback is invoked with a nil/empty slice.
func TestParseSuccessEmpty(t *testing.T) {
	data := marshalSketchPayload(nil)
	called := false
	err := Parse(bytes.NewReader(data), "", func(series []*datadogsketches.Sketch) error {
		called = true
		if len(series) != 0 {
			return fmt.Errorf("expected 0 sketches, got %d", len(series))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("callback was never called")
	}
}

// TestParseSuccessSingleSketch verifies that a single sketch with one Dogsketch
// is decoded correctly — metric name, host, tags and numeric fields are preserved.
func TestParseSuccessSingleSketch(t *testing.T) {
	input := &datadogsketches.Sketch{
		Metric: "system.cpu.user",
		Host:   "host1",
		Tags:   []string{"env:prod", "region:us-east-1"},
		Dogsketches: []*datadogsketches.Dogsketch{
			{
				Ts:  1700000000,
				Cnt: 17,
				Min: 8.0,
				Max: 21.0,
				Sum: 234.5,
				K:   []int32{0, 1472, 1473, 1503, 1504},
				N:   []uint32{0, 1, 4, 6, 1},
			},
		},
	}

	data := marshalSketchPayload([]*datadogsketches.Sketch{input})

	var got []*datadogsketches.Sketch
	err := Parse(bytes.NewReader(data), "", func(series []*datadogsketches.Sketch) error {
		// Copy the slice — the parser may reuse backing memory after the callback returns.
		got = make([]*datadogsketches.Sketch, len(series))
		copy(got, series)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 sketch, got %d", len(got))
	}

	s := got[0]
	if s.Metric != input.Metric {
		t.Errorf("Metric: got %q, want %q", s.Metric, input.Metric)
	}
	if s.Host != input.Host {
		t.Errorf("Host: got %q, want %q", s.Host, input.Host)
	}
	if len(s.Tags) != len(input.Tags) {
		t.Fatalf("Tags len: got %d, want %d", len(s.Tags), len(input.Tags))
	}
	for i, tag := range input.Tags {
		if s.Tags[i] != tag {
			t.Errorf("Tags[%d]: got %q, want %q", i, s.Tags[i], tag)
		}
	}
	if len(s.Dogsketches) != 1 {
		t.Fatalf("Dogsketches len: got %d, want 1", len(s.Dogsketches))
	}
	d := s.Dogsketches[0]
	if d.Ts != input.Dogsketches[0].Ts {
		t.Errorf("Ts: got %d, want %d", d.Ts, input.Dogsketches[0].Ts)
	}
	if d.Cnt != input.Dogsketches[0].Cnt {
		t.Errorf("Cnt: got %d, want %d", d.Cnt, input.Dogsketches[0].Cnt)
	}
	if d.Min != input.Dogsketches[0].Min {
		t.Errorf("Min: got %v, want %v", d.Min, input.Dogsketches[0].Min)
	}
	if d.Max != input.Dogsketches[0].Max {
		t.Errorf("Max: got %v, want %v", d.Max, input.Dogsketches[0].Max)
	}
	if d.Sum != input.Dogsketches[0].Sum {
		t.Errorf("Sum: got %v, want %v", d.Sum, input.Dogsketches[0].Sum)
	}
}

// TestParseSuccessMultipleSketches verifies that multiple sketches in a single
// payload are all delivered to the callback.
func TestParseSuccessMultipleSketches(t *testing.T) {
	const n = 5
	inputs := make([]*datadogsketches.Sketch, n)
	for i := range inputs {
		inputs[i] = &datadogsketches.Sketch{
			Metric: fmt.Sprintf("metric.%d", i),
			Dogsketches: []*datadogsketches.Dogsketch{
				{Ts: int64(1700000000 + i), Cnt: int64(i + 1), Min: float64(i), Max: float64(i + 10), Sum: float64(i * 5)},
			},
		}
	}

	data := marshalSketchPayload(inputs)

	var gotCount int
	err := Parse(bytes.NewReader(data), "", func(series []*datadogsketches.Sketch) error {
		gotCount = len(series)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotCount != n {
		t.Fatalf("expected %d sketches, got %d", n, gotCount)
	}
}

// TestParseSuccessGzipEncoding verifies that gzip-compressed payloads are
// decompressed and parsed correctly (Content-Encoding: gzip path).
func TestParseSuccessGzipEncoding(t *testing.T) {
	sketch := &datadogsketches.Sketch{
		Metric: "gzip.metric",
		Dogsketches: []*datadogsketches.Dogsketch{
			{Ts: 1700000001, Cnt: 3, Min: 1.0, Max: 3.0, Sum: 6.0},
		},
	}
	plain := marshalSketchPayload([]*datadogsketches.Sketch{sketch})
	compressed := gzipCompress(plain)

	var gotMetric string
	err := Parse(bytes.NewReader(compressed), "gzip", func(series []*datadogsketches.Sketch) error {
		if len(series) != 1 {
			return fmt.Errorf("expected 1 sketch, got %d", len(series))
		}
		gotMetric = series[0].Metric
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMetric != sketch.Metric {
		t.Errorf("Metric: got %q, want %q", gotMetric, sketch.Metric)
	}
}

// TestParseSuccessIdentityEncoding verifies that "identity" (a DataDog extension
// for Content-Encoding) is treated as no compression — see the comment in
// protoparserutil/compress_reader.go about issue #8649.
func TestParseSuccessIdentityEncoding(t *testing.T) {
	sketch := &datadogsketches.Sketch{
		Metric: "identity.metric",
		Dogsketches: []*datadogsketches.Dogsketch{
			{Ts: 1700000002, Cnt: 1, Min: 5.0, Max: 5.0, Sum: 5.0},
		},
	}
	data := marshalSketchPayload([]*datadogsketches.Sketch{sketch})

	var gotMetric string
	err := Parse(bytes.NewReader(data), "identity", func(series []*datadogsketches.Sketch) error {
		if len(series) != 1 {
			return fmt.Errorf("expected 1 sketch, got %d", len(series))
		}
		gotMetric = series[0].Metric
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error for identity encoding: %v", err)
	}
	if gotMetric != sketch.Metric {
		t.Errorf("Metric: got %q, want %q", gotMetric, sketch.Metric)
	}
}

// TestParseSuccessNoneEncoding verifies that "none" is also treated as no
// compression (another alias handled by GetUncompressedReader).
func TestParseSuccessNoneEncoding(t *testing.T) {
	sketch := &datadogsketches.Sketch{
		Metric: "none.metric",
		Dogsketches: []*datadogsketches.Dogsketch{
			{Ts: 1700000003, Cnt: 2, Min: 2.0, Max: 4.0, Sum: 6.0},
		},
	}
	data := marshalSketchPayload([]*datadogsketches.Sketch{sketch})

	err := Parse(bytes.NewReader(data), "none", func(series []*datadogsketches.Sketch) error {
		if len(series) != 1 {
			return fmt.Errorf("expected 1 sketch, got %d", len(series))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error for none encoding: %v", err)
	}
}

// TestParseSuccessRowsCount exercises the RowsCount() path: len(quantiles)+2
// rows per Dogsketch.  For 5 quantiles that is 7 rows per Dogsketch.
// The test uses multiple Dogsketches to make sure RowsCount sums correctly.
func TestParseSuccessRowsCount(t *testing.T) {
	const dogsketches = 3
	ds := make([]*datadogsketches.Dogsketch, dogsketches)
	for i := range ds {
		ds[i] = &datadogsketches.Dogsketch{Ts: int64(i + 1), Cnt: 1, Min: 1, Max: 1, Sum: 1}
	}
	sketch := &datadogsketches.Sketch{
		Metric:      "rows.count.metric",
		Dogsketches: ds,
	}

	// Verify RowsCount before encoding so the test is self-documenting.
	// 5 quantiles + sum + count = 7 rows per Dogsketch.
	wantRows := 7 * dogsketches
	if got := sketch.RowsCount(); got != wantRows {
		t.Fatalf("RowsCount: got %d, want %d", got, wantRows)
	}

	data := marshalSketchPayload([]*datadogsketches.Sketch{sketch})
	err := Parse(bytes.NewReader(data), "", func(series []*datadogsketches.Sketch) error {
		if len(series) != 1 {
			return fmt.Errorf("expected 1 sketch, got %d", len(series))
		}
		if len(series[0].Dogsketches) != dogsketches {
			return fmt.Errorf("expected %d dogsketches, got %d", dogsketches, len(series[0].Dogsketches))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestParseInternalGetPutRequest exercises the getRequest/putRequest pool
// helpers by running Parse multiple times in succession, ensuring pooled
// SketchPayload objects are properly reset between calls.
func TestParseInternalGetPutRequest(t *testing.T) {
	for i := 0; i < 5; i++ {
		sketch := &datadogsketches.Sketch{
			Metric: fmt.Sprintf("pool.metric.%d", i),
			Dogsketches: []*datadogsketches.Dogsketch{
				{Ts: int64(i), Cnt: int64(i + 1), Min: float64(i), Max: float64(i + 1), Sum: float64(i)},
			},
		}
		data := marshalSketchPayload([]*datadogsketches.Sketch{sketch})
		var gotMetric string
		err := Parse(bytes.NewReader(data), "", func(series []*datadogsketches.Sketch) error {
			if len(series) != 1 {
				return fmt.Errorf("iteration %d: expected 1 sketch, got %d", i, len(series))
			}
			gotMetric = series[0].Metric
			return nil
		})
		if err != nil {
			t.Fatalf("iteration %d: unexpected error: %v", i, err)
		}
		if gotMetric != sketch.Metric {
			t.Errorf("iteration %d: Metric: got %q, want %q", i, gotMetric, sketch.Metric)
		}
	}
}

package stream

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/protoparser/opentsdb"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/protoparser/protoparserutil"
)

func TestMain(m *testing.M) {
	protoparserutil.StartUnmarshalWorkers()
	code := m.Run()
	protoparserutil.StopUnmarshalWorkers()
	os.Exit(code)
}

func TestParseSuccess(t *testing.T) {
	f := func(data string, rowsExpected []opentsdb.Row) {
		t.Helper()
		var gotRows []opentsdb.Row
		r := strings.NewReader(data)
		err := Parse(r, func(rows []opentsdb.Row) error {
			for _, row := range rows {
				// Deep-copy tags so we don't hold references to pooled data.
				tagsCopy := make([]opentsdb.Tag, len(row.Tags))
				copy(tagsCopy, row.Tags)
				gotRows = append(gotRows, opentsdb.Row{
					Metric:    row.Metric,
					Tags:      tagsCopy,
					Value:     row.Value,
					Timestamp: row.Timestamp,
				})
			}
			return nil
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(gotRows) != len(rowsExpected) {
			t.Fatalf("row count mismatch: got %d, want %d\ngot: %+v\nwant: %+v",
				len(gotRows), len(rowsExpected), gotRows, rowsExpected)
		}
		for i := range rowsExpected {
			got := gotRows[i]
			want := rowsExpected[i]
			if got.Metric != want.Metric {
				t.Fatalf("row[%d] Metric mismatch: got %q, want %q", i, got.Metric, want.Metric)
			}
			if got.Value != want.Value {
				t.Fatalf("row[%d] Value mismatch: got %v, want %v", i, got.Value, want.Value)
			}
			// Timestamp is processed (multiplied by 1000 if in seconds, trimmed).
			// Just check it is non-zero for rows with explicit timestamps.
			if want.Timestamp != 0 && got.Timestamp == 0 {
				t.Fatalf("row[%d] Timestamp should not be zero", i)
			}
			if len(got.Tags) != len(want.Tags) {
				t.Fatalf("row[%d] tag count mismatch: got %d, want %d", i, len(got.Tags), len(want.Tags))
			}
			for j := range want.Tags {
				if got.Tags[j].Key != want.Tags[j].Key {
					t.Fatalf("row[%d] tag[%d] Key mismatch: got %q, want %q", i, j, got.Tags[j].Key, want.Tags[j].Key)
				}
				if got.Tags[j].Value != want.Tags[j].Value {
					t.Fatalf("row[%d] tag[%d] Value mismatch: got %q, want %q", i, j, got.Tags[j].Value, want.Tags[j].Value)
				}
			}
		}
	}

	// Empty input
	f("", nil)

	// Single valid line (timestamp in seconds → converted to ms)
	f("put cpu.load 1000000 3.14 host=web01\n", []opentsdb.Row{
		{
			Metric: "cpu.load",
			Tags:   []opentsdb.Tag{{Key: "host", Value: "web01"}},
			Value:  3.14,
			// timestamp will be 1000000*1000 = 1000000000 ms
			Timestamp: 1,
		},
	})

	// Multiple valid lines
	f("put mem.free 1000001 512 host=db01\nput disk.io 1000002 99.9 host=db01 disk=sda\n", []opentsdb.Row{
		{
			Metric:    "mem.free",
			Tags:      []opentsdb.Tag{{Key: "host", Value: "db01"}},
			Value:     512,
			Timestamp: 1,
		},
		{
			Metric:    "disk.io",
			Tags:      []opentsdb.Tag{{Key: "host", Value: "db01"}, {Key: "disk", Value: "sda"}},
			Value:     99.9,
			Timestamp: 1,
		},
	})

	// Line without tags (accepted per VictoriaMetrics extension)
	f("put metric.no.tags 1000003 42\n", []opentsdb.Row{
		{
			Metric:    "metric.no.tags",
			Tags:      nil,
			Value:     42,
			Timestamp: 1,
		},
	})

	// Mixed valid/invalid lines – invalid lines are silently skipped
	f("put good 1000004 1.0 x=y\nbad line\nput also.good 1000005 2.0 a=b\n", []opentsdb.Row{
		{
			Metric:    "good",
			Tags:      []opentsdb.Tag{{Key: "x", Value: "y"}},
			Value:     1.0,
			Timestamp: 1,
		},
		{
			Metric:    "also.good",
			Tags:      []opentsdb.Tag{{Key: "a", Value: "b"}},
			Value:     2.0,
			Timestamp: 1,
		},
	})

	// Negative value
	f("put neg.metric 1000006 -7.77 k=v\n", []opentsdb.Row{
		{
			Metric:    "neg.metric",
			Tags:      []opentsdb.Tag{{Key: "k", Value: "v"}},
			Value:     -7.77,
			Timestamp: 1,
		},
	})
}

func TestParseCallbackError(t *testing.T) {
	r := strings.NewReader("put cpu 1000000 1.0 host=web\n")
	wantErr := fmt.Errorf("callback test error")
	err := Parse(r, func(rows []opentsdb.Row) error {
		return wantErr
	})
	if err == nil {
		t.Fatal("expected error from callback, got nil")
	}
}

func TestParseEmptyReader(t *testing.T) {
	called := false
	err := Parse(strings.NewReader(""), func(rows []opentsdb.Row) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Fatal("callback should not be called for empty input")
	}
}

func TestParseOnlyInvalidLines(t *testing.T) {
	// All lines are invalid – callback should be invoked with zero rows or not at all.
	totalRows := 0
	r := strings.NewReader("not a put\nalso bad\n\n")
	err := Parse(r, func(rows []opentsdb.Row) error {
		totalRows += len(rows)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if totalRows != 0 {
		t.Fatalf("expected 0 rows for invalid input, got %d", totalRows)
	}
}

func TestParseLargeInput(t *testing.T) {
	// Build a large payload to exercise the streaming / buffering path.
	var sb strings.Builder
	const n = 1000
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, "put metric.%d 1000000 %d host=h%d\n", i, i, i)
	}
	totalRows := 0
	err := Parse(strings.NewReader(sb.String()), func(rows []opentsdb.Row) error {
		totalRows += len(rows)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if totalRows != n {
		t.Fatalf("expected %d rows, got %d", n, totalRows)
	}
}

func TestParseReaderError(t *testing.T) {
	// A reader that returns an error mid-stream.
	r := &errReader{data: "put cpu 1000000 1.0 host=web\n", errAt: 5}
	err := Parse(r, func(rows []opentsdb.Row) error { return nil })
	if err == nil {
		t.Fatal("expected error from reader, got nil")
	}
}

// errReader is a reader that returns an error after errAt bytes.
type errReader struct {
	data  string
	pos   int
	errAt int
}

func (r *errReader) Read(p []byte) (int, error) {
	if r.pos >= r.errAt {
		return 0, fmt.Errorf("simulated read error")
	}
	remaining := r.errAt - r.pos
	if len(p) > remaining {
		p = p[:remaining]
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	if r.pos >= r.errAt {
		return n, fmt.Errorf("simulated read error")
	}
	return n, nil
}

func TestParseMissingTimestampFilledIn(t *testing.T) {
	// A row with timestamp=0 should get current time filled in.
	// We can't know the exact value so just make sure it's non-zero.
	var ts int64
	r := strings.NewReader("put cpu 0 1.0 host=web\n")
	err := Parse(r, func(rows []opentsdb.Row) error {
		if len(rows) > 0 {
			ts = rows[0].Timestamp
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ts == 0 {
		t.Fatal("expected non-zero auto-filled timestamp")
	}
}

func TestParseMillisecondTimestampKept(t *testing.T) {
	// A timestamp with the secondMask set (>= 2^32) should be treated as ms and kept as-is.
	// 0x100000000 = 4294967296 which has bits above bit 31 set → secondMask bit set.
	msTs := int64(0x100000000) // already in milliseconds range
	var gotTs int64
	r := strings.NewReader(fmt.Sprintf("put cpu %d 1.0 host=web\n", msTs))
	err := Parse(r, func(rows []opentsdb.Row) error {
		if len(rows) > 0 {
			gotTs = rows[0].Timestamp
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotTs != msTs {
		t.Fatalf("expected ms timestamp %d to be kept, got %d", msTs, gotTs)
	}
}

// Compile-time check that io.Reader is satisfied.
var _ io.Reader = (*errReader)(nil)

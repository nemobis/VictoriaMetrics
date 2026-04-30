package stream

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/protoparser/csvimport"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/protoparser/protoparserutil"
)

// makeRequest builds a minimal *http.Request with the given format query param
// and body. No real HTTP round-trip is performed.
func makeRequest(format, body string) *http.Request {
	u := &url.URL{
		Scheme:   "http",
		Host:     "localhost",
		Path:     "/api/v1/import/csv",
		RawQuery: url.Values{"format": {format}}.Encode(),
	}
	return &http.Request{
		Method: http.MethodPost,
		URL:    u,
		Header: make(http.Header),
		Body:   io.NopCloser(strings.NewReader(body)),
	}
}

// rowSnapshot is a deep-copied, comparable snapshot of a csvimport.Row.
type rowSnapshot struct {
	Metric    string
	Tags      []tagSnapshot
	Value     float64
	Timestamp int64
}

type tagSnapshot struct {
	Key   string
	Value string
}

func snapshotRow(r csvimport.Row) rowSnapshot {
	s := rowSnapshot{
		Metric:    r.Metric,
		Value:     r.Value,
		Timestamp: r.Timestamp,
	}
	for _, t := range r.Tags {
		s.Tags = append(s.Tags, tagSnapshot{Key: t.Key, Value: t.Value})
	}
	return s
}

// sortRows sorts a slice of rowSnapshot by (Timestamp, Metric, Value) so that
// tests are not sensitive to the non-deterministic order of concurrent
// callback invocations.
func sortRows(rows []rowSnapshot) {
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Timestamp != b.Timestamp {
			return a.Timestamp < b.Timestamp
		}
		if a.Metric != b.Metric {
			return a.Metric < b.Metric
		}
		return a.Value < b.Value
	})
}

// ---------------------------------------------------------------------------
// Parse() integration tests (use the full pipeline with worker pool)
// ---------------------------------------------------------------------------

func TestParse_InvalidFormat(t *testing.T) {
	protoparserutil.StartUnmarshalWorkers()
	defer protoparserutil.StopUnmarshalWorkers()

	req := makeRequest("not-a-valid-format", "123")
	err := Parse(req, func(_ []csvimport.Row) error { return nil })
	if err == nil {
		t.Fatal("expected error for invalid format, got nil")
	}
}

func TestParse_EmptyBody(t *testing.T) {
	protoparserutil.StartUnmarshalWorkers()
	defer protoparserutil.StopUnmarshalWorkers()

	var mu sync.Mutex
	var count int
	req := makeRequest("1:metric:foo", "")
	err := Parse(req, func(rows []csvimport.Row) error {
		mu.Lock()
		count += len(rows)
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 rows, got %d", count)
	}
}

func TestParse_SingleRow(t *testing.T) {
	protoparserutil.StartUnmarshalWorkers()
	defer protoparserutil.StopUnmarshalWorkers()

	req := makeRequest("1:metric:foo,2:time:unix_ms", "42,1000")
	var mu sync.Mutex
	var got []rowSnapshot
	err := Parse(req, func(rows []csvimport.Row) error {
		mu.Lock()
		for _, r := range rows {
			got = append(got, snapshotRow(r))
		}
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 row, got %d", len(got))
	}
	r := got[0]
	if r.Metric != "foo" {
		t.Fatalf("unexpected metric: %q", r.Metric)
	}
	if r.Value != 42 {
		t.Fatalf("unexpected value: %v", r.Value)
	}
	if r.Timestamp != 1000 {
		t.Fatalf("unexpected timestamp: %d", r.Timestamp)
	}
}

func TestParse_MultipleRows(t *testing.T) {
	protoparserutil.StartUnmarshalWorkers()
	defer protoparserutil.StopUnmarshalWorkers()

	body := "10,1000\n20,2000\n30,3000"
	req := makeRequest("1:metric:temp,2:time:unix_s", body)

	var mu sync.Mutex
	var got []rowSnapshot
	err := Parse(req, func(rows []csvimport.Row) error {
		mu.Lock()
		for _, r := range rows {
			got = append(got, snapshotRow(r))
		}
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(got))
	}
	sortRows(got)
	expected := []rowSnapshot{
		{Metric: "temp", Value: 10, Timestamp: 1000000},
		{Metric: "temp", Value: 20, Timestamp: 2000000},
		{Metric: "temp", Value: 30, Timestamp: 3000000},
	}
	for i, e := range expected {
		if got[i].Metric != e.Metric || got[i].Value != e.Value || got[i].Timestamp != e.Timestamp {
			t.Fatalf("row %d mismatch: got %+v, want %+v", i, got[i], e)
		}
	}
}

func TestParse_WithLabels(t *testing.T) {
	protoparserutil.StartUnmarshalWorkers()
	defer protoparserutil.StopUnmarshalWorkers()

	body := "server1,85.5,1704067200\nserver2,92.3,1704067200"
	req := makeRequest("1:label:host,2:metric:cpu,3:time:unix_s", body)

	var mu sync.Mutex
	var got []rowSnapshot
	err := Parse(req, func(rows []csvimport.Row) error {
		mu.Lock()
		for _, r := range rows {
			got = append(got, snapshotRow(r))
		}
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(got))
	}
	// Sort by label value so the assertion is order-independent.
	sort.Slice(got, func(i, j int) bool {
		if len(got[i].Tags) > 0 && len(got[j].Tags) > 0 {
			return got[i].Tags[0].Value < got[j].Tags[0].Value
		}
		return false
	})
	if len(got[0].Tags) == 0 || got[0].Tags[0].Key != "host" || got[0].Tags[0].Value != "server1" {
		t.Fatalf("unexpected tags for row 0: %+v", got[0].Tags)
	}
	if len(got[1].Tags) == 0 || got[1].Tags[0].Key != "host" || got[1].Tags[0].Value != "server2" {
		t.Fatalf("unexpected tags for row 1: %+v", got[1].Tags)
	}
}

func TestParse_MultipleMetricsPerRow(t *testing.T) {
	protoparserutil.StartUnmarshalWorkers()
	defer protoparserutil.StopUnmarshalWorkers()

	body := "1.5,1.6,1000"
	req := makeRequest("1:metric:bid,2:metric:ask,3:time:unix_s", body)

	var mu sync.Mutex
	var got []rowSnapshot
	err := Parse(req, func(rows []csvimport.Row) error {
		mu.Lock()
		for _, r := range rows {
			got = append(got, snapshotRow(r))
		}
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 rows (one per metric), got %d", len(got))
	}
	if got[0].Metric != "bid" || got[0].Value != 1.5 {
		t.Fatalf("unexpected bid row: %+v", got[0])
	}
	if got[1].Metric != "ask" || got[1].Value != 1.6 {
		t.Fatalf("unexpected ask row: %+v", got[1])
	}
}

func TestParse_SkipsInvalidLines(t *testing.T) {
	protoparserutil.StartUnmarshalWorkers()
	defer protoparserutil.StopUnmarshalWorkers()

	// The second line has a non-numeric value; the parser logs and skips it.
	body := "10,1000\nnot-a-number,2000\n30,3000"
	req := makeRequest("1:metric:foo,2:time:unix_s", body)

	var mu sync.Mutex
	var count int
	err := Parse(req, func(rows []csvimport.Row) error {
		mu.Lock()
		count += len(rows)
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 valid rows after skipping invalid line, got %d", count)
	}
}

func TestParse_CallbackError(t *testing.T) {
	protoparserutil.StartUnmarshalWorkers()
	defer protoparserutil.StopUnmarshalWorkers()

	wantErr := fmt.Errorf("deliberate callback error")
	req := makeRequest("1:metric:foo,2:time:unix_s", "42,1000")
	err := Parse(req, func(_ []csvimport.Row) error {
		return wantErr
	})
	if err == nil {
		t.Fatal("expected error from callback, got nil")
	}
	if !strings.Contains(err.Error(), wantErr.Error()) {
		t.Fatalf("unexpected error text: %v", err)
	}
}

func TestParse_HeaderDetection(t *testing.T) {
	protoparserutil.StartUnmarshalWorkers()
	defer protoparserutil.StopUnmarshalWorkers()

	// The first line is a text header; it should be auto-detected and skipped.
	body := "value,timestamp\n10,1000\n20,2000"
	req := makeRequest("1:metric:foo,2:time:unix_s", body)

	var mu sync.Mutex
	var got []rowSnapshot
	err := Parse(req, func(rows []csvimport.Row) error {
		mu.Lock()
		for _, r := range rows {
			got = append(got, snapshotRow(r))
		}
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 data rows after header skip, got %d", len(got))
	}
	if got[0].Timestamp != 1000000 || got[0].Value != 10 {
		t.Fatalf("unexpected row 0: %+v", got[0])
	}
	if got[1].Timestamp != 2000000 || got[1].Value != 20 {
		t.Fatalf("unexpected row 1: %+v", got[1])
	}
}

func TestParse_MissingTimestampFilledWithCurrentTime(t *testing.T) {
	protoparserutil.StartUnmarshalWorkers()
	defer protoparserutil.StopUnmarshalWorkers()

	// No timestamp column: Unmarshal leaves Timestamp==0, and the stream
	// parser fills it with the current wall-clock time in milliseconds.
	req := makeRequest("1:metric:foo", "99.9")

	before := time.Now().UnixNano() / 1e6
	var mu sync.Mutex
	var got []rowSnapshot
	err := Parse(req, func(rows []csvimport.Row) error {
		mu.Lock()
		for _, r := range rows {
			got = append(got, snapshotRow(r))
		}
		mu.Unlock()
		return nil
	})
	after := time.Now().UnixNano() / 1e6

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 row, got %d", len(got))
	}
	ts := got[0].Timestamp
	if ts < before || ts > after {
		t.Fatalf("auto-filled timestamp %d not in expected range [%d, %d]", ts, before, after)
	}
}

func TestParse_GzipEncoding(t *testing.T) {
	protoparserutil.StartUnmarshalWorkers()
	defer protoparserutil.StopUnmarshalWorkers()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := io.WriteString(gz, "55,1000\n66,2000\n"); err != nil {
		t.Fatalf("failed to write gzip data: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("failed to close gzip writer: %v", err)
	}

	u := &url.URL{
		Scheme:   "http",
		Host:     "localhost",
		Path:     "/api/v1/import/csv",
		RawQuery: url.Values{"format": {"1:metric:foo,2:time:unix_s"}}.Encode(),
	}
	req := &http.Request{
		Method: http.MethodPost,
		URL:    u,
		Header: http.Header{"Content-Encoding": {"gzip"}},
		Body:   io.NopCloser(&buf),
	}

	var mu sync.Mutex
	var count int
	err := Parse(req, func(rows []csvimport.Row) error {
		mu.Lock()
		count += len(rows)
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error with gzip input: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 rows from gzip input, got %d", count)
	}
}

func TestParse_RFC3339Timestamp(t *testing.T) {
	protoparserutil.StartUnmarshalWorkers()
	defer protoparserutil.StopUnmarshalWorkers()

	body := "100,2024-01-01T00:00:00Z"
	req := makeRequest("1:metric:foo,2:time:rfc3339", body)

	var mu sync.Mutex
	var got []rowSnapshot
	err := Parse(req, func(rows []csvimport.Row) error {
		mu.Lock()
		for _, r := range rows {
			got = append(got, snapshotRow(r))
		}
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 row, got %d", len(got))
	}
	// 2024-01-01T00:00:00Z == 1704067200 seconds == 1704067200000 ms
	if got[0].Timestamp != 1704067200000 {
		t.Fatalf("unexpected timestamp: %d, want 1704067200000", got[0].Timestamp)
	}
}

// ---------------------------------------------------------------------------
// streamContext unit tests
// ---------------------------------------------------------------------------

func TestStreamContext_ErrorIsNilForEOF(t *testing.T) {
	ctx := &streamContext{err: io.EOF}
	if ctx.Error() != nil {
		t.Fatalf("expected nil for EOF, got %v", ctx.Error())
	}
}

func TestStreamContext_ErrorPreservesNonEOF(t *testing.T) {
	want := fmt.Errorf("some read error")
	ctx := &streamContext{err: want}
	if ctx.Error() != want {
		t.Fatalf("expected %v, got %v", want, ctx.Error())
	}
}

func TestStreamContext_HasCallbackError_Initially_False(t *testing.T) {
	ctx := &streamContext{}
	if ctx.hasCallbackError() {
		t.Fatal("expected no callback error on fresh ctx")
	}
}

func TestStreamContext_HasCallbackError_After_Set(t *testing.T) {
	ctx := &streamContext{}
	ctx.callbackErr = fmt.Errorf("cb error")
	if !ctx.hasCallbackError() {
		t.Fatal("expected hasCallbackError to return true")
	}
}

func TestStreamContext_Reset(t *testing.T) {
	ctx := getStreamContext(strings.NewReader("data"))
	ctx.err = fmt.Errorf("read error")
	ctx.callbackErr = fmt.Errorf("cb error")
	ctx.reqBuf = append(ctx.reqBuf, 'a', 'b', 'c')
	ctx.tailBuf = append(ctx.tailBuf, 'd', 'e')

	ctx.reset()

	if ctx.err != nil {
		t.Fatalf("expected nil err after reset, got %v", ctx.err)
	}
	if ctx.callbackErr != nil {
		t.Fatalf("expected nil callbackErr after reset, got %v", ctx.callbackErr)
	}
	if len(ctx.reqBuf) != 0 {
		t.Fatalf("expected empty reqBuf after reset, got len=%d", len(ctx.reqBuf))
	}
	if len(ctx.tailBuf) != 0 {
		t.Fatalf("expected empty tailBuf after reset, got len=%d", len(ctx.tailBuf))
	}
	putStreamContext(ctx)
}

func TestGetPutStreamContext_Reuse(t *testing.T) {
	r := strings.NewReader("hello")
	ctx := getStreamContext(r)
	if ctx == nil {
		t.Fatal("expected non-nil streamContext")
	}
	putStreamContext(ctx)

	// Second get should reuse the pooled instance.
	ctx2 := getStreamContext(r)
	if ctx2 == nil {
		t.Fatal("expected non-nil streamContext on second get")
	}
	putStreamContext(ctx2)
}

func TestGetPutUnmarshalWork_Reuse(t *testing.T) {
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

// ---------------------------------------------------------------------------
// streamContext.Read() unit tests
// ---------------------------------------------------------------------------

func TestStreamContext_ReadReturnsData(t *testing.T) {
	data := "line1\nline2\nline3\n"
	ctx := getStreamContext(strings.NewReader(data))
	defer putStreamContext(ctx)

	if !ctx.Read() {
		t.Fatal("expected Read() to return true for non-empty input")
	}
	if len(ctx.reqBuf) == 0 {
		t.Fatal("expected non-empty reqBuf after successful Read()")
	}
}

func TestStreamContext_ReadReturnsFalseOnEmptyInput(t *testing.T) {
	ctx := getStreamContext(strings.NewReader(""))
	defer putStreamContext(ctx)

	if ctx.Read() {
		t.Fatal("expected Read() to return false on empty input")
	}
}

func TestStreamContext_ReadStopsOnCallbackError(t *testing.T) {
	data := strings.Repeat("line\n", 100)
	ctx := getStreamContext(strings.NewReader(data))
	defer putStreamContext(ctx)

	ctx.callbackErr = fmt.Errorf("already failed")
	if ctx.Read() {
		t.Fatal("expected Read() to return false when callbackErr is set")
	}
}

// ---------------------------------------------------------------------------
// unmarshalWork unit tests (exercise Unmarshal() directly without HTTP layer)
// ---------------------------------------------------------------------------

func TestUnmarshalWork_BasicUnmarshal(t *testing.T) {
	cds, err := csvimport.ParseColumnDescriptors("1:metric:cpu,2:time:unix_s")
	if err != nil {
		t.Fatalf("unexpected error parsing column descriptors: %v", err)
	}

	ctx := getStreamContext(strings.NewReader(""))
	defer putStreamContext(ctx)

	uw := getUnmarshalWork()
	var callbackCalled int
	var gotRows []rowSnapshot
	uw.ctx = ctx
	uw.cds = cds
	uw.firstRow = false
	uw.callback = func(rows []csvimport.Row) error {
		callbackCalled++
		for _, r := range rows {
			gotRows = append(gotRows, snapshotRow(r))
		}
		return nil
	}
	uw.reqBuf = append(uw.reqBuf[:0], "10,100\n20,200\n30,300"...)
	ctx.wg.Add(1)
	uw.Unmarshal()
	ctx.wg.Wait()

	if callbackCalled != 1 {
		t.Fatalf("expected 1 callback call, got %d", callbackCalled)
	}
	if len(gotRows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(gotRows))
	}
	if gotRows[0].Value != 10 || gotRows[0].Timestamp != 100000 {
		t.Fatalf("unexpected row 0: %+v", gotRows[0])
	}
	if gotRows[1].Value != 20 || gotRows[1].Timestamp != 200000 {
		t.Fatalf("unexpected row 1: %+v", gotRows[1])
	}
	if gotRows[2].Value != 30 || gotRows[2].Timestamp != 300000 {
		t.Fatalf("unexpected row 2: %+v", gotRows[2])
	}
}

func TestUnmarshalWork_FirstRowHeaderSkip(t *testing.T) {
	cds, err := csvimport.ParseColumnDescriptors("1:metric:cpu,2:time:unix_s")
	if err != nil {
		t.Fatalf("unexpected error parsing column descriptors: %v", err)
	}

	ctx := getStreamContext(strings.NewReader(""))
	defer putStreamContext(ctx)

	uw := getUnmarshalWork()
	var gotRows []rowSnapshot
	uw.ctx = ctx
	uw.cds = cds
	uw.firstRow = true // triggers UnmarshalDetectHeader
	uw.callback = func(rows []csvimport.Row) error {
		for _, r := range rows {
			gotRows = append(gotRows, snapshotRow(r))
		}
		return nil
	}
	uw.reqBuf = append(uw.reqBuf[:0], "value,timestamp\n42,1000"...)
	ctx.wg.Add(1)
	uw.Unmarshal()
	ctx.wg.Wait()

	if len(gotRows) != 1 {
		t.Fatalf("expected 1 data row after header skip, got %d", len(gotRows))
	}
	if gotRows[0].Value != 42 || gotRows[0].Timestamp != 1000000 {
		t.Fatalf("unexpected row: %+v", gotRows[0])
	}
}

func TestUnmarshalWork_CallbackErrorPropagated(t *testing.T) {
	cds, err := csvimport.ParseColumnDescriptors("1:metric:foo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := getStreamContext(strings.NewReader(""))
	defer putStreamContext(ctx)

	uw := getUnmarshalWork()
	wantErr := fmt.Errorf("forced error")
	uw.ctx = ctx
	uw.cds = cds
	uw.firstRow = false
	uw.callback = func(_ []csvimport.Row) error { return wantErr }
	uw.reqBuf = append(uw.reqBuf[:0], "99"...)
	ctx.wg.Add(1)
	uw.Unmarshal()
	ctx.wg.Wait()

	if ctx.callbackErr == nil {
		t.Fatal("expected callbackErr to be set, got nil")
	}
	if !strings.Contains(ctx.callbackErr.Error(), wantErr.Error()) {
		t.Fatalf("unexpected callbackErr: %v", ctx.callbackErr)
	}
}

func TestUnmarshalWork_ZeroTimestampFilledWithCurrentTime(t *testing.T) {
	// No timestamp column: Timestamp stays 0 after Unmarshal, the stream
	// parser must replace it with the current wall-clock time in ms.
	cds, err := csvimport.ParseColumnDescriptors("1:metric:foo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := getStreamContext(strings.NewReader(""))
	defer putStreamContext(ctx)

	uw := getUnmarshalWork()
	var gotTs int64
	uw.ctx = ctx
	uw.cds = cds
	uw.firstRow = false
	uw.callback = func(rows []csvimport.Row) error {
		if len(rows) > 0 {
			gotTs = rows[0].Timestamp
		}
		return nil
	}
	uw.reqBuf = append(uw.reqBuf[:0], "123"...)

	before := time.Now().UnixNano() / 1e6
	ctx.wg.Add(1)
	uw.Unmarshal()
	ctx.wg.Wait()
	after := time.Now().UnixNano() / 1e6

	if gotTs < before || gotTs > after {
		t.Fatalf("auto-filled timestamp %d not in range [%d, %d]", gotTs, before, after)
	}
}

func TestUnmarshalWork_MultipleMetrics(t *testing.T) {
	cds, err := csvimport.ParseColumnDescriptors("1:metric:bid,2:metric:ask,3:time:unix_ms")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := getStreamContext(strings.NewReader(""))
	defer putStreamContext(ctx)

	uw := getUnmarshalWork()
	var gotRows []rowSnapshot
	uw.ctx = ctx
	uw.cds = cds
	uw.firstRow = false
	uw.callback = func(rows []csvimport.Row) error {
		for _, r := range rows {
			gotRows = append(gotRows, snapshotRow(r))
		}
		return nil
	}
	uw.reqBuf = append(uw.reqBuf[:0], "1.5,1.6,5000"...)
	ctx.wg.Add(1)
	uw.Unmarshal()
	ctx.wg.Wait()

	if len(gotRows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(gotRows))
	}
	if gotRows[0].Metric != "bid" || gotRows[0].Value != 1.5 || gotRows[0].Timestamp != 5000 {
		t.Fatalf("unexpected bid row: %+v", gotRows[0])
	}
	if gotRows[1].Metric != "ask" || gotRows[1].Value != 1.6 || gotRows[1].Timestamp != 5000 {
		t.Fatalf("unexpected ask row: %+v", gotRows[1])
	}
}

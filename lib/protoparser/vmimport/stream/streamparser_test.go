package stream

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/protoparser/protoparserutil"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/protoparser/vmimport"
)

func TestParse_EmptyInput(t *testing.T) {
	protoparserutil.StartUnmarshalWorkers()
	defer protoparserutil.StopUnmarshalWorkers()

	var mu sync.Mutex
	var gotRows []vmimport.Row
	err := Parse(bytes.NewReader(nil), "", func(rows []vmimport.Row) error {
		mu.Lock()
		gotRows = append(gotRows, rows...)
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error on empty input: %v", err)
	}
	if len(gotRows) != 0 {
		t.Fatalf("expected 0 rows, got %d", len(gotRows))
	}
}

func TestParse_SingleRow(t *testing.T) {
	protoparserutil.StartUnmarshalWorkers()
	defer protoparserutil.StopUnmarshalWorkers()

	data := `{"metric":{"__name__":"cpu_usage","host":"server1"},"values":[1.5,2.5],"timestamps":[1000,2000]}`
	var mu sync.Mutex
	var gotRows []rowSnapshot
	err := Parse(bytes.NewReader([]byte(data)), "", func(rows []vmimport.Row) error {
		mu.Lock()
		for _, r := range rows {
			gotRows = append(gotRows, snapshotRow(r))
		}
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(gotRows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(gotRows))
	}
	row := gotRows[0]
	if len(row.Values) != 2 || row.Values[0] != 1.5 || row.Values[1] != 2.5 {
		t.Fatalf("unexpected values: %v", row.Values)
	}
	if len(row.Timestamps) != 2 || row.Timestamps[0] != 1000 || row.Timestamps[1] != 2000 {
		t.Fatalf("unexpected timestamps: %v", row.Timestamps)
	}
	if len(row.Tags) == 0 {
		t.Fatalf("expected tags, got none")
	}
}

func TestParse_MultipleRows(t *testing.T) {
	protoparserutil.StartUnmarshalWorkers()
	defer protoparserutil.StopUnmarshalWorkers()

	lines := []string{
		`{"metric":{"__name__":"metric_a","host":"h1"},"values":[10],"timestamps":[100]}`,
		`{"metric":{"__name__":"metric_b","host":"h2"},"values":[20],"timestamps":[200]}`,
		`{"metric":{"__name__":"metric_c","host":"h3"},"values":[30],"timestamps":[300]}`,
	}
	data := strings.Join(lines, "\n")

	var mu sync.Mutex
	var gotRows []rowSnapshot
	err := Parse(bytes.NewReader([]byte(data)), "", func(rows []vmimport.Row) error {
		mu.Lock()
		for _, r := range rows {
			gotRows = append(gotRows, snapshotRow(r))
		}
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(gotRows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(gotRows))
	}
}

func TestParse_SkipsInvalidLines(t *testing.T) {
	protoparserutil.StartUnmarshalWorkers()
	defer protoparserutil.StopUnmarshalWorkers()

	// The second line is invalid JSON; the parser should skip it and still deliver the valid ones.
	lines := []string{
		`{"metric":{"__name__":"good1"},"values":[1],"timestamps":[10]}`,
		`this is not valid json`,
		`{"metric":{"__name__":"good2"},"values":[2],"timestamps":[20]}`,
	}
	data := strings.Join(lines, "\n")

	var mu sync.Mutex
	var gotCount int
	err := Parse(bytes.NewReader([]byte(data)), "", func(rows []vmimport.Row) error {
		mu.Lock()
		gotCount += len(rows)
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Invalid lines are logged and skipped by the vmimport parser; only valid ones reach callback.
	if gotCount != 2 {
		t.Fatalf("expected 2 valid rows, got %d", gotCount)
	}
}

func TestParse_CallbackError(t *testing.T) {
	protoparserutil.StartUnmarshalWorkers()
	defer protoparserutil.StopUnmarshalWorkers()

	data := `{"metric":{"__name__":"cpu"},"values":[1],"timestamps":[1000]}`
	wantErr := fmt.Errorf("callback failure")
	err := Parse(bytes.NewReader([]byte(data)), "", func(rows []vmimport.Row) error {
		return wantErr
	})
	if err == nil {
		t.Fatal("expected error from callback, got nil")
	}
	if !strings.Contains(err.Error(), wantErr.Error()) {
		t.Fatalf("unexpected error text: %v", err)
	}
}

func TestParse_BlankLinesIgnored(t *testing.T) {
	protoparserutil.StartUnmarshalWorkers()
	defer protoparserutil.StopUnmarshalWorkers()

	data := "\n\n" +
		`{"metric":{"__name__":"m1"},"values":[7],"timestamps":[70]}` +
		"\n\n"

	var mu sync.Mutex
	var gotCount int
	err := Parse(bytes.NewReader([]byte(data)), "", func(rows []vmimport.Row) error {
		mu.Lock()
		gotCount += len(rows)
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotCount != 1 {
		t.Fatalf("expected 1 row, got %d", gotCount)
	}
}

func TestParse_MultipleValuesPerRow(t *testing.T) {
	protoparserutil.StartUnmarshalWorkers()
	defer protoparserutil.StopUnmarshalWorkers()

	data := `{"metric":{"__name__":"temp","host":"srv"},"values":[1,2,3,4,5],"timestamps":[10,20,30,40,50]}`

	var mu sync.Mutex
	var gotRows []rowSnapshot
	err := Parse(bytes.NewReader([]byte(data)), "", func(rows []vmimport.Row) error {
		mu.Lock()
		for _, r := range rows {
			gotRows = append(gotRows, snapshotRow(r))
		}
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(gotRows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(gotRows))
	}
	if len(gotRows[0].Values) != 5 {
		t.Fatalf("expected 5 values, got %d", len(gotRows[0].Values))
	}
	if len(gotRows[0].Timestamps) != 5 {
		t.Fatalf("expected 5 timestamps, got %d", len(gotRows[0].Timestamps))
	}
}

func TestStreamContext_ErrorReturnsNilOnEOF(t *testing.T) {
	ctx := &streamContext{err: nil}
	if ctx.Error() != nil {
		t.Fatalf("expected nil error for zero ctx, got %v", ctx.Error())
	}
}

func TestStreamContext_HasCallbackError(t *testing.T) {
	ctx := &streamContext{}
	if ctx.hasCallbackError() {
		t.Fatal("expected no callback error initially")
	}
	ctx.callbackErr = fmt.Errorf("some error")
	if !ctx.hasCallbackError() {
		t.Fatal("expected callback error to be detected")
	}
}

func TestStreamContext_Reset(t *testing.T) {
	ctx := getStreamContext(bytes.NewReader(nil))
	ctx.err = fmt.Errorf("test error")
	ctx.callbackErr = fmt.Errorf("cb error")
	ctx.reqBuf = append(ctx.reqBuf, 'x')
	ctx.tailBuf = append(ctx.tailBuf, 'y')
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

func TestGetPutStreamContext(t *testing.T) {
	r := bytes.NewReader([]byte("data"))
	ctx := getStreamContext(r)
	if ctx == nil {
		t.Fatal("expected non-nil streamContext")
	}
	putStreamContext(ctx)
	// Second get should reuse the pooled context.
	ctx2 := getStreamContext(r)
	if ctx2 == nil {
		t.Fatal("expected non-nil streamContext on second get")
	}
	putStreamContext(ctx2)
}

func TestGetPutUnmarshalWork(t *testing.T) {
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

// rowSnapshot holds a deep copy of the fields we want to inspect from vmimport.Row.
type rowSnapshot struct {
	Tags       []tagSnapshot
	Values     []float64
	Timestamps []int64
}

type tagSnapshot struct {
	Key   string
	Value string
}

func snapshotRow(r vmimport.Row) rowSnapshot {
	s := rowSnapshot{
		Values:     append([]float64(nil), r.Values...),
		Timestamps: append([]int64(nil), r.Timestamps...),
	}
	for _, t := range r.Tags {
		s.Tags = append(s.Tags, tagSnapshot{
			Key:   string(t.Key),
			Value: string(t.Value),
		})
	}
	return s
}

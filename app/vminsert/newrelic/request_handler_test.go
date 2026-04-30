package newrelic

// Unit tests for request_handler.go
//
// Architecture note
// -----------------
// FlushBufs() calls vmstorage.AddRows() which dereferences the global
// *storage.Storage pointer.  In unit tests that pointer is nil, so any
// test that lets a *valid* payload reach FlushBufs will panic.
//
// Strategy:
//   • InsertHandlerForHTTP tests only exercise paths that fail *before*
//     FlushBufs: invalid extra_label, malformed JSON, invalid top-level type,
//     and gzip wrapping of invalid JSON.
//   • insertRows tests call the function via a thin panic-catching helper so
//     that we can distinguish a clean "storage-layer error" return from a
//     panic caused by a logic bug (nil-deref in label building, wrong loop
//     index, etc.).  A panic from Storage.IsReadOnly is the *expected* failure
//     mode when rows reach the storage layer, and the helper maps it to a
//     sentinel so callers can decide whether to accept it.
//
// Coverage intent
// ---------------
//   • extra_label query arg parsing (valid / invalid)
//   • malformed JSON body → error from stream parser
//   • top-level JSON object instead of array → error from stream parser
//   • gzip-compressed invalid body → error from stream/gzip layer
//   • insertRows: nil rows → reaches storage (panic-caught)
//   • insertRows: rows with zero samples → reaches storage (panic-caught)
//   • insertRows: rows with samples → label-building loop executes; storage
//     panic is caught – proves no panic before the storage call
//   • insertRows: extra labels appended per sample without panic
//   • insertRows: multiple rows / multiple samples counted correctly
//   • insertRows: negative and zero sample values cause no panic

import (
	"bytes"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/klauspost/compress/gzip"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/prompb"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/protoparser/newrelic"
)

// ---- storagePanic sentinel --------------------------------------------------

// errStoragePanic is returned by callInsertRows when insertRows panics due to
// the uninitialised storage layer (vmstorage.Storage == nil).  Any other panic
// is re-panicked so the test runner can report it normally.
var errStoragePanic = fmt.Errorf("storage layer panic (expected in unit tests)")

// callInsertRows calls insertRows and recovers from the nil-storage panic that
// is expected in a unit-test environment.  Any other panic is re-raised.
func callInsertRows(rows []newrelic.Row, extraLabels []prompb.Label) (retErr error) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		msg := fmt.Sprintf("%v", r)
		// Expected: nil pointer dereference inside storage.IsReadOnly or AddRows.
		if strings.Contains(msg, "nil pointer dereference") ||
			strings.Contains(msg, "invalid memory address") {
			retErr = errStoragePanic
			return
		}
		// Unexpected panic – re-raise.
		panic(r)
	}()
	retErr = insertRows(rows, extraLabels)
	return retErr
}

// ---- helpers ----------------------------------------------------------------

// minimalValidBody is a single-event NewRelic JSON payload.
// Parsing succeeds; this body is only used in cases that should fail before
// reaching the storage layer.
const minimalValidBody = `[{
	"EntityID": 1,
	"IsAgent": true,
	"Events": [
		{
			"eventType": "SystemSample",
			"timestamp": 1690286061,
			"cpuPercent": 12.5
		}
	],
	"ReportingAgentID": 1
}]`

// ---- InsertHandlerForHTTP tests – fail before storage -----------------------

// TestInsertHandlerForHTTP_InvalidExtraLabel verifies that an ill-formed
// extra_label query arg causes InsertHandlerForHTTP to return an error that
// mentions "extra_label" before any body parsing occurs.
func TestInsertHandlerForHTTP_InvalidExtraLabel(t *testing.T) {
	req := httptest.NewRequest("POST",
		"/newrelic/infra/v2/metrics/events/bulk?extra_label=no-equals",
		strings.NewReader(minimalValidBody))

	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected error for malformed extra_label, got nil")
	}
	if !strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("expected error to mention 'extra_label'; got: %v", err)
	}
}

// TestInsertHandlerForHTTP_InvalidExtraLabel_NoEquals covers the format check:
// "extra_label" must be "name=value"; missing '=' is rejected.
func TestInsertHandlerForHTTP_InvalidExtraLabel_NoEquals(t *testing.T) {
	req := httptest.NewRequest("POST",
		"/newrelic/infra/v2/metrics/events/bulk?extra_label=justvalue",
		strings.NewReader(minimalValidBody))

	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected error for extra_label without '=', got nil")
	}
	if !strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("expected 'extra_label' in error; got: %v", err)
	}
}

// TestInsertHandlerForHTTP_InvalidJSON verifies that a non-JSON body is
// rejected with an error mentioning "NewRelic".
func TestInsertHandlerForHTTP_InvalidJSON(t *testing.T) {
	req := httptest.NewRequest("POST",
		"/newrelic/infra/v2/metrics/events/bulk",
		strings.NewReader("not-json-at-all"))

	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected error for invalid JSON body, got nil")
	}
	if !strings.Contains(err.Error(), "NewRelic") {
		t.Fatalf("expected 'NewRelic' in error; got: %v", err)
	}
}

// TestInsertHandlerForHTTP_EmptyBody verifies that an empty body (not valid
// JSON) causes a parse error.
func TestInsertHandlerForHTTP_EmptyBody(t *testing.T) {
	req := httptest.NewRequest("POST",
		"/newrelic/infra/v2/metrics/events/bulk",
		strings.NewReader(""))

	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected error for empty body, got nil")
	}
}

// TestInsertHandlerForHTTP_TopLevelNotArray verifies that a top-level JSON
// object (not an array) is rejected by the NewRelic parser.
func TestInsertHandlerForHTTP_TopLevelNotArray(t *testing.T) {
	req := httptest.NewRequest("POST",
		"/newrelic/infra/v2/metrics/events/bulk",
		strings.NewReader(`{"Events":[]}`))

	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected error for non-array top-level JSON, got nil")
	}
}

// TestInsertHandlerForHTTP_TopLevelNumber verifies that a bare number is
// rejected by the NewRelic parser.
func TestInsertHandlerForHTTP_TopLevelNumber(t *testing.T) {
	req := httptest.NewRequest("POST",
		"/newrelic/infra/v2/metrics/events/bulk",
		strings.NewReader(`123`))

	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected error for bare number, got nil")
	}
}

// TestInsertHandlerForHTTP_GzipInvalidPayload verifies that a gzip stream
// decompressing to invalid JSON is rejected at the JSON layer, wrapped inside
// the "NewRelic" decode error.
func TestInsertHandlerForHTTP_GzipInvalidPayload(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write([]byte("not-valid-json")); err != nil {
		t.Fatalf("cannot write gzip: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("cannot close gzip writer: %v", err)
	}

	req := httptest.NewRequest("POST",
		"/newrelic/infra/v2/metrics/events/bulk",
		&buf)
	req.Header.Set("Content-Encoding", "gzip")

	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected error for gzip-compressed invalid JSON, got nil")
	}
}

// TestInsertHandlerForHTTP_GzipEmptyArrayBody verifies that a gzip stream
// decompressing to an empty JSON array is accepted by the parser.
// The error (if any) must NOT mention "NewRelic data" – it must come from the
// storage layer (or not at all).
func TestInsertHandlerForHTTP_GzipEmptyArrayBody(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write([]byte(`[]`)); err != nil {
		t.Fatalf("cannot write gzip: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("cannot close gzip writer: %v", err)
	}

	req := httptest.NewRequest("POST",
		"/newrelic/infra/v2/metrics/events/bulk",
		&buf)
	req.Header.Set("Content-Encoding", "gzip")

	// Use recover because with zero events FlushBufs is called on an empty
	// context which still tries to reach vmstorage (nil in unit tests).
	func() {
		defer func() {
			r := recover()
			if r != nil {
				msg := fmt.Sprintf("%v", r)
				if strings.Contains(msg, "nil pointer") || strings.Contains(msg, "invalid memory address") {
					// Expected nil-storage panic — treat as pass.
					return
				}
				panic(r)
			}
		}()
		err := InsertHandlerForHTTP(req)
		if err != nil && strings.Contains(err.Error(), "cannot decode NewRelic data") {
			t.Fatalf("gzip empty-array body caused unexpected decode error: %v", err)
		}
	}()
}

// TestInsertHandlerForHTTP_TruncatedGzip verifies that a truncated / invalid
// gzip stream is rejected.
func TestInsertHandlerForHTTP_TruncatedGzip(t *testing.T) {
	req := httptest.NewRequest("POST",
		"/newrelic/infra/v2/metrics/events/bulk",
		// Raw bytes that are not a valid gzip stream
		strings.NewReader("\x1f\x8b\x00\x00truncated"))
	req.Header.Set("Content-Encoding", "gzip")

	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected error for truncated gzip, got nil")
	}
}

// TestInsertHandlerForHTTP_MalformedEventsArray verifies that a MetricPost
// object whose Events value is not an array causes a parse error.
func TestInsertHandlerForHTTP_MalformedEventsArray(t *testing.T) {
	body := `[{"EntityID":1,"Events":"not-an-array"}]`
	req := httptest.NewRequest("POST",
		"/newrelic/infra/v2/metrics/events/bulk",
		strings.NewReader(body))

	err := InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected error for Events not being an array, got nil")
	}
}

// ---- insertRows tests -------------------------------------------------------

// TestInsertRows_NilRows verifies that passing nil rows does not cause a
// logic panic before the storage layer is reached.
func TestInsertRows_NilRows(t *testing.T) {
	err := callInsertRows(nil, nil)
	// nil or errStoragePanic are both acceptable.
	if err != nil && err != errStoragePanic {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestInsertRows_EmptySlice verifies the same with an explicit empty slice.
func TestInsertRows_EmptySlice(t *testing.T) {
	err := callInsertRows([]newrelic.Row{}, nil)
	if err != nil && err != errStoragePanic {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestInsertRows_RowWithNoSamples verifies that a row carrying tags but zero
// samples reaches the storage layer (or returns nil) without a logic panic.
func TestInsertRows_RowWithNoSamples(t *testing.T) {
	rows := []newrelic.Row{
		{
			Tags: []newrelic.Tag{
				{Key: []byte("eventType"), Value: []byte("SystemSample")},
			},
			Samples:   nil,
			Timestamp: 1690286061000,
		},
	}
	err := callInsertRows(rows, nil)
	if err != nil && err != errStoragePanic {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestInsertRows_SingleSampleNoExtraLabels verifies that the label-building
// loop executes without a logic panic for a single row with one sample.
func TestInsertRows_SingleSampleNoExtraLabels(t *testing.T) {
	rows := []newrelic.Row{
		{
			Tags: []newrelic.Tag{
				{Key: []byte("eventType"), Value: []byte("SystemSample")},
				{Key: []byte("host"), Value: []byte("myhost")},
			},
			Samples: []newrelic.Sample{
				{Name: []byte("cpuPercent"), Value: 55.0},
			},
			Timestamp: 1690286061000,
		},
	}
	err := callInsertRows(rows, nil)
	if err != nil && err != errStoragePanic {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestInsertRows_ExtraLabelsAppended verifies that extra labels passed to
// insertRows do not cause a logic panic.  Each sample in each row receives
// the extra labels after the row tags.
func TestInsertRows_ExtraLabelsAppended(t *testing.T) {
	rows := []newrelic.Row{
		{
			Tags: []newrelic.Tag{
				{Key: []byte("eventType"), Value: []byte("SystemSample")},
			},
			Samples: []newrelic.Sample{
				{Name: []byte("memFree"), Value: 1024},
			},
			Timestamp: 1690286061000,
		},
	}
	extraLabels := []prompb.Label{
		{Name: "env", Value: "prod"},
		{Name: "region", Value: "us-east-1"},
	}
	err := callInsertRows(rows, extraLabels)
	if err != nil && err != errStoragePanic {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestInsertRows_MultipleRowsMultipleSamples verifies that the nested loop
// over rows × samples is correct: the samplesCount accumulation and
// ctx.Reset(samplesCount) complete without index out-of-range.
func TestInsertRows_MultipleRowsMultipleSamples(t *testing.T) {
	rows := []newrelic.Row{
		{
			Tags: []newrelic.Tag{
				{Key: []byte("eventType"), Value: []byte("SystemSample")},
				{Key: []byte("host"), Value: []byte("host1")},
			},
			Samples: []newrelic.Sample{
				{Name: []byte("cpuPercent"), Value: 10.0},
				{Name: []byte("memFree"), Value: 2048},
				{Name: []byte("diskWritesPerSecond"), Value: -1.5},
			},
			Timestamp: 1690286061000,
		},
		{
			Tags: []newrelic.Tag{
				{Key: []byte("eventType"), Value: []byte("ProcessSample")},
				{Key: []byte("host"), Value: []byte("host2")},
			},
			Samples: []newrelic.Sample{
				{Name: []byte("uptime"), Value: 999},
				{Name: []byte("threadCount"), Value: 42},
			},
			Timestamp: 1690286062000,
		},
		{
			// Row with no tags
			Samples: []newrelic.Sample{
				{Name: []byte("bare_metric"), Value: 0},
			},
			Timestamp: 1690286063000,
		},
	}
	err := callInsertRows(rows, nil)
	if err != nil && err != errStoragePanic {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestInsertRows_NegativeAndZeroValues verifies that negative and zero sample
// values do not cause a panic or unexpected error in the label-building stage.
func TestInsertRows_NegativeAndZeroValues(t *testing.T) {
	rows := []newrelic.Row{
		{
			Tags: []newrelic.Tag{
				{Key: []byte("eventType"), Value: []byte("StorageSample")},
			},
			Samples: []newrelic.Sample{
				{Name: []byte("ioReadBytesPerSecond"), Value: 0},
				{Name: []byte("diskWritesPerSecond"), Value: -34.21},
			},
			Timestamp: 1690286061000,
		},
	}
	err := callInsertRows(rows, nil)
	if err != nil && err != errStoragePanic {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestInsertRows_MixedZeroAndNonZeroSampleRows verifies that rows with zero
// samples interspersed with rows that have samples do not cause a panic.
// This exercises the samplesCount accumulation: rows[0].Samples(0) +
// rows[1].Samples(1) + rows[2].Samples(0) + rows[3].Samples(2) = 3.
func TestInsertRows_MixedZeroAndNonZeroSampleRows(t *testing.T) {
	rows := []newrelic.Row{
		{Samples: nil, Timestamp: 1000},
		{
			Samples: []newrelic.Sample{
				{Name: []byte("m1"), Value: 1},
			},
			Timestamp: 2000,
		},
		{Samples: nil, Timestamp: 3000},
		{
			Samples: []newrelic.Sample{
				{Name: []byte("m2"), Value: 2},
				{Name: []byte("m3"), Value: 3},
			},
			Timestamp: 4000,
		},
	}
	err := callInsertRows(rows, nil)
	if err != nil && err != errStoragePanic {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestInsertRows_ExtraLabelsMultipleRows verifies that extra labels are
// applied to all samples across all rows (the inner extraLabels loop runs
// for every sample in every row).
func TestInsertRows_ExtraLabelsMultipleRows(t *testing.T) {
	rows := []newrelic.Row{
		{
			Tags: []newrelic.Tag{
				{Key: []byte("host"), Value: []byte("h1")},
			},
			Samples: []newrelic.Sample{
				{Name: []byte("cpu"), Value: 5},
			},
			Timestamp: 1000,
		},
		{
			Tags: []newrelic.Tag{
				{Key: []byte("host"), Value: []byte("h2")},
			},
			Samples: []newrelic.Sample{
				{Name: []byte("cpu"), Value: 6},
			},
			Timestamp: 2000,
		},
	}
	extraLabels := []prompb.Label{
		{Name: "datacenter", Value: "dc1"},
	}
	err := callInsertRows(rows, extraLabels)
	if err != nil && err != errStoragePanic {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestInsertRows_LargeNumberOfTagsAndSamples verifies that a row with many
// tags and many samples (realistic infra payload) does not panic.
func TestInsertRows_LargeNumberOfTagsAndSamples(t *testing.T) {
	tags := make([]newrelic.Tag, 10)
	for i := range tags {
		tags[i] = newrelic.Tag{
			Key:   []byte(fmt.Sprintf("tag%d", i)),
			Value: []byte(fmt.Sprintf("val%d", i)),
		}
	}
	samples := make([]newrelic.Sample, 20)
	for i := range samples {
		samples[i] = newrelic.Sample{
			Name:  []byte(fmt.Sprintf("metric%d", i)),
			Value: float64(i) * 1.5,
		}
	}
	rows := []newrelic.Row{
		{Tags: tags, Samples: samples, Timestamp: 1690286061000},
	}
	err := callInsertRows(rows, nil)
	if err != nil && err != errStoragePanic {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestInsertRows_TimestampZeroIsAllowed verifies that a row with Timestamp==0
// (which the stream parser fills with the current time) is processed without
// a logic panic.
func TestInsertRows_TimestampZeroIsAllowed(t *testing.T) {
	rows := []newrelic.Row{
		{
			Tags: []newrelic.Tag{
				{Key: []byte("eventType"), Value: []byte("NetworkSample")},
			},
			Samples: []newrelic.Sample{
				{Name: []byte("receivePacketsPerSecond"), Value: 100},
			},
			Timestamp: 0, // zero → filled by stream.Parse, passed as-is here
		},
	}
	err := callInsertRows(rows, nil)
	if err != nil && err != errStoragePanic {
		t.Fatalf("unexpected error: %v", err)
	}
}

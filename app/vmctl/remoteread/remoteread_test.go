package remoteread

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gogo/protobuf/proto"
	"github.com/golang/snappy"
	"github.com/prometheus/prometheus/prompb"

	"github.com/VictoriaMetrics/VictoriaMetrics/app/vmctl/vm"
)

// ---------------------------------------------------------------------------
// NewClient validation tests
// ---------------------------------------------------------------------------

func TestNewClientEmptyAddr(t *testing.T) {
	_, err := NewClient(Config{})
	if err == nil {
		t.Fatal("expected error for empty Addr, got nil")
	}
}

func TestNewClientDefaultTimeout(t *testing.T) {
	c, err := NewClient(Config{Addr: "http://localhost:9090"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.c.Timeout != defaultReadTimeout {
		t.Errorf("timeout = %v; want %v", c.c.Timeout, defaultReadTimeout)
	}
}

func TestNewClientTrailingSlashStripped(t *testing.T) {
	c, err := NewClient(Config{Addr: "http://localhost:9090/"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.addr != "http://localhost:9090" {
		t.Errorf("addr = %q; want no trailing slash", c.addr)
	}
}

func TestNewClientMalformedHeader(t *testing.T) {
	_, err := NewClient(Config{
		Addr:    "http://localhost:9090",
		Headers: "no-colon-here",
	})
	if err == nil {
		t.Fatal("expected error for malformed header, got nil")
	}
}

func TestNewClientValidHeader(t *testing.T) {
	c, err := NewClient(Config{
		Addr:    "http://localhost:9090",
		Headers: "X-Foo: bar^^X-Baz: qux",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(c.headers) != 2 {
		t.Fatalf("headers len = %d; want 2", len(c.headers))
	}
	if c.headers[0].key != "X-Foo" || c.headers[0].value != "bar" {
		t.Errorf("header[0] = %+v; want {X-Foo bar}", c.headers[0])
	}
	if c.headers[1].key != "X-Baz" || c.headers[1].value != "qux" {
		t.Errorf("header[1] = %+v; want {X-Baz qux}", c.headers[1])
	}
}

func TestNewClientLabelMismatch(t *testing.T) {
	_, err := NewClient(Config{
		Addr:        "http://localhost",
		LabelNames:  []string{"job"},
		LabelValues: []string{},
	})
	if err == nil {
		t.Fatal("expected error for mismatched label names/values, got nil")
	}
}

func TestNewClientEmptyLabelName(t *testing.T) {
	_, err := NewClient(Config{
		Addr:        "http://localhost",
		LabelNames:  []string{""},
		LabelValues: []string{"val"},
	})
	if err == nil {
		t.Fatal("expected error for empty label name, got nil")
	}
}

func TestNewClientMatchers(t *testing.T) {
	c, err := NewClient(Config{
		Addr:        "http://localhost",
		LabelNames:  []string{"job", "instance"},
		LabelValues: []string{"prometheus", "localhost:9090"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(c.matchers) != 2 {
		t.Fatalf("matchers len = %d; want 2", len(c.matchers))
	}
	if c.matchers[0].Name != "job" {
		t.Errorf("matcher[0].Name = %q; want job", c.matchers[0].Name)
	}
	if c.matchers[0].Type != prompb.LabelMatcher_RE {
		t.Errorf("matcher[0].Type = %v; want RE", c.matchers[0].Type)
	}
}

func TestNewClientDisablePathAppend(t *testing.T) {
	c, err := NewClient(Config{
		Addr:              "http://localhost/custom/path",
		DisablePathAppend: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !c.disablePathAppend {
		t.Error("disablePathAppend = false; want true")
	}
}

func TestNewClientUseStream(t *testing.T) {
	c, err := NewClient(Config{
		Addr:      "http://localhost",
		UseStream: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !c.useStream {
		t.Error("useStream = false; want true")
	}
}

// ---------------------------------------------------------------------------
// parseHeaders unit tests
// ---------------------------------------------------------------------------

func TestParseHeadersEmpty(t *testing.T) {
	kvs, err := parseHeaders(nil)
	if err != nil {
		t.Fatalf("parseHeaders(nil) error: %v", err)
	}
	if kvs != nil {
		t.Errorf("parseHeaders(nil) = %v; want nil", kvs)
	}
}

func TestParseHeadersMissingColon(t *testing.T) {
	_, err := parseHeaders([]string{"BadHeader"})
	if err == nil {
		t.Fatal("expected error for header without colon")
	}
}

func TestParseHeadersTrimsWhitespace(t *testing.T) {
	kvs, err := parseHeaders([]string{"  Key  :  Value  "})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if kvs[0].key != "Key" {
		t.Errorf("key = %q; want Key", kvs[0].key)
	}
	if kvs[0].value != "Value" {
		t.Errorf("value = %q; want Value", kvs[0].value)
	}
}

// ---------------------------------------------------------------------------
// convertSamples unit tests
// ---------------------------------------------------------------------------

func TestConvertSamplesEmpty(t *testing.T) {
	ts := convertSamples(nil, nil)
	if ts == nil {
		t.Fatal("convertSamples returned nil")
	}
	if ts.Name != "" {
		t.Errorf("Name = %q; want empty", ts.Name)
	}
	if len(ts.Timestamps) != 0 {
		t.Errorf("Timestamps len = %d; want 0", len(ts.Timestamps))
	}
}

func TestConvertSamplesExtractsName(t *testing.T) {
	labels := []prompb.Label{
		{Name: "__name__", Value: "cpu_usage"},
		{Name: "job", Value: "prometheus"},
	}
	samples := []prompb.Sample{
		{Timestamp: 1000, Value: 3.14},
	}
	ts := convertSamples(samples, labels)
	if ts.Name != "cpu_usage" {
		t.Errorf("Name = %q; want cpu_usage", ts.Name)
	}
	if len(ts.LabelPairs) != 1 {
		t.Fatalf("LabelPairs len = %d; want 1", len(ts.LabelPairs))
	}
	if ts.LabelPairs[0].Name != "job" {
		t.Errorf("LabelPairs[0].Name = %q; want job", ts.LabelPairs[0].Name)
	}
}

func TestConvertSamplesValues(t *testing.T) {
	samples := []prompb.Sample{
		{Timestamp: 100, Value: 1.1},
		{Timestamp: 200, Value: 2.2},
		{Timestamp: 300, Value: 3.3},
	}
	ts := convertSamples(samples, nil)
	if len(ts.Timestamps) != 3 {
		t.Fatalf("Timestamps len = %d; want 3", len(ts.Timestamps))
	}
	for i, s := range samples {
		if ts.Timestamps[i] != s.Timestamp {
			t.Errorf("Timestamps[%d] = %d; want %d", i, ts.Timestamps[i], s.Timestamp)
		}
		if ts.Values[i] != s.Value {
			t.Errorf("Values[%d] = %f; want %f", i, ts.Values[i], s.Value)
		}
	}
}

// ---------------------------------------------------------------------------
// processResponse unit tests (via fake HTTP server)
// ---------------------------------------------------------------------------

// buildSnappyProtoResponse encodes a prompb.ReadResponse as snappy-compressed protobuf.
func buildSnappyProtoResponse(t *testing.T, resp *prompb.ReadResponse) []byte {
	t.Helper()
	data, err := proto.Marshal(resp)
	if err != nil {
		t.Fatalf("proto.Marshal: %v", err)
	}
	return snappy.Encode(nil, data)
}

func TestReadHTTPNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	c, err := NewClient(Config{Addr: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	filter := &Filter{StartTimestampMs: 0, EndTimestampMs: 1000}
	err = c.Read(context.Background(), filter, func(_ *vm.TimeSeries) error { return nil })
	if err == nil {
		t.Fatal("expected error for 500 response, got nil")
	}
}

// TestReadHTTP204EmptyBodyErrors documents the current behaviour: a true
// HTTP 204 (no body) passes the status-code check but then fails at the
// snappy decode stage because the body is empty.
func TestReadHTTP204EmptyBodyErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
		// HTTP 204 must have no body; the Go server will discard any writes.
	}))
	defer srv.Close()

	c, err := NewClient(Config{Addr: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	filter := &Filter{StartTimestampMs: 0, EndTimestampMs: 1000}
	err = c.Read(context.Background(), filter, func(_ *vm.TimeSeries) error { return nil })
	// The status-code guard passes (204 is allowed), but processResponse fails
	// because the empty body cannot be snappy-decoded.
	if err == nil {
		t.Fatal("expected snappy decode error for empty 204 body, got nil")
	}
}

func TestReadHTTP200EmptyResponse(t *testing.T) {
	readResp := &prompb.ReadResponse{}
	encoded := buildSnappyProtoResponse(t, readResp)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(encoded)
	}))
	defer srv.Close()

	c, err := NewClient(Config{Addr: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	filter := &Filter{StartTimestampMs: 0, EndTimestampMs: 1000}
	var called int
	err = c.Read(context.Background(), filter, func(_ *vm.TimeSeries) error {
		called++
		return nil
	})
	if err != nil {
		t.Fatalf("Read() unexpected error: %v", err)
	}
	if called != 0 {
		t.Errorf("callback called %d times; want 0", called)
	}
}

func TestReadHTTP200WithTimeSeries(t *testing.T) {
	readResp := &prompb.ReadResponse{
		Results: []*prompb.QueryResult{
			{
				Timeseries: []*prompb.TimeSeries{
					{
						Labels: []prompb.Label{
							{Name: "__name__", Value: "up"},
							{Name: "job", Value: "test"},
						},
						Samples: []prompb.Sample{
							{Timestamp: 1000, Value: 1.0},
							{Timestamp: 2000, Value: 0.0},
						},
					},
				},
			},
		},
	}
	encoded := buildSnappyProtoResponse(t, readResp)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(encoded)
	}))
	defer srv.Close()

	c, err := NewClient(Config{Addr: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	filter := &Filter{StartTimestampMs: 0, EndTimestampMs: 3000}
	var collected []*vm.TimeSeries
	err = c.Read(context.Background(), filter, func(ts *vm.TimeSeries) error {
		collected = append(collected, ts)
		return nil
	})
	if err != nil {
		t.Fatalf("Read() unexpected error: %v", err)
	}
	if len(collected) != 1 {
		t.Fatalf("collected %d series; want 1", len(collected))
	}
	ts := collected[0]
	if ts.Name != "up" {
		t.Errorf("Name = %q; want up", ts.Name)
	}
	if len(ts.Timestamps) != 2 {
		t.Errorf("Timestamps len = %d; want 2", len(ts.Timestamps))
	}
}

func TestReadHTTPCallbackError(t *testing.T) {
	readResp := &prompb.ReadResponse{
		Results: []*prompb.QueryResult{
			{
				Timeseries: []*prompb.TimeSeries{
					{
						Labels:  []prompb.Label{{Name: "__name__", Value: "metric"}},
						Samples: []prompb.Sample{{Timestamp: 1000, Value: 1.0}},
					},
				},
			},
		},
	}
	encoded := buildSnappyProtoResponse(t, readResp)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(encoded)
	}))
	defer srv.Close()

	c, err := NewClient(Config{Addr: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	filter := &Filter{StartTimestampMs: 0, EndTimestampMs: 2000}
	cbErr := fmt.Errorf("callback failed")
	err = c.Read(context.Background(), filter, func(_ *vm.TimeSeries) error {
		return cbErr
	})
	if err == nil {
		t.Fatal("expected error from callback, got nil")
	}
}

func TestReadCancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buildSnappyProtoResponse(t, &prompb.ReadResponse{}))
	}))
	defer srv.Close()

	c, err := NewClient(Config{Addr: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	filter := &Filter{StartTimestampMs: 0, EndTimestampMs: 1000}
	err = c.Read(ctx, filter, func(_ *vm.TimeSeries) error { return nil })
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
}

func TestReadHTTPBasicAuth(t *testing.T) {
	var gotUser, gotPass string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, _ = r.BasicAuth()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c, err := NewClient(Config{
		Addr:     srv.URL,
		Username: "alice",
		Password: "secret",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	filter := &Filter{StartTimestampMs: 0, EndTimestampMs: 1000}
	_ = c.Read(context.Background(), filter, func(_ *vm.TimeSeries) error { return nil })
	if gotUser != "alice" {
		t.Errorf("username = %q; want alice", gotUser)
	}
	if gotPass != "secret" {
		t.Errorf("password = %q; want secret", gotPass)
	}
}

func TestReadHTTPCustomHeaders(t *testing.T) {
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Custom")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c, err := NewClient(Config{
		Addr:    srv.URL,
		Headers: "X-Custom: myvalue",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	filter := &Filter{StartTimestampMs: 0, EndTimestampMs: 1000}
	_ = c.Read(context.Background(), filter, func(_ *vm.TimeSeries) error { return nil })
	if gotHeader != "myvalue" {
		t.Errorf("X-Custom header = %q; want myvalue", gotHeader)
	}
}

func TestReadHTTPDisablePathAppend(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	// With DisablePathAppend the full URL is used as-is.
	customURL := srv.URL + "/my/custom/read"
	c, err := NewClient(Config{
		Addr:              customURL,
		DisablePathAppend: true,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	filter := &Filter{StartTimestampMs: 0, EndTimestampMs: 1000}
	_ = c.Read(context.Background(), filter, func(_ *vm.TimeSeries) error { return nil })
	if gotPath != "/my/custom/read" {
		t.Errorf("path = %q; want /my/custom/read", gotPath)
	}
}

func TestReadHTTPDefaultPathAppend(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c, err := NewClient(Config{Addr: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	filter := &Filter{StartTimestampMs: 0, EndTimestampMs: 1000}
	_ = c.Read(context.Background(), filter, func(_ *vm.TimeSeries) error { return nil })
	if gotPath != remoteReadPath {
		t.Errorf("path = %q; want %q", gotPath, remoteReadPath)
	}
}

// ---------------------------------------------------------------------------
// processResponse direct tests
// ---------------------------------------------------------------------------

func TestProcessResponseInvalidSnappy(t *testing.T) {
	body := io.NopCloser(bytes.NewReader([]byte("not snappy")))
	err := processResponse(body, func(_ *vm.TimeSeries) error { return nil })
	if err == nil {
		t.Fatal("expected error for invalid snappy data, got nil")
	}
}

func TestProcessResponseInvalidProto(t *testing.T) {
	// Valid snappy but invalid protobuf.
	bad := snappy.Encode(nil, []byte("not a protobuf"))
	body := io.NopCloser(bytes.NewReader(bad))
	err := processResponse(body, func(_ *vm.TimeSeries) error { return nil })
	if err == nil {
		t.Fatal("expected error for invalid protobuf, got nil")
	}
}

// ---------------------------------------------------------------------------
// parseSamples tests
// ---------------------------------------------------------------------------

func TestParseSamplesInvalidChunk(t *testing.T) {
	_, err := parseSamples([]byte("garbage chunk data"))
	if err == nil {
		t.Fatal("expected error for invalid chunk data, got nil")
	}
}

func TestParseSamplesEmptyChunk(t *testing.T) {
	// An empty XOR chunk header: 2 bytes for sample count (0) + encoding byte.
	// XOR chunk format: 2 bytes (big-endian uint16 for num samples), then data.
	chunk := make([]byte, 3)
	binary.BigEndian.PutUint16(chunk[:2], 0)
	chunk[2] = 0 // no data
	// This may or may not error depending on the chunkenc implementation.
	// The important thing is it doesn't panic.
	samples, _ := parseSamples(chunk)
	_ = samples
}

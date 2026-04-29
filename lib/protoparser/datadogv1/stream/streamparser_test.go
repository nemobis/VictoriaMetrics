package stream

import (
	"bytes"
	"compress/gzip"
	"strings"
	"testing"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/protoparser/datadogv1"
)

// TestParseJSONRoundtrip verifies that Parse correctly decodes a plain-JSON
// DataDog v1 series payload and delivers the series to the callback.
func TestParseJSONRoundtrip(t *testing.T) {
	body := `{"series":[{"metric":"http.requests","points":[[1000,42]],"host":"myhost","tags":["env:prod","service:web"]}]}`

	var got []datadogv1.Series
	err := Parse(bytes.NewReader([]byte(body)), "", func(series []datadogv1.Series) error {
		// Deep copy: the backing Request is pooled and may be reset after
		// the callback returns.
		cp := make([]datadogv1.Series, len(series))
		copy(cp, series)
		got = append(got, cp...)
		return nil
	})
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 series, got %d", len(got))
	}
	s := got[0]
	if s.Metric != "http.requests" {
		t.Errorf("metric: got %q, want %q", s.Metric, "http.requests")
	}
	if s.Host != "myhost" {
		t.Errorf("host: got %q, want %q", s.Host, "myhost")
	}
	if len(s.Points) != 1 {
		t.Fatalf("points: got %d, want 1", len(s.Points))
	}
	if s.Points[0][1] != 42 {
		t.Errorf("point value: got %v, want 42", s.Points[0][1])
	}
}

// TestParseGzipEncoding verifies that Parse can decode a gzip-compressed body.
func TestParseGzipEncoding(t *testing.T) {
	body := `{"series":[{"metric":"cpu.usage","points":[[2000,99.5]],"host":"h1"}]}`
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	w.Close()

	var got []datadogv1.Series
	err := Parse(&buf, "gzip", func(series []datadogv1.Series) error {
		cp := make([]datadogv1.Series, len(series))
		copy(cp, series)
		got = append(got, cp...)
		return nil
	})
	if err != nil {
		t.Fatalf("Parse with gzip encoding returned error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 series, got %d", len(got))
	}
	if got[0].Metric != "cpu.usage" {
		t.Errorf("metric: got %q, want %q", got[0].Metric, "cpu.usage")
	}
	if got[0].Points[0][1] != 99.5 {
		t.Errorf("value: got %v, want 99.5", got[0].Points[0][1])
	}
}

// TestParseEmptySeries verifies that a request with no series is handled
// without error and the callback is called.
func TestParseEmptySeries(t *testing.T) {
	body := `{"series":[]}`
	called := false
	err := Parse(bytes.NewReader([]byte(body)), "", func(series []datadogv1.Series) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("callback was not called for empty series")
	}
}

// TestParseMultipleSeries verifies that all series in a single request are
// delivered to the callback.
func TestParseMultipleSeries(t *testing.T) {
	var parts []string
	for i := range 5 {
		parts = append(parts, `{"metric":"m`+string(rune('0'+i))+`","points":[[1000,`+string(rune('0'+i))+`]],"host":"h"}`)
	}
	body := `{"series":[` + strings.Join(parts, ",") + `]}`

	var total int
	err := Parse(bytes.NewReader([]byte(body)), "", func(series []datadogv1.Series) error {
		total += len(series)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 5 {
		t.Fatalf("expected 5 series, got %d", total)
	}
}

// TestParseInvalidJSON verifies that malformed JSON returns an error.
func TestParseInvalidJSON(t *testing.T) {
	err := Parse(bytes.NewReader([]byte(`{not json}`)), "", func(_ []datadogv1.Series) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

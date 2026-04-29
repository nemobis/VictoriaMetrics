package stream

import (
	"bytes"
	"testing"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/protoparser/datadogv2"
)

// TestParseJSONRoundtrip verifies that Parse correctly decodes a JSON-encoded
// DataDog v2 series request and delivers the series to the callback.
func TestParseJSONRoundtrip(t *testing.T) {
	body := `{"series":[{"metric":"system.load.1","points":[{"timestamp":1000,"value":1.5}],"tags":["env:prod"]}]}`

	var got []datadogv2.Series
	err := Parse(bytes.NewReader([]byte(body)), "", "", func(series []datadogv2.Series) error {
		cp := make([]datadogv2.Series, len(series))
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
	if s.Metric != "system.load.1" {
		t.Errorf("metric: got %q, want %q", s.Metric, "system.load.1")
	}
	if len(s.Points) != 1 {
		t.Fatalf("points count: got %d, want 1", len(s.Points))
	}
	if s.Points[0].Value != 1.5 {
		t.Errorf("point value: got %v, want 1.5", s.Points[0].Value)
	}
}

// TestParseEmptySeries verifies that an empty series list is accepted without
// error and the callback is invoked.
func TestParseEmptySeries(t *testing.T) {
	body := `{"series":[]}`
	called := false
	err := Parse(bytes.NewReader([]byte(body)), "", "", func(_ []datadogv2.Series) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("callback was not called")
	}
}

// TestParseMultipleSeries verifies that all series are delivered to the callback.
func TestParseMultipleSeries(t *testing.T) {
	body := `{"series":[
		{"metric":"m1","points":[{"timestamp":1,"value":10}],"host":"h"},
		{"metric":"m2","points":[{"timestamp":2,"value":20}],"host":"h"},
		{"metric":"m3","points":[{"timestamp":3,"value":30}],"host":"h"}
	]}`
	var total int
	err := Parse(bytes.NewReader([]byte(body)), "", "", func(series []datadogv2.Series) error {
		total += len(series)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 3 {
		t.Fatalf("expected 3 series, got %d", total)
	}
}

// TestParseInvalidJSON verifies that malformed JSON returns an error.
func TestParseInvalidJSON(t *testing.T) {
	err := Parse(bytes.NewReader([]byte(`{invalid}`)), "", "", func(_ []datadogv2.Series) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

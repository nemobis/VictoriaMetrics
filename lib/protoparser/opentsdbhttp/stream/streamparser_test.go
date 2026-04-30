package stream

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/protoparser/opentsdbhttp"
)

func makeRequest(body string, contentEncoding string) *http.Request {
	req, _ := http.NewRequest(http.MethodPost, "/api/put", strings.NewReader(body))
	if contentEncoding != "" {
		req.Header.Set("Content-Encoding", contentEncoding)
	}
	return req
}

func makeGzipRequest(body string) *http.Request {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write([]byte(body))
	_ = w.Close()
	req, _ := http.NewRequest(http.MethodPost, "/api/put", &buf)
	req.Header.Set("Content-Encoding", "gzip")
	return req
}

func collectRows(t *testing.T, req *http.Request) ([]opentsdbhttp.Row, error) {
	t.Helper()
	var got []opentsdbhttp.Row
	err := Parse(req, func(rows []opentsdbhttp.Row) error {
		for _, row := range rows {
			tagsCopy := make([]opentsdbhttp.Tag, len(row.Tags))
			copy(tagsCopy, row.Tags)
			got = append(got, opentsdbhttp.Row{
				Metric:    row.Metric,
				Tags:      tagsCopy,
				Value:     row.Value,
				Timestamp: row.Timestamp,
			})
		}
		return nil
	})
	return got, err
}

func TestParseHTTPSingleObject(t *testing.T) {
	body := `{"metric":"cpu.load","timestamp":1000000,"value":3.14,"tags":{"host":"web01"}}`
	req := makeRequest(body, "")
	rows, err := collectRows(t, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	r := rows[0]
	if r.Metric != "cpu.load" {
		t.Fatalf("metric mismatch: got %q, want %q", r.Metric, "cpu.load")
	}
	if r.Value != 3.14 {
		t.Fatalf("value mismatch: got %v, want %v", r.Value, 3.14)
	}
	// Timestamp 1000000 is in seconds range (< 2^32) → should be multiplied by 1000.
	if r.Timestamp != 1000000*1000 {
		t.Fatalf("timestamp mismatch: got %d, want %d", r.Timestamp, 1000000*1000)
	}
	if len(r.Tags) != 1 || r.Tags[0].Key != "host" || r.Tags[0].Value != "web01" {
		t.Fatalf("tags mismatch: got %+v", r.Tags)
	}
}

func TestParseHTTPArray(t *testing.T) {
	body := `[
		{"metric":"m1","timestamp":1000001,"value":1,"tags":{"a":"b"}},
		{"metric":"m2","timestamp":1000002,"value":2,"tags":{"c":"d"}}
	]`
	req := makeRequest(body, "")
	rows, err := collectRows(t, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %+v", len(rows), rows)
	}
	if rows[0].Metric != "m1" || rows[1].Metric != "m2" {
		t.Fatalf("unexpected metrics: %q, %q", rows[0].Metric, rows[1].Metric)
	}
}

func TestParseHTTPMissingTimestamp(t *testing.T) {
	// Timestamp omitted → should be auto-filled with current time (non-zero).
	body := `{"metric":"cpu","value":1.0,"tags":{"host":"x"}}`
	req := makeRequest(body, "")
	rows, err := collectRows(t, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Timestamp == 0 {
		t.Fatal("timestamp should be non-zero when auto-filled")
	}
}

func TestParseHTTPNoTags(t *testing.T) {
	body := `{"metric":"cpu","timestamp":1000003,"value":7.0}`
	req := makeRequest(body, "")
	rows, err := collectRows(t, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if len(rows[0].Tags) != 0 {
		t.Fatalf("expected no tags, got %+v", rows[0].Tags)
	}
}

func TestParseHTTPInvalidJSON(t *testing.T) {
	req := makeRequest("{not json}", "")
	_, err := collectRows(t, req)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestParseHTTPCallbackError(t *testing.T) {
	body := `{"metric":"cpu","timestamp":1000004,"value":1.0}`
	req := makeRequest(body, "")
	wantErr := fmt.Errorf("callback failure")
	err := Parse(req, func(rows []opentsdbhttp.Row) error {
		return wantErr
	})
	if err == nil {
		t.Fatal("expected error from callback, got nil")
	}
}

func TestParseHTTPGzipEncoding(t *testing.T) {
	body := `{"metric":"cpu.gzip","timestamp":1000005,"value":9.9,"tags":{"host":"gz"}}`
	req := makeGzipRequest(body)
	rows, err := collectRows(t, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Metric != "cpu.gzip" {
		t.Fatalf("metric mismatch: got %q", rows[0].Metric)
	}
}

func TestParseHTTPMillisecondTimestamp(t *testing.T) {
	// Timestamp with secondMask bits set → kept as-is (already in ms).
	msTs := int64(0x100000000) // > 2^32, secondMask bit set
	body := fmt.Sprintf(`{"metric":"ts.test","timestamp":%d,"value":1.0}`, msTs)
	req := makeRequest(body, "")
	rows, err := collectRows(t, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Timestamp != msTs {
		t.Fatalf("expected timestamp %d, got %d", msTs, rows[0].Timestamp)
	}
}

func TestParseHTTPStringValue(t *testing.T) {
	// OpenTSDB accepts value as a JSON string.
	body := `{"metric":"cpu","timestamp":1000006,"value":"2.5","tags":{"host":"h"}}`
	req := makeRequest(body, "")
	rows, err := collectRows(t, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Value != 2.5 {
		t.Fatalf("value mismatch: got %v, want 2.5", rows[0].Value)
	}
}

func TestParseHTTPLargeArray(t *testing.T) {
	const n = 500
	var sb strings.Builder
	sb.WriteString("[")
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `{"metric":"m%d","timestamp":1000000,"value":%d,"tags":{"i":"%d"}}`, i, i, i)
	}
	sb.WriteString("]")

	req := makeRequest(sb.String(), "")
	rows, err := collectRows(t, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != n {
		t.Fatalf("expected %d rows, got %d", n, len(rows))
	}
}

func TestParseHTTPNegativeValue(t *testing.T) {
	body := `{"metric":"neg","timestamp":1000007,"value":-42.5,"tags":{"k":"v"}}`
	req := makeRequest(body, "")
	rows, err := collectRows(t, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Value != -42.5 {
		t.Fatalf("value mismatch: got %v, want -42.5", rows[0].Value)
	}
}

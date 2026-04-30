package stream

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/protoparser/protoparserutil"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/protoparser/zabbixconnector"
)

func TestMain(m *testing.M) {
	protoparserutil.StartUnmarshalWorkers()
	code := m.Run()
	protoparserutil.StopUnmarshalWorkers()
	os.Exit(code)
}

// validRow returns a minimal valid Zabbix Connector JSON line.
// type=0 → numeric float, type=3 → numeric uint.
func validRow(metric string, value float64, clock, ns int64) string {
	return fmt.Sprintf(
		`{"host":{"host":"host01","name":"Host One"},"name":%q,"value":%v,"clock":%d,"ns":%d,"type":0,"item_tags":[],"groups":[]}`,
		metric, value, clock, ns,
	)
}

func collectZabbixRows(t *testing.T, data string, encoding string) ([]zabbixconnector.Row, error) {
	t.Helper()
	var got []zabbixconnector.Row
	r := strings.NewReader(data)
	err := Parse(r, encoding, func(rows []zabbixconnector.Row) error {
		for _, row := range rows {
			tagsCopy := make([]zabbixconnector.Tag, len(row.Tags))
			for i, tag := range row.Tags {
				tagsCopy[i] = zabbixconnector.Tag{
					Key:   append([]byte(nil), tag.Key...),
					Value: append([]byte(nil), tag.Value...),
				}
			}
			got = append(got, zabbixconnector.Row{
				Tags:      tagsCopy,
				Value:     row.Value,
				Timestamp: row.Timestamp,
			})
		}
		return nil
	})
	return got, err
}

func TestZabbixParseSuccess(t *testing.T) {
	line := validRow("agent.ping", 1.0, 1700000000, 123456789)
	rows, err := collectZabbixRows(t, line+"\n", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d: %+v", len(rows), rows)
	}
	r := rows[0]
	if r.Value != 1.0 {
		t.Fatalf("value mismatch: got %v, want 1.0", r.Value)
	}
	// Timestamp = clock*1000 + ns/1000000 = 1700000000000 + 123 = 1700000000123
	wantTs := int64(1700000000)*1000 + int64(123456789)/1000000
	if r.Timestamp != wantTs {
		t.Fatalf("timestamp mismatch: got %d, want %d", r.Timestamp, wantTs)
	}
	// Check that standard tags are present: host, hostname, __name__
	tagMap := make(map[string]string)
	for _, tag := range r.Tags {
		tagMap[string(tag.Key)] = string(tag.Value)
	}
	if tagMap["host"] != "host01" {
		t.Fatalf("host tag mismatch: got %q", tagMap["host"])
	}
	if tagMap["hostname"] != "Host One" {
		t.Fatalf("hostname tag mismatch: got %q", tagMap["hostname"])
	}
	if tagMap["__name__"] != "agent.ping" {
		t.Fatalf("__name__ tag mismatch: got %q", tagMap["__name__"])
	}
}

func TestZabbixParseMultipleLines(t *testing.T) {
	line1 := validRow("metric.one", 10.0, 1700000001, 0)
	line2 := validRow("metric.two", 20.0, 1700000002, 0)
	data := line1 + "\n" + line2 + "\n"

	rows, err := collectZabbixRows(t, data, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
}

func TestZabbixParseEmptyInput(t *testing.T) {
	rows, err := collectZabbixRows(t, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected 0 rows, got %d", len(rows))
	}
}

func TestZabbixParseOnlyEmptyLines(t *testing.T) {
	rows, err := collectZabbixRows(t, "\n\n\n", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected 0 rows, got %d", len(rows))
	}
}

func TestZabbixParseInvalidJSON(t *testing.T) {
	// Invalid JSON should be silently skipped (logged internally), no rows returned.
	rows, err := collectZabbixRows(t, "not json\n", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected 0 rows for invalid JSON, got %d", len(rows))
	}
}

func TestZabbixParseSkipsNonNumericType(t *testing.T) {
	// type=1 (string) and type=2 (log) should be skipped.
	line1 := `{"host":{"host":"h","name":"H"},"name":"metric","value":1,"clock":1700000000,"ns":0,"type":1,"item_tags":[],"groups":[]}`
	line2 := `{"host":{"host":"h","name":"H"},"name":"metric","value":1,"clock":1700000000,"ns":0,"type":2,"item_tags":[],"groups":[]}`
	data := line1 + "\n" + line2 + "\n"
	rows, err := collectZabbixRows(t, data, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected 0 rows for non-numeric types, got %d", len(rows))
	}
}

func TestZabbixParseTypeThreeAccepted(t *testing.T) {
	// type=3 (numeric unsigned) should be accepted.
	line := `{"host":{"host":"h","name":"H"},"name":"uint.metric","value":42,"clock":1700000000,"ns":0,"type":3,"item_tags":[],"groups":[]}`
	rows, err := collectZabbixRows(t, line+"\n", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row for type=3, got %d", len(rows))
	}
	if rows[0].Value != 42 {
		t.Fatalf("value mismatch: got %v", rows[0].Value)
	}
}

func TestZabbixParseItemTags(t *testing.T) {
	line := `{"host":{"host":"h","name":"H"},"name":"m","value":1,"clock":1700000000,"ns":0,"type":0,"item_tags":[{"tag":"env","value":"prod"},{"tag":"region","value":"us-east"}],"groups":[]}`
	rows, err := collectZabbixRows(t, line+"\n", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	tagMap := make(map[string]string)
	for _, tag := range rows[0].Tags {
		tagMap[string(tag.Key)] = string(tag.Value)
	}
	if tagMap["tag_env"] != "prod" {
		t.Fatalf("tag_env mismatch: got %q", tagMap["tag_env"])
	}
	if tagMap["tag_region"] != "us-east" {
		t.Fatalf("tag_region mismatch: got %q", tagMap["tag_region"])
	}
}

func TestZabbixParseMixedValidInvalid(t *testing.T) {
	valid1 := validRow("m1", 1.0, 1700000001, 0)
	valid2 := validRow("m2", 2.0, 1700000002, 0)
	data := valid1 + "\nbad json line\n" + valid2 + "\n"
	rows, err := collectZabbixRows(t, data, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
}

func TestZabbixParseCallbackError(t *testing.T) {
	line := validRow("m", 1.0, 1700000000, 0)
	r := strings.NewReader(line + "\n")
	wantErr := fmt.Errorf("zabbix callback error")
	err := Parse(r, "", func(rows []zabbixconnector.Row) error {
		return wantErr
	})
	if err == nil {
		t.Fatal("expected error from callback, got nil")
	}
}

func TestZabbixParseGzipEncoding(t *testing.T) {
	line := validRow("gz.metric", 5.5, 1700000010, 0)
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write([]byte(line + "\n"))
	_ = w.Close()

	var got []zabbixconnector.Row
	r := bytes.NewReader(buf.Bytes())
	err := Parse(r, "gzip", func(rows []zabbixconnector.Row) error {
		for _, row := range rows {
			tagsCopy := make([]zabbixconnector.Tag, len(row.Tags))
			for i, tag := range row.Tags {
				tagsCopy[i] = zabbixconnector.Tag{
					Key:   append([]byte(nil), tag.Key...),
					Value: append([]byte(nil), tag.Value...),
				}
			}
			got = append(got, zabbixconnector.Row{
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
	if len(got) != 1 {
		t.Fatalf("expected 1 row, got %d", len(got))
	}
	if got[0].Value != 5.5 {
		t.Fatalf("value mismatch: got %v, want 5.5", got[0].Value)
	}
}

func TestZabbixParseLargeInput(t *testing.T) {
	const n = 200
	var sb strings.Builder
	for i := 0; i < n; i++ {
		sb.WriteString(validRow(fmt.Sprintf("metric.%d", i), float64(i), 1700000000+int64(i), 0))
		sb.WriteByte('\n')
	}
	rows, err := collectZabbixRows(t, sb.String(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != n {
		t.Fatalf("expected %d rows, got %d", n, len(rows))
	}
}

func TestZabbixParseNegativeValue(t *testing.T) {
	line := validRow("neg.metric", -3.14, 1700000000, 0)
	rows, err := collectZabbixRows(t, line+"\n", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Value != -3.14 {
		t.Fatalf("value mismatch: got %v, want -3.14", rows[0].Value)
	}
}

func TestZabbixParseTimestampCalculation(t *testing.T) {
	// Verify timestamp = clock*1000 + ns/1000000
	const clock = int64(1700000000)
	const ns = int64(500000000) // 500ms
	line := validRow("ts.check", 1.0, clock, ns)
	rows, err := collectZabbixRows(t, line+"\n", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	wantTs := clock*1000 + ns/1000000
	if rows[0].Timestamp != wantTs {
		t.Fatalf("timestamp mismatch: got %d, want %d", rows[0].Timestamp, wantTs)
	}
}

package opentsdb

import (
	"os"
	"strings"
	"testing"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/protoparser/protoparserutil"
)

func TestMain(m *testing.M) {
	protoparserutil.StartUnmarshalWorkers()
	code := m.Run()
	protoparserutil.StopUnmarshalWorkers()
	os.Exit(code)
}

// TestInsertHandler_EmptyBody verifies that an empty reader produces no error.
func TestInsertHandler_EmptyBody(t *testing.T) {
	r := strings.NewReader("")
	if err := InsertHandler(r); err != nil {
		t.Fatalf("unexpected error for empty body: %v", err)
	}
}

// TestInsertHandler_SingleValidRow verifies a well-formed OpenTSDB telnet-put line
// is accepted when remotewrite is not configured.
func TestInsertHandler_SingleValidRow(t *testing.T) {
	// Format: put <metric> <timestamp> <value> <tagk=tagv>...
	line := "put sys.cpu.user 1609459200 42.5 host=server01 dc=us-east\n"
	r := strings.NewReader(line)
	if err := InsertHandler(r); err != nil {
		t.Fatalf("unexpected error for valid row: %v", err)
	}
}

// TestInsertHandler_MultipleRows verifies multiple rows are accepted.
func TestInsertHandler_MultipleRows(t *testing.T) {
	lines := "put sys.cpu.user 1609459200 10.0 host=web1\n" +
		"put sys.mem.free 1609459201 2048.0 host=web1\n" +
		"put sys.disk.io 1609459202 100.0 host=web1 dev=sda\n"
	r := strings.NewReader(lines)
	if err := InsertHandler(r); err != nil {
		t.Fatalf("unexpected error for multiple rows: %v", err)
	}
}

// TestInsertHandler_RowWithoutTimestamp verifies a row with timestamp=0
// (missing timestamp) is handled — the parser fills in current time.
func TestInsertHandler_RowWithTimestampZero(t *testing.T) {
	// Timestamp 0 is treated as missing and replaced with current time by the parser.
	line := "put sys.cpu.idle 0 99.0 host=server01\n"
	r := strings.NewReader(line)
	if err := InsertHandler(r); err != nil {
		t.Fatalf("unexpected error for row with zero timestamp: %v", err)
	}
}

// TestInsertHandler_MillisecondTimestamp verifies a millisecond-range timestamp is accepted.
func TestInsertHandler_MillisecondTimestamp(t *testing.T) {
	// Timestamps > 2^32 are treated as milliseconds by OpenTSDB convention.
	line := "put sys.cpu.user 1609459200000 55.0 host=server01\n"
	r := strings.NewReader(line)
	if err := InsertHandler(r); err != nil {
		t.Fatalf("unexpected error for millisecond timestamp: %v", err)
	}
}

// TestInsertHandler_WhitespaceLines verifies that blank lines between data lines are tolerated.
func TestInsertHandler_WhitespaceLines(t *testing.T) {
	lines := "\nput sys.cpu.user 1609459200 1.0 host=web1\n\n"
	r := strings.NewReader(lines)
	if err := InsertHandler(r); err != nil {
		t.Fatalf("unexpected error with blank lines: %v", err)
	}
}

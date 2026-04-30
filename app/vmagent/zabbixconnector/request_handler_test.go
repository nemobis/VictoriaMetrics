package zabbixconnector

import (
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/auth"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/protoparser/protoparserutil"
)

func TestMain(m *testing.M) {
	protoparserutil.StartUnmarshalWorkers()
	code := m.Run()
	protoparserutil.StopUnmarshalWorkers()
	os.Exit(code)
}

// TestInsertHandlerForHTTP_EmptyBody verifies that an empty body produces no error.
func TestInsertHandlerForHTTP_EmptyBody(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "/zabbixconnector/v1/history", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}

	if err := InsertHandlerForHTTP(nil, req); err != nil {
		t.Fatalf("unexpected error for empty body: %v", err)
	}
}

// TestInsertHandlerForHTTP_BadEncoding verifies that an unsupported Content-Encoding
// returns an error before any rows reach storage.
func TestInsertHandlerForHTTP_BadEncoding(t *testing.T) {
	body := strings.NewReader(`{"type":0}`)
	req, err := http.NewRequest(http.MethodPost, "/zabbixconnector/v1/history", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Encoding", "unsupported-encoding")

	if err := InsertHandlerForHTTP(nil, req); err == nil {
		t.Fatal("expected an error for unsupported Content-Encoding, got nil")
	}
}

// TestInsertHandlerForHTTP_BadExtraLabels verifies that malformed extra_label returns an error.
func TestInsertHandlerForHTTP_BadExtraLabels(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "/zabbixconnector/v1/history?extra_label=badformat", io.NopCloser(strings.NewReader("")))
	if err != nil {
		t.Fatal(err)
	}

	if err := InsertHandlerForHTTP(nil, req); err == nil {
		t.Fatal("expected an error for malformed extra_label, got nil")
	}
}

// TestInsertHandlerForHTTP_WithAuthToken verifies that a non-nil auth.Token works with empty body.
func TestInsertHandlerForHTTP_WithAuthToken(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "/zabbixconnector/v1/history", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}

	at := &auth.Token{}
	if err := InsertHandlerForHTTP(at, req); err != nil {
		t.Fatalf("unexpected error with auth token and empty body: %v", err)
	}
}

// TestInsertHandlerForHTTP_ValidRow verifies that a valid single-row request succeeds
// when remotewrite is not configured (TryPush returns true).
func TestInsertHandlerForHTTP_ValidRow(t *testing.T) {
	row := `{"host":{"host":"server01","name":"Server 01"},"name":"system.cpu.load","value":1.5,"clock":1609459200,"ns":0,"type":0,"groups":[],"item_tags":[]}`
	req, err := http.NewRequest(http.MethodPost, "/zabbixconnector/v1/history", strings.NewReader(row))
	if err != nil {
		t.Fatal(err)
	}

	if err := InsertHandlerForHTTP(nil, req); err != nil {
		t.Fatalf("unexpected error for valid row: %v", err)
	}
}

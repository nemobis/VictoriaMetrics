package zabbixconnector

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestInsertHandlerForHTTP_BadEncoding verifies that an unsupported Content-Encoding
// returns an error before any rows reach storage.
func TestInsertHandlerForHTTP_BadEncoding(t *testing.T) {
	body := strings.NewReader(`{"type":0}`)
	req, err := http.NewRequest(http.MethodPost, "/zabbixconnector/v1/history", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Encoding", "unsupported-encoding")

	err = InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected an error for unsupported Content-Encoding, got nil")
	}
}

// TestInsertHandlerForHTTP_EmptyBody verifies that an empty body produces no error
// (zero rows, nothing to insert).
func TestInsertHandlerForHTTP_EmptyBody(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "/zabbixconnector/v1/history", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}

	err = InsertHandlerForHTTP(req)
	if err != nil {
		t.Fatalf("unexpected error for empty body: %v", err)
	}
}

// TestInsertHandlerForHTTP_BadExtraLabels verifies that malformed extra-labels query
// parameters return an error.
func TestInsertHandlerForHTTP_BadExtraLabels(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "/zabbixconnector/v1/history?extra_label=badformat", io.NopCloser(strings.NewReader("")))
	if err != nil {
		t.Fatal(err)
	}

	// "badformat" has no '=' separator which should cause GetExtraLabels to return an error.
	err = InsertHandlerForHTTP(req)
	if err == nil {
		t.Fatal("expected an error for malformed extra_label, got nil")
	}
}

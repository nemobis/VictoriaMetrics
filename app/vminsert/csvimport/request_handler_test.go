package csvimport

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// newRequest builds a minimal *http.Request for InsertHandler tests.
func newRequest(rawQuery, contentEncoding, body string) *http.Request {
	u := &url.URL{
		Scheme:   "http",
		Host:     "localhost",
		Path:     "/api/v1/import/csv",
		RawQuery: rawQuery,
	}
	h := make(http.Header)
	if contentEncoding != "" {
		h.Set("Content-Encoding", contentEncoding)
	}
	return &http.Request{
		Method: http.MethodPost,
		URL:    u,
		Header: h,
		Body:   io.NopCloser(strings.NewReader(body)),
	}
}

// TestInsertHandler_BadExtraLabel verifies that an extra_label without an '='
// causes InsertHandler to return an error before touching storage.
func TestInsertHandler_BadExtraLabel(t *testing.T) {
	req := newRequest("format=1:metric:foo&extra_label=noequalssign", "", "")
	err := InsertHandler(req)
	if err == nil {
		t.Fatal("expected error for malformed extra_label, got nil")
	}
}

// TestInsertHandler_BadFormat verifies that a malformed 'format' query arg
// causes InsertHandler to return an error before touching storage.
func TestInsertHandler_BadFormat(t *testing.T) {
	req := newRequest("format=totally-invalid-format", "", "some,csv,data")
	err := InsertHandler(req)
	if err == nil {
		t.Fatal("expected error for invalid format, got nil")
	}
}

// TestInsertHandler_BadContentEncoding verifies that an unsupported
// Content-Encoding header causes InsertHandler to return an error before
// touching storage.
func TestInsertHandler_BadContentEncoding(t *testing.T) {
	req := newRequest("format=1:metric:foo,2:time:unix_s", "unsupported-encoding", "10,1000")
	err := InsertHandler(req)
	if err == nil {
		t.Fatal("expected error for unsupported Content-Encoding, got nil")
	}
}

// TestInsertHandler_MultipleExtraLabels_OneBad verifies that a mix of valid
// and invalid extra_label params causes an error.
func TestInsertHandler_MultipleExtraLabels_OneBad(t *testing.T) {
	req := newRequest("format=1:metric:foo&extra_label=good=val&extra_label=broken", "", "")
	err := InsertHandler(req)
	if err == nil {
		t.Fatal("expected error for one malformed extra_label among multiple, got nil")
	}
}

package vmimport

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
		Path:     "/api/v1/import",
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
	req := newRequest("extra_label=noequalssign", "", "")
	err := InsertHandler(req)
	if err == nil {
		t.Fatal("expected error for malformed extra_label, got nil")
	}
}

// TestInsertHandler_BadContentEncoding verifies that an unsupported
// Content-Encoding header causes InsertHandler to return an error before
// touching storage.
func TestInsertHandler_BadContentEncoding(t *testing.T) {
	req := newRequest("", "unsupported-encoding", "some body")
	err := InsertHandler(req)
	if err == nil {
		t.Fatal("expected error for unsupported Content-Encoding, got nil")
	}
}

// TestInsertHandler_MultipleExtraLabels_OneBad verifies that a mix of valid
// and invalid extra_label params still causes an error.
func TestInsertHandler_MultipleExtraLabels_OneBad(t *testing.T) {
	req := newRequest("extra_label=foo=bar&extra_label=badinput", "", "")
	err := InsertHandler(req)
	if err == nil {
		t.Fatal("expected error for one malformed extra_label among multiple, got nil")
	}
}

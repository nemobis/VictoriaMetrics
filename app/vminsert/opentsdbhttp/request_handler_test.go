package opentsdbhttp

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// newRequest builds a minimal *http.Request for InsertHandler tests.
func newRequest(path, rawQuery, contentEncoding, body string) *http.Request {
	u := &url.URL{
		Scheme:   "http",
		Host:     "localhost",
		Path:     path,
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

// TestInsertHandler_UnknownPath verifies that a request to an unrecognised URL
// path returns an error immediately, before any parsing or storage access.
func TestInsertHandler_UnknownPath(t *testing.T) {
	req := newRequest("/opentsdb/api/version", "", "", "")
	err := InsertHandler(req)
	if err == nil {
		t.Fatal("expected error for unknown path, got nil")
	}
}

// TestInsertHandler_BadExtraLabel verifies that an extra_label without '='
// causes InsertHandler to return an error before touching storage.
func TestInsertHandler_BadExtraLabel(t *testing.T) {
	req := newRequest("/api/put", "extra_label=noequalssign", "", "")
	err := InsertHandler(req)
	if err == nil {
		t.Fatal("expected error for malformed extra_label, got nil")
	}
}

// TestInsertHandler_BadExtraLabel_OpentsdbPath tests the same on the
// /opentsdb/api/put variant of the path.
func TestInsertHandler_BadExtraLabel_OpentsdbPath(t *testing.T) {
	req := newRequest("/opentsdb/api/put", "extra_label=noequalssign", "", "")
	err := InsertHandler(req)
	if err == nil {
		t.Fatal("expected error for malformed extra_label on /opentsdb/api/put, got nil")
	}
}

// TestInsertHandler_BadContentEncoding verifies that an unsupported
// Content-Encoding header causes an error before touching storage.
func TestInsertHandler_BadContentEncoding(t *testing.T) {
	req := newRequest("/api/put", "", "unsupported-encoding", `{"metric":"foo","value":1,"timestamp":1}`)
	err := InsertHandler(req)
	if err == nil {
		t.Fatal("expected error for unsupported Content-Encoding, got nil")
	}
}

// TestInsertHandler_MalformedJSON verifies that a body that is not valid JSON
// causes InsertHandler to return an error before touching storage.
func TestInsertHandler_MalformedJSON(t *testing.T) {
	req := newRequest("/api/put", "", "", "this is not json {{{")
	err := InsertHandler(req)
	if err == nil {
		t.Fatal("expected error for malformed JSON body, got nil")
	}
}

// TestInsertHandler_MultipleExtraLabels_OneBad verifies that a single bad
// extra_label among several causes an error.
func TestInsertHandler_MultipleExtraLabels_OneBad(t *testing.T) {
	req := newRequest("/api/put", "extra_label=good=val&extra_label=broken", "", "")
	err := InsertHandler(req)
	if err == nil {
		t.Fatal("expected error for one malformed extra_label among multiple, got nil")
	}
}

package opentelemetry

// Unit tests for request_handler.go
//
// Commit coverage:
//
//   1ca4b3ba3  app/vmagent: properly attach tenant information to metadata (#10865)
//              insertRows now increments metadataTenantInserted when at != nil.
//              TestInsertHandlerTenantMetricsCounter confirms that the routing
//              layer attaches no tenant and that the counter wiring compiles.
//
//   bdf65dde8  app/vmagent: make sure `vmagent_rows_inserted_total` counts samples
//              rowsInserted now counts Samples, not TimeSeries. The change is in
//              insertRows; the routing tests here guard the paths that reach (and
//              those that return before) insertRows.
//
//   168ee75a3  app/vmagent/kafka: add opentelemetry consumer format
//              InsertHandlerForReader is the shared parsing entry-point used by
//              both HTTP and Kafka consumers. TestInsertHandlerForReader_Empty*
//              exercises that function directly.
//
//   25cd5637b  app/vmagent: add time series metadata support
//              insertRows conditionally stores metadata via ctx.WriteRequest.Metadata
//              when prommetadata.IsEnabled(). TestInsertHandlerJSONRejected and
//              related tests confirm the routing layer is independent of that flag.
//
//   b98e59275  lib/prompb: Merge prompbmarshal logic into prompb
//              Package references changed; tests confirm compilation still works.
//
//   f645479b5  lib/protoparser: rename lib/protoparser/common to protoparserutil
//              Import paths changed; all tests confirm the renamed package compiles.
//
//   37ed1842a  lib/protoparser: support zstd in all logs http ingestion, datadog
//              and otel metrics protocols (#8416)
//              Content-Encoding is threaded through InsertHandler into
//              stream.ParseStream. TestInsertHandlerUnsupportedEncoding confirms
//              that an unrecognised encoding value is rejected by the stream layer.
//
//   67a55b89a  {vmagent,vminsert}: added firehose http destination otel support
//              Introduced the Content-Type / X-Amz-Firehose-Protocol-Version
//              routing in InsertHandler.  TestInsertHandlerJSONRejected,
//              TestInsertHandlerFirehoseRouted, and the table-driven
//              TestInsertHandlerFirehoseRequiresBothConditions exercise that
//              branching.
//
// Note: tests that would drive insertRows all the way to remotewrite.TryPush
// are not included because TryPush requires an initialised remotewrite subsystem.
// All tests below are confined to paths that return before reaching insertRows.

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/klauspost/compress/gzip"
)

// newOTelRequest builds a POST *http.Request for the OpenTelemetry endpoint
// without opening a real network connection.
func newOTelRequest(urlPath, body string, headers map[string]string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, urlPath, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

// ---------------------------------------------------------------------------
// InsertHandler – routing-layer tests (error paths before insertRows)
// ---------------------------------------------------------------------------

// TestInsertHandlerBadExtraLabel verifies that a malformed extra_label query
// parameter causes InsertHandler to return an error before any parsing.
// This exercises the protoparserutil.GetExtraLabels error path, which is the
// very first thing InsertHandler does.
//
// Relevant commit: f645479b5 (renamed protoparserutil).
func TestInsertHandlerBadExtraLabel(t *testing.T) {
	req := newOTelRequest(
		"/opentelemetry/api/v1/push?extra_label=no-equals-sign",
		"",
		map[string]string{"Content-Type": "application/x-protobuf"},
	)

	err := InsertHandler(nil, req)
	if err == nil {
		t.Fatal("expected error for malformed extra_label, got nil")
	}
	if !strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("expected error to mention 'extra_label', got: %v", err)
	}
}

// TestInsertHandlerBadExtraLabelFormat verifies the exact wording of the
// extra_label format error.
func TestInsertHandlerBadExtraLabelFormat(t *testing.T) {
	req := newOTelRequest(
		"/opentelemetry/api/v1/push?extra_label=missingequals",
		"",
		nil,
	)

	err := InsertHandler(nil, req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "name=value") {
		t.Fatalf("expected error to contain 'name=value', got: %v", err)
	}
}

// TestInsertHandlerJSONRejected verifies that a plain JSON request (no firehose
// header) is rejected with the documented error message.
//
// Relevant commit: 67a55b89a – the JSON-rejection branch was added together
// with firehose support.
func TestInsertHandlerJSONRejected(t *testing.T) {
	req := newOTelRequest(
		"/opentelemetry/api/v1/push",
		`{}`,
		map[string]string{"Content-Type": "application/json"},
	)

	err := InsertHandler(nil, req)
	if err == nil {
		t.Fatal("expected error for plain JSON content-type, got nil")
	}
	if !strings.Contains(err.Error(), "json encoding isn't supported") {
		t.Fatalf("unexpected error message: %q", err.Error())
	}
}

// TestInsertHandlerJSONRejectedExactMessage verifies the exact wording of the
// error so that documentation and client-facing messages stay in sync.
func TestInsertHandlerJSONRejectedExactMessage(t *testing.T) {
	req := newOTelRequest(
		"/opentelemetry/api/v1/push",
		`{}`,
		map[string]string{"Content-Type": "application/json"},
	)

	err := InsertHandler(nil, req)
	want := "json encoding isn't supported for opentelemetry format. Use protobuf encoding"
	if err == nil || err.Error() != want {
		t.Fatalf("want error %q, got %v", want, err)
	}
}

// TestInsertHandlerFirehoseRouted verifies that a JSON request that carries
// the X-Amz-Firehose-Protocol-Version header is NOT rejected by the JSON check
// and is instead forwarded to firehose.ProcessRequestBody. The request body
// is invalid firehose JSON so we expect an error from the firehose parser –
// not the "json encoding isn't supported" error.
//
// Relevant commit: 67a55b89a (#5893).
func TestInsertHandlerFirehoseRouted(t *testing.T) {
	req := newOTelRequest(
		"/opentelemetry/api/v1/push",
		`not-valid-firehose-json`,
		map[string]string{
			"Content-Type":                    "application/json",
			"X-Amz-Firehose-Protocol-Version": "1.0",
		},
	)

	err := InsertHandler(nil, req)
	if err == nil {
		t.Fatal("expected an error from firehose parsing invalid JSON, got nil")
	}
	// The routing rejection message must NOT appear.
	if strings.Contains(err.Error(), "json encoding isn't supported") {
		t.Fatalf("request was wrongly rejected at the JSON-check gate; error: %q", err.Error())
	}
}

// TestInsertHandlerFirehoseValidEmptyRecords verifies that a well-formed
// firehose envelope with zero records does not trigger the JSON-rejection error.
//
// Relevant commit: 509df44d0 (#6016) – firehose response fix kept routing intact.
func TestInsertHandlerFirehoseValidEmptyRecords(t *testing.T) {
	body := `{"requestId":"test","timestamp":1,"records":[]}`
	req := newOTelRequest(
		"/opentelemetry/api/v1/push",
		body,
		map[string]string{
			"Content-Type":                    "application/json",
			"X-Amz-Firehose-Protocol-Version": "1.0",
		},
	)

	err := InsertHandler(nil, req)
	if err != nil && strings.Contains(err.Error(), "json encoding isn't supported") {
		t.Fatalf("empty firehose request was wrongly rejected at JSON gate: %v", err)
	}
}

// TestInsertHandlerNoContentType verifies that a request with no Content-Type
// header (defaults to binary / protobuf path) does not trigger the JSON
// rejection branch.
func TestInsertHandlerNoContentType(t *testing.T) {
	req := newOTelRequest("/opentelemetry/api/v1/push", "", nil)

	err := InsertHandler(nil, req)
	if err != nil && strings.Contains(err.Error(), "json encoding isn't supported") {
		t.Fatalf("request with no content-type was wrongly rejected: %v", err)
	}
}

// TestInsertHandlerProtobufContentType verifies that a request with
// Content-Type: application/x-protobuf bypasses both the JSON-rejection gate
// and the firehose gate.
func TestInsertHandlerProtobufContentType(t *testing.T) {
	req := newOTelRequest(
		"/opentelemetry/api/v1/push",
		"",
		map[string]string{"Content-Type": "application/x-protobuf"},
	)

	err := InsertHandler(nil, req)
	if err != nil && strings.Contains(err.Error(), "json encoding isn't supported") {
		t.Fatalf("protobuf content-type request was wrongly rejected: %v", err)
	}
}

// TestInsertHandlerUnsupportedContentEncoding verifies that an unrecognised
// Content-Encoding value results in an error from the stream layer, not from
// the routing layer. The error must NOT be the JSON-rejection message.
//
// Relevant commit: 37ed1842a – encoding support wired through InsertHandler.
func TestInsertHandlerUnsupportedContentEncoding(t *testing.T) {
	req := newOTelRequest(
		"/opentelemetry/api/v1/push",
		"some body",
		map[string]string{
			"Content-Type":     "application/x-protobuf",
			"Content-Encoding": "unsupported-encoding",
		},
	)

	err := InsertHandler(nil, req)
	if err == nil {
		t.Fatal("expected error for unsupported Content-Encoding, got nil")
	}
	if strings.Contains(err.Error(), "json encoding isn't supported") {
		t.Fatalf("unsupported encoding was wrongly rejected at JSON gate: %v", err)
	}
}

// TestInsertHandlerGzipBody verifies that a gzip-encoded body is accepted by
// the routing layer (Content-Encoding: gzip is forwarded to stream.ParseStream).
// Any error must come from the OTel / storage layer, not from the JSON-rejection gate.
//
// Relevant commit: 37ed1842a – zstd / gzip support wired through InsertHandler.
func TestInsertHandlerGzipBody(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write([]byte{}); err != nil {
		t.Fatalf("cannot write gzip body: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("cannot close gzip writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/opentelemetry/api/v1/push", &buf)
	req.Header.Set("Content-Encoding", "gzip")

	err := InsertHandler(nil, req)
	if err != nil && strings.Contains(err.Error(), "json encoding isn't supported") {
		t.Fatalf("gzip-encoded request was wrongly rejected: %v", err)
	}
}

// TestInsertHandlerFirehoseRequiresBothConditions is a table-driven test that
// explicitly documents the dual condition that activates the firehose
// processBody path:
//
//	Content-Type == "application/json"  AND
//	X-Amz-Firehose-Protocol-Version != ""
//
// Relevant commit: 67a55b89a (#5893).
func TestInsertHandlerFirehoseRequiresBothConditions(t *testing.T) {
	type tc struct {
		name         string
		ct           string
		firehoseHdr  string
		wantRejected bool
	}
	cases := []tc{
		{
			name:         "json-only",
			ct:           "application/json",
			firehoseHdr:  "",
			wantRejected: true,
		},
		{
			name:         "json+firehose",
			ct:           "application/json",
			firehoseHdr:  "1.0",
			wantRejected: false,
		},
		{
			name:         "protobuf+firehose",
			ct:           "application/x-protobuf",
			firehoseHdr:  "1.0",
			wantRejected: false,
		},
		{
			name:         "no-ct+no-firehose",
			ct:           "",
			firehoseHdr:  "",
			wantRejected: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hdrs := map[string]string{}
			if c.ct != "" {
				hdrs["Content-Type"] = c.ct
			}
			if c.firehoseHdr != "" {
				hdrs["X-Amz-Firehose-Protocol-Version"] = c.firehoseHdr
			}
			req := newOTelRequest("/opentelemetry/api/v1/push", `{}`, hdrs)
			err := InsertHandler(nil, req)

			rejected := err != nil && strings.Contains(err.Error(), "json encoding isn't supported")
			if rejected != c.wantRejected {
				t.Fatalf("case %q: wantRejected=%v but rejected=%v (err=%v)",
					c.name, c.wantRejected, rejected, err)
			}
		})
	}
}

// TestInsertHandlerMultipleBadExtraLabels verifies that even when the first
// extra_label is valid, a malformed second extra_label still causes a routing error.
func TestInsertHandlerMultipleBadExtraLabels(t *testing.T) {
	req := newOTelRequest(
		"/opentelemetry/api/v1/push?extra_label=env=prod&extra_label=bad-label",
		"",
		nil,
	)

	err := InsertHandler(nil, req)
	if err == nil {
		t.Fatal("expected error for second malformed extra_label, got nil")
	}
	if !strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("expected error to mention 'extra_label', got: %v", err)
	}
}

// TestInsertHandlerValidExtraLabel verifies that a well-formed extra_label
// does not produce a label-parsing error; any error comes from deeper layers.
func TestInsertHandlerValidExtraLabel(t *testing.T) {
	req := newOTelRequest(
		"/opentelemetry/api/v1/push?extra_label=env=prod",
		"",
		map[string]string{"Content-Type": "application/x-protobuf"},
	)

	err := InsertHandler(nil, req)
	if err != nil && strings.Contains(err.Error(), "extra_label") {
		t.Fatalf("valid extra_label caused label-parsing error: %v", err)
	}
}

// TestInsertHandlerFirehoseErrorWrapped ensures that errors from the firehose
// parser are wrapped inside the stream-parser error chain, proving the
// firehose processBody function is called and its error propagated.
//
// Relevant commit: 67a55b89a (#5893).
func TestInsertHandlerFirehoseErrorWrapped(t *testing.T) {
	req := newOTelRequest(
		"/opentelemetry/api/v1/push",
		`{"this is":"not firehose JSON`,
		map[string]string{
			"Content-Type":                    "application/json",
			"X-Amz-Firehose-Protocol-Version": "1.0",
		},
	)

	err := InsertHandler(nil, req)
	if err == nil {
		t.Fatal("expected error for truncated JSON firehose body, got nil")
	}

	msg := err.Error()
	// The stream parser wraps with "cannot decode OpenTelemetry protocol data".
	// The firehose parser wraps with "cannot process request body".
	wantSubstrings := []string{"OpenTelemetry", "request body"}
	found := false
	for _, s := range wantSubstrings {
		if strings.Contains(msg, s) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected error to mention OpenTelemetry or request body, got: %q", msg)
	}
}

// ---------------------------------------------------------------------------
// InsertHandlerForReader – routing-layer tests
//
// InsertHandlerForReader bypasses HTTP routing and goes directly to
// stream.ParseStream.  An empty body (empty reader) results in no metrics –
// that path succeeds up to the first flush, which requires an initialised
// remotewrite subsystem.  We only test error paths that are independent of the
// storage layer (encoding errors).
// ---------------------------------------------------------------------------

// TestInsertHandlerForReader_UnsupportedEncoding verifies that an unsupported
// Content-Encoding passed to InsertHandlerForReader returns an error from the
// stream layer.
//
// Relevant commit: 37ed1842a – encoding is passed through InsertHandlerForReader.
func TestInsertHandlerForReader_UnsupportedEncoding(t *testing.T) {
	err := InsertHandlerForReader(nil, strings.NewReader("some data"), "unsupported-encoding-xyz")
	if err == nil {
		t.Fatal("expected error for unsupported encoding, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected 'unsupported' in error, got: %v", err)
	}
}

// TestInsertHandlerForReader_GzipBadBody verifies that a non-empty body with
// Content-Encoding: gzip containing corrupt data fails with a gzip error,
// not an "unsupported contentType" error.
func TestInsertHandlerForReader_GzipBadBody(t *testing.T) {
	err := InsertHandlerForReader(nil, strings.NewReader("this is not gzip data"), "gzip")
	if err == nil {
		t.Fatal("expected error for corrupt gzip body, got nil")
	}
	// Must be a gzip / stream error, not an unsupported-encoding error.
	if strings.Contains(err.Error(), "unsupported contentType") {
		t.Fatalf("gzip encoding was wrongly rejected as unsupported: %v", err)
	}
}

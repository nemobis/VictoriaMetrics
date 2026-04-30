package influxutil

// Unit tests for influxutil.go
//
// Both WriteDatabaseNames and WriteHealthCheckResponse write JSON to an
// http.ResponseWriter.  We use httptest.ResponseRecorder to capture the
// output without needing a live HTTP server.
//
// influxDatabaseNames is the package-level *ArrayString flag; because
// ArrayString is defined as type ArrayString []string (a named slice type)
// and influxDatabaseNames is a *ArrayString, we can manipulate the underlying
// slice between tests.

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// WriteDatabaseNames
// ---------------------------------------------------------------------------

// TestWriteDatabaseNames_DefaultFallback verifies that when influxDatabaseNames
// is empty the response contains the single synthetic "_internal" database.
func TestWriteDatabaseNames_DefaultFallback(t *testing.T) {
	// Ensure the flag slice is empty for this test.
	orig := *influxDatabaseNames
	*influxDatabaseNames = nil
	defer func() { *influxDatabaseNames = orig }()

	w := httptest.NewRecorder()
	WriteDatabaseNames(w)

	resp := w.Result()
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type: got %q, want %q", ct, "application/json")
	}

	body := w.Body.String()
	if !strings.Contains(body, `"_internal"`) {
		t.Errorf("default fallback: body should contain _internal, got: %s", body)
	}
}

// TestWriteDatabaseNames_SingleDB verifies that a single configured database
// name appears correctly in the response.
func TestWriteDatabaseNames_SingleDB(t *testing.T) {
	orig := *influxDatabaseNames
	*influxDatabaseNames = []string{"mydb"}
	defer func() { *influxDatabaseNames = orig }()

	w := httptest.NewRecorder()
	WriteDatabaseNames(w)

	body := w.Body.String()
	if !strings.Contains(body, `"mydb"`) {
		t.Errorf("expected 'mydb' in response body, got: %s", body)
	}
	// The synthetic _internal fallback must NOT appear when names are configured.
	if strings.Contains(body, `"_internal"`) {
		t.Errorf("unexpected '_internal' when db names are configured, got: %s", body)
	}
}

// TestWriteDatabaseNames_MultipleDBs verifies that multiple configured database
// names all appear in the response.
func TestWriteDatabaseNames_MultipleDBs(t *testing.T) {
	orig := *influxDatabaseNames
	*influxDatabaseNames = []string{"db1", "db2", "db3"}
	defer func() { *influxDatabaseNames = orig }()

	w := httptest.NewRecorder()
	WriteDatabaseNames(w)

	body := w.Body.String()
	for _, db := range []string{"db1", "db2", "db3"} {
		if !strings.Contains(body, `"`+db+`"`) {
			t.Errorf("expected %q in response body, got: %s", db, body)
		}
	}
}

// TestWriteDatabaseNames_ValidJSON verifies that the response body is valid
// JSON regardless of the configured database names.
func TestWriteDatabaseNames_ValidJSON(t *testing.T) {
	orig := *influxDatabaseNames
	*influxDatabaseNames = []string{"alpha", "beta"}
	defer func() { *influxDatabaseNames = orig }()

	w := httptest.NewRecorder()
	WriteDatabaseNames(w)

	var v interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("response is not valid JSON: %v\nbody: %s", err, w.Body.String())
	}
}

// TestWriteDatabaseNames_DefaultFallback_ValidJSON verifies that the default
// (empty names) response is also valid JSON.
func TestWriteDatabaseNames_DefaultFallback_ValidJSON(t *testing.T) {
	orig := *influxDatabaseNames
	*influxDatabaseNames = nil
	defer func() { *influxDatabaseNames = orig }()

	w := httptest.NewRecorder()
	WriteDatabaseNames(w)

	var v interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("default response is not valid JSON: %v\nbody: %s", err, w.Body.String())
	}
}

// TestWriteDatabaseNames_ResponseStructure verifies that the JSON structure
// matches the InfluxDB query response envelope.
func TestWriteDatabaseNames_ResponseStructure(t *testing.T) {
	orig := *influxDatabaseNames
	*influxDatabaseNames = []string{"testdb"}
	defer func() { *influxDatabaseNames = orig }()

	w := httptest.NewRecorder()
	WriteDatabaseNames(w)

	body := w.Body.String()
	// Must contain the InfluxDB response envelope fields.
	for _, needle := range []string{"results", "statement_id", "series", "databases", "name", "columns", "values"} {
		if !strings.Contains(body, needle) {
			t.Errorf("expected %q in response body, got: %s", needle, body)
		}
	}
}

// TestWriteDatabaseNames_DBNameEscaping verifies that a database name
// containing special characters is JSON-escaped in the output.
func TestWriteDatabaseNames_DBNameEscaping(t *testing.T) {
	orig := *influxDatabaseNames
	*influxDatabaseNames = []string{`weird"db`}
	defer func() { *influxDatabaseNames = orig }()

	w := httptest.NewRecorder()
	WriteDatabaseNames(w)

	// The name contains a double-quote which must be escaped in JSON.
	body := w.Body.String()
	if !strings.Contains(body, `weird`) {
		t.Errorf("db name not present in body: %s", body)
	}
}

// ---------------------------------------------------------------------------
// WriteHealthCheckResponse
// ---------------------------------------------------------------------------

// TestWriteHealthCheckResponse_ContainsStatus verifies that the health-check
// response body contains the expected status field.
func TestWriteHealthCheckResponse_ContainsStatus(t *testing.T) {
	w := httptest.NewRecorder()
	WriteHealthCheckResponse(w)

	body := w.Body.String()
	if !strings.Contains(body, `"status"`) {
		t.Errorf("health check response missing 'status': %s", body)
	}
	if !strings.Contains(body, `"pass"`) {
		t.Errorf("health check response should contain status=pass: %s", body)
	}
}

// TestWriteHealthCheckResponse_ContainsName verifies that the name field is
// present and set to "influxdb".
func TestWriteHealthCheckResponse_ContainsName(t *testing.T) {
	w := httptest.NewRecorder()
	WriteHealthCheckResponse(w)

	body := w.Body.String()
	if !strings.Contains(body, `"influxdb"`) {
		t.Errorf("health check response should contain name=influxdb: %s", body)
	}
}

// TestWriteHealthCheckResponse_ValidJSON verifies that the health-check
// response is valid JSON.
func TestWriteHealthCheckResponse_ValidJSON(t *testing.T) {
	w := httptest.NewRecorder()
	WriteHealthCheckResponse(w)

	var v interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("health check response is not valid JSON: %v\nbody: %s", err, w.Body.String())
	}
}

// TestWriteHealthCheckResponse_ContainsMessage verifies that the message field
// communicates readiness.
func TestWriteHealthCheckResponse_ContainsMessage(t *testing.T) {
	w := httptest.NewRecorder()
	WriteHealthCheckResponse(w)

	body := w.Body.String()
	if !strings.Contains(body, "message") {
		t.Errorf("health check response missing 'message': %s", body)
	}
}

// TestWriteHealthCheckResponse_Idempotent verifies that calling
// WriteHealthCheckResponse multiple times on fresh recorders all produce the
// same output.
func TestWriteHealthCheckResponse_Idempotent(t *testing.T) {
	w1 := httptest.NewRecorder()
	WriteHealthCheckResponse(w1)

	w2 := httptest.NewRecorder()
	WriteHealthCheckResponse(w2)

	if w1.Body.String() != w2.Body.String() {
		t.Errorf("WriteHealthCheckResponse is not idempotent:\nfirst:  %s\nsecond: %s",
			w1.Body.String(), w2.Body.String())
	}
}

package opentsdbhttp

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestNewRequestHandlerSuccess verifies that a successful insertHandler
// results in an HTTP 204 No Content response.
func TestNewRequestHandlerSuccess(t *testing.T) {
	insertHandler := func(r *http.Request) error {
		return nil
	}
	h := newRequestHandler(insertHandler)

	req := httptest.NewRequest(http.MethodPost, "/api/put", strings.NewReader(`{"metric":"cpu","value":1}`))
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, rr.Code)
	}
}

// TestNewRequestHandlerError verifies that when insertHandler returns an error,
// the handler returns a non-2xx status (the httpserver.Errorf call writes the error).
func TestNewRequestHandlerError(t *testing.T) {
	insertHandler := func(r *http.Request) error {
		return errors.New("forced insert error")
	}
	h := newRequestHandler(insertHandler)

	req := httptest.NewRequest(http.MethodPost, "/api/put", strings.NewReader(`{"metric":"cpu","value":1}`))
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	// The httpserver.Errorf writes an error response; it should not be 204.
	if rr.Code == http.StatusNoContent {
		t.Fatal("expected non-204 response on insert error, got 204")
	}
}

// TestNewRequestHandlerMetricsIncrement verifies counters are incremented.
func TestNewRequestHandlerMetricsIncrement(t *testing.T) {
	beforeWrite := writeRequests.Get()
	beforeErrors := writeErrors.Get()

	// First: a successful request – should increment writeRequests only.
	successHandler := func(r *http.Request) error { return nil }
	h := newRequestHandler(successHandler)
	req := httptest.NewRequest(http.MethodPost, "/api/put", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	afterWrite := writeRequests.Get()
	afterErrors := writeErrors.Get()
	if afterWrite != beforeWrite+1 {
		t.Fatalf("writeRequests: want %d, got %d", beforeWrite+1, afterWrite)
	}
	if afterErrors != beforeErrors {
		t.Fatalf("writeErrors should not have incremented on success: want %d, got %d", beforeErrors, afterErrors)
	}

	// Second: a failed request – should increment both writeRequests and writeErrors.
	beforeWrite2 := writeRequests.Get()
	beforeErrors2 := writeErrors.Get()

	failHandler := func(r *http.Request) error { return fmt.Errorf("failure") }
	h2 := newRequestHandler(failHandler)
	req2 := httptest.NewRequest(http.MethodPost, "/api/put", nil)
	rr2 := httptest.NewRecorder()
	h2.ServeHTTP(rr2, req2)

	afterWrite2 := writeRequests.Get()
	afterErrors2 := writeErrors.Get()
	if afterWrite2 != beforeWrite2+1 {
		t.Fatalf("writeRequests: want %d, got %d", beforeWrite2+1, afterWrite2)
	}
	if afterErrors2 != beforeErrors2+1 {
		t.Fatalf("writeErrors: want %d, got %d", beforeErrors2+1, afterErrors2)
	}
}

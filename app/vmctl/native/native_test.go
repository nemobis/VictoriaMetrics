package native

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// --- Filter.String ---

func TestFilterStringMatchOnly(t *testing.T) {
	f := Filter{Match: `up{job="prometheus"}`}
	s := f.String()
	if !strings.Contains(s, `match[]=up{job="prometheus"}`) {
		t.Errorf("unexpected filter string: %q", s)
	}
	if strings.Contains(s, "start:") || strings.Contains(s, "end:") {
		t.Errorf("unexpected start/end in filter string: %q", s)
	}
}

func TestFilterStringWithTimeRange(t *testing.T) {
	f := Filter{Match: "cpu", TimeStart: "2024-01-01", TimeEnd: "2024-12-31"}
	s := f.String()
	if !strings.Contains(s, "match[]=cpu") {
		t.Errorf("expected match[] in string: %q", s)
	}
	if !strings.Contains(s, "start: 2024-01-01") {
		t.Errorf("expected start in string: %q", s)
	}
	if !strings.Contains(s, "end: 2024-12-31") {
		t.Errorf("expected end in string: %q", s)
	}
}

func TestFilterStringTimeStartOnly(t *testing.T) {
	f := Filter{Match: "m", TimeStart: "2024-01-01"}
	s := f.String()
	if !strings.Contains(s, "start: 2024-01-01") {
		t.Errorf("expected start in string: %q", s)
	}
	if strings.Contains(s, "end:") {
		t.Errorf("unexpected end in string: %q", s)
	}
}

// --- Client.Explore ---

func TestClientExploreSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data":   []string{"cpu_usage", "mem_usage"},
		})
	}))
	defer srv.Close()

	c := &Client{Addr: srv.URL, HTTPClient: srv.Client()}
	names, err := c.Explore(context.Background(), Filter{Match: "cpu_usage"}, "", time.Now().Add(-time.Hour), time.Now())
	if err != nil {
		t.Fatalf("Explore returned error: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("expected 2 metric names, got %d: %v", len(names), names)
	}
}

func TestClientExploreWithTenant(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data":   []string{},
		})
	}))
	defer srv.Close()

	c := &Client{Addr: srv.URL, HTTPClient: srv.Client()}
	_, err := c.Explore(context.Background(), Filter{Match: "m"}, "42", time.Now().Add(-time.Hour), time.Now())
	if err != nil {
		t.Fatalf("Explore returned error: %v", err)
	}
	if !strings.Contains(gotPath, "select/42") {
		t.Errorf("expected tenant in path, got: %q", gotPath)
	}
}

func TestClientExploreServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := &Client{Addr: srv.URL, HTTPClient: srv.Client()}
	_, err := c.Explore(context.Background(), Filter{Match: "m"}, "", time.Now().Add(-time.Hour), time.Now())
	if err == nil {
		t.Fatal("expected error from server 500, got nil")
	}
}

// --- Client.ExportPipe ---

func TestClientExportPipeSuccess(t *testing.T) {
	payload := []byte("binary-native-data")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	c := &Client{Addr: srv.URL, HTTPClient: srv.Client()}
	rc, err := c.ExportPipe(context.Background(), srv.URL+"/api/v1/export/native", Filter{Match: "m"})
	if err != nil {
		t.Fatalf("ExportPipe returned error: %v", err)
	}
	defer rc.Close()

	body, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("reading export pipe body: %v", err)
	}
	if string(body) != string(payload) {
		t.Errorf("unexpected body: %q", string(body))
	}
}

func TestClientExportPipeServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	c := &Client{Addr: srv.URL, HTTPClient: srv.Client()}
	_, err := c.ExportPipe(context.Background(), srv.URL+"/api/v1/export/native", Filter{Match: "m"})
	if err == nil {
		t.Fatal("expected error from 404 response, got nil")
	}
}

// --- Client.ImportPipe ---

func TestClientImportPipeSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write([]byte("data"))
		pw.Close()
	}()

	c := &Client{Addr: srv.URL, HTTPClient: srv.Client()}
	err := c.ImportPipe(context.Background(), srv.URL+"/api/v1/import/native", pr)
	if err != nil {
		t.Fatalf("ImportPipe returned error: %v", err)
	}
}

func TestClientImportPipeServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer srv.Close()

	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write([]byte("data"))
		pw.Close()
	}()

	c := &Client{Addr: srv.URL, HTTPClient: srv.Client()}
	err := c.ImportPipe(context.Background(), srv.URL+"/api/v1/import/native", pr)
	if err == nil {
		t.Fatal("expected error from 400 response, got nil")
	}
}

// --- Client.GetSourceTenants ---

func TestClientGetSourceTenantsSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []string{"tenant1", "tenant2"},
		})
	}))
	defer srv.Close()

	c := &Client{Addr: srv.URL, HTTPClient: srv.Client()}
	tenants, err := c.GetSourceTenants(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("GetSourceTenants returned error: %v", err)
	}
	if len(tenants) != 2 {
		t.Fatalf("expected 2 tenants, got %d: %v", len(tenants), tenants)
	}
}

func TestClientGetSourceTenantsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer srv.Close()

	c := &Client{Addr: srv.URL, HTTPClient: srv.Client()}
	_, err := c.GetSourceTenants(context.Background(), Filter{})
	if err == nil {
		t.Fatal("expected error from 403 response, got nil")
	}
}

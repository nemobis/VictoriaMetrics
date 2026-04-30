package fsurl

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFSString(t *testing.T) {
	fs := &FS{Path: "http://example.com/rules.yaml"}
	got := fs.String()
	if got == "" {
		t.Fatal("String() returned empty")
	}
}

func TestFSInit(t *testing.T) {
	fs := &FS{Path: "http://example.com/rules.yaml"}
	if err := fs.Init(); err != nil {
		t.Fatalf("Init() returned unexpected error: %v", err)
	}
}

func TestFSList(t *testing.T) {
	fs := &FS{Path: "http://example.com/rules.yaml"}
	files, err := fs.List()
	if err != nil {
		t.Fatalf("List() returned unexpected error: %v", err)
	}
	if len(files) != 1 || files[0] != fs.Path {
		t.Fatalf("List() = %v, want [%q]", files, fs.Path)
	}
}

func TestFSReadSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("groups: []"))
	}))
	defer srv.Close()

	fs := &FS{Path: srv.URL + "/rules.yaml"}
	result, err := fs.Read([]string{fs.Path})
	if err != nil {
		t.Fatalf("Read() returned unexpected error: %v", err)
	}
	if string(result[fs.Path]) != "groups: []" {
		t.Errorf("Read() content mismatch: got %q", result[fs.Path])
	}
}

func TestFSReadNonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	fs := &FS{Path: srv.URL + "/missing.yaml"}
	_, err := fs.Read([]string{fs.Path})
	if err == nil {
		t.Fatal("Read() expected error for 404 response, got nil")
	}
}

func TestFSReadNetworkError(t *testing.T) {
	fs := &FS{Path: "http://127.0.0.1:1/nonexistent"}
	_, err := fs.Read([]string{fs.Path})
	if err == nil {
		t.Fatal("Read() expected error for unreachable server, got nil")
	}
}

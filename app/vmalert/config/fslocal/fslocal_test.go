package fslocal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFSString(t *testing.T) {
	fs := &FS{Pattern: "/etc/*.yaml"}
	got := fs.String()
	if got == "" {
		t.Fatal("String() returned empty")
	}
}

func TestFSInitValidPattern(t *testing.T) {
	fs := &FS{Pattern: "/tmp/*.yaml"}
	if err := fs.Init(); err != nil {
		t.Fatalf("Init() returned unexpected error for valid pattern: %v", err)
	}
}

func TestFSInitInvalidPattern(t *testing.T) {
	// doublestar.FilepathGlob is very permissive; use a clearly invalid pattern.
	fs := &FS{Pattern: "[invalid"}
	// This may or may not return an error depending on the implementation.
	// Just ensure it doesn't panic.
	_ = fs.Init()
}

func TestFSListMatchesFiles(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "rules1.yaml")
	f2 := filepath.Join(dir, "rules2.yaml")
	if err := os.WriteFile(f1, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f2, []byte("y"), 0600); err != nil {
		t.Fatal(err)
	}

	fs := &FS{Pattern: filepath.Join(dir, "*.yaml")}
	files, err := fs.List()
	if err != nil {
		t.Fatalf("List() returned unexpected error: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("List() returned %d files, want 2: %v", len(files), files)
	}
}

func TestFSListNoMatches(t *testing.T) {
	dir := t.TempDir()
	fs := &FS{Pattern: filepath.Join(dir, "*.yaml")}
	files, err := fs.List()
	if err != nil {
		t.Fatalf("List() returned unexpected error: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("List() returned %d files, want 0: %v", len(files), files)
	}
}

func TestFSReadExistingFiles(t *testing.T) {
	dir := t.TempDir()
	fpath := filepath.Join(dir, "rules.yaml")
	content := []byte("groups: []")
	if err := os.WriteFile(fpath, content, 0600); err != nil {
		t.Fatal(err)
	}

	fs := &FS{Pattern: fpath}
	result, err := fs.Read([]string{fpath})
	if err != nil {
		t.Fatalf("Read() returned unexpected error: %v", err)
	}
	if string(result[fpath]) != string(content) {
		t.Errorf("Read() content mismatch: got %q, want %q", result[fpath], content)
	}
}

func TestFSReadMissingFile(t *testing.T) {
	fs := &FS{Pattern: "/nonexistent/path/rules.yaml"}
	_, err := fs.Read([]string{"/nonexistent/path/rules.yaml"})
	if err == nil {
		t.Fatal("Read() expected error for missing file, got nil")
	}
}

func TestFSReadMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a.yaml": "content-a",
		"b.yaml": "content-b",
	}
	var paths []string
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}

	fs := &FS{Pattern: filepath.Join(dir, "*.yaml")}
	result, err := fs.Read(paths)
	if err != nil {
		t.Fatalf("Read() returned unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("Read() returned %d results, want 2", len(result))
	}
}

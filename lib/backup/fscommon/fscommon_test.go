package fscommon

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// ---- helpers ----------------------------------------------------------------

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", path, err)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func sortedPaths(paths []string) []string {
	cp := make([]string, len(paths))
	copy(cp, paths)
	sort.Strings(cp)
	return cp
}

// ---- IgnorePath -------------------------------------------------------------

func TestIgnorePath(t *testing.T) {
	cases := []struct {
		path   string
		ignore bool
	}{
		{"foo.ignore", true},
		{"bar/baz.ignore", true},
		{"backup_restore.ignore", true},
		{"backup_complete.ignore", true},
		{"/abs/path/file.ignore", true},
		{"ordinary.txt", false},
		{"no-suffix", false},
		{"ignored_almost", false},
		{"", false},
		{".ignore", true}, // only suffix matters
	}
	for _, tc := range cases {
		got := IgnorePath(tc.path)
		if got != tc.ignore {
			t.Errorf("IgnorePath(%q) = %v; want %v", tc.path, got, tc.ignore)
		}
	}
}

// ---- isSpecialFile ----------------------------------------------------------

func TestIsSpecialFile(t *testing.T) {
	specials := []string{
		"flock.lock",
		"restore-in-progress",    // backupnames.RestoreInProgressFilename
		"backup_restore.ignore",  // backupnames.RestoreMarkFileName
		"something.tmp",
		"data.tmp",
	}
	for _, name := range specials {
		if !isSpecialFile(name) {
			t.Errorf("isSpecialFile(%q) = false; want true", name)
		}
	}

	notSpecials := []string{
		"data.bin",
		"index.json",
		"metricname",
		"tmpfile",     // does not end with .tmp
		"flock.lock2", // different name
		"",
	}
	for _, name := range notSpecials {
		if isSpecialFile(name) {
			t.Errorf("isSpecialFile(%q) = true; want false", name)
		}
	}
}

// ---- AppendFiles ------------------------------------------------------------

func TestAppendFilesEmpty(t *testing.T) {
	dir := t.TempDir()
	got, err := AppendFiles(nil, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty slice, got %v", got)
	}
}

func TestAppendFilesNonExistentDir(t *testing.T) {
	_, err := AppendFiles(nil, "/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Fatal("expected error for non-existent directory, got nil")
	}
}

func TestAppendFilesSingleFile(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "a.bin"), "hello")

	got, err := AppendFiles(nil, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 file, got %v", got)
	}
	if got[0] != filepath.Join(dir, "a.bin") {
		t.Fatalf("unexpected path: %v", got[0])
	}
}

func TestAppendFilesSkipsSpecialFiles(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "real.bin"), "data")
	mustWriteFile(t, filepath.Join(dir, "flock.lock"), "lock")
	mustWriteFile(t, filepath.Join(dir, "tmp.tmp"), "temp")
	mustWriteFile(t, filepath.Join(dir, "restore-in-progress"), "marker")
	mustWriteFile(t, filepath.Join(dir, "backup_restore.ignore"), "marker2")

	got, err := AppendFiles(nil, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 file (real.bin), got %v", got)
	}
	if got[0] != filepath.Join(dir, "real.bin") {
		t.Fatalf("unexpected path: %v", got[0])
	}
}

func TestAppendFilesRecursive(t *testing.T) {
	dir := t.TempDir()
	mustMkdirAll(t, filepath.Join(dir, "sub1"))
	mustMkdirAll(t, filepath.Join(dir, "sub2", "nested"))
	mustWriteFile(t, filepath.Join(dir, "root.bin"), "r")
	mustWriteFile(t, filepath.Join(dir, "sub1", "a.bin"), "a")
	mustWriteFile(t, filepath.Join(dir, "sub2", "b.bin"), "b")
	mustWriteFile(t, filepath.Join(dir, "sub2", "nested", "c.bin"), "c")

	got, err := AppendFiles(nil, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got = sortedPaths(got)
	expected := sortedPaths([]string{
		filepath.Join(dir, "root.bin"),
		filepath.Join(dir, "sub1", "a.bin"),
		filepath.Join(dir, "sub2", "b.bin"),
		filepath.Join(dir, "sub2", "nested", "c.bin"),
	})
	if len(got) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, got)
	}
	for i := range expected {
		if got[i] != expected[i] {
			t.Errorf("path[%d]: got %q, want %q", i, got[i], expected[i])
		}
	}
}

func TestAppendFilesAppendsToExisting(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "new.bin"), "n")

	existing := []string{"pre-existing"}
	got, err := AppendFiles(existing, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %v", got)
	}
	if got[0] != "pre-existing" {
		t.Fatalf("first entry should be pre-existing, got %q", got[0])
	}
}

// ---- RemoveEmptyDirs --------------------------------------------------------

func TestRemoveEmptyDirsNonExistent(t *testing.T) {
	// Non-existent directory should not return an error.
	err := RemoveEmptyDirs("/nonexistent/path/xyz")
	if err != nil {
		t.Fatalf("unexpected error for non-existent dir: %v", err)
	}
}

func TestRemoveEmptyDirsEmpty(t *testing.T) {
	parent := t.TempDir()
	emptyDir := filepath.Join(parent, "empty")
	mustMkdirAll(t, emptyDir)

	if err := RemoveEmptyDirs(emptyDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(emptyDir); !os.IsNotExist(err) {
		t.Fatal("expected empty directory to be removed")
	}
}

func TestRemoveEmptyDirsWithFile(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "data.bin"), "content")

	if err := RemoveEmptyDirs(dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Directory should still exist because it has a file.
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("expected directory to still exist: %v", err)
	}
}

func TestRemoveEmptyDirsNestedEmpty(t *testing.T) {
	parent := t.TempDir()
	nested := filepath.Join(parent, "a", "b", "c")
	mustMkdirAll(t, nested)

	// Put a file at the parent level so parent doesn't get removed.
	mustWriteFile(t, filepath.Join(parent, "keep.bin"), "x")

	if err := RemoveEmptyDirs(parent); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Nested empty subdirs should be gone.
	if _, err := os.Stat(filepath.Join(parent, "a")); !os.IsNotExist(err) {
		t.Fatal("expected nested empty dirs to be removed")
	}
	// Parent must survive because it has keep.bin.
	if _, err := os.Stat(parent); err != nil {
		t.Fatalf("expected parent dir to survive: %v", err)
	}
}

func TestRemoveEmptyDirsMixedContent(t *testing.T) {
	parent := t.TempDir()
	emptyChild := filepath.Join(parent, "empty")
	filledChild := filepath.Join(parent, "filled")
	mustMkdirAll(t, emptyChild)
	mustMkdirAll(t, filledChild)
	mustWriteFile(t, filepath.Join(filledChild, "f.bin"), "data")

	if err := RemoveEmptyDirs(parent); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// emptyChild should be removed.
	if _, err := os.Stat(emptyChild); !os.IsNotExist(err) {
		t.Fatal("expected empty child to be removed")
	}
	// filledChild must still be present.
	if _, err := os.Stat(filledChild); err != nil {
		t.Fatalf("expected filled child to survive: %v", err)
	}
}

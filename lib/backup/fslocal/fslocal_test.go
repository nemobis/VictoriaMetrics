package fslocal

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/backup/common"
)

func TestFSString(t *testing.T) {
	fs := &FS{Dir: "/tmp/test"}
	got := fs.String()
	if !strings.Contains(got, "/tmp/test") {
		t.Fatalf("String() = %q; want it to contain /tmp/test", got)
	}
}

func TestFSInitAndMustStop(t *testing.T) {
	fs := &FS{Dir: t.TempDir()}
	if err := fs.Init(); err != nil {
		t.Fatalf("Init() unexpected error: %v", err)
	}
	fs.MustStop()
}

func TestFSInitWithBandwidthLimiter(t *testing.T) {
	fs := &FS{
		Dir:               t.TempDir(),
		MaxBytesPerSecond: 1024 * 1024,
	}
	if err := fs.Init(); err != nil {
		t.Fatalf("Init() unexpected error: %v", err)
	}
	if fs.bl == nil {
		t.Fatal("expected bandwidth limiter to be set")
	}
	fs.MustStop()
	if fs.bl != nil {
		t.Fatal("expected bandwidth limiter to be nil after MustStop")
	}
}

func TestFSListPartsNonExistingDir(t *testing.T) {
	fs := &FS{Dir: "/nonexistent/path/that/does/not/exist"}
	parts, err := fs.ListParts()
	if err != nil {
		t.Fatalf("ListParts() on non-existing dir returned error: %v", err)
	}
	if len(parts) != 0 {
		t.Fatalf("ListParts() on non-existing dir returned %d parts; want 0", len(parts))
	}
}

func TestFSListPartsEmptyDir(t *testing.T) {
	dir := t.TempDir()
	fs := &FS{Dir: dir}
	parts, err := fs.ListParts()
	if err != nil {
		t.Fatalf("ListParts() on empty dir returned error: %v", err)
	}
	if len(parts) != 0 {
		t.Fatalf("ListParts() on empty dir returned %d parts; want 0", len(parts))
	}
}

func TestFSListPartsWithFiles(t *testing.T) {
	dir := t.TempDir()

	// Write a small file.
	content := []byte("hello world")
	filePath := filepath.Join(dir, "testfile.bin")
	if err := os.WriteFile(filePath, content, 0600); err != nil {
		t.Fatalf("cannot write test file: %v", err)
	}

	fs := &FS{Dir: dir}
	parts, err := fs.ListParts()
	if err != nil {
		t.Fatalf("ListParts() returned error: %v", err)
	}
	if len(parts) == 0 {
		t.Fatal("ListParts() returned 0 parts; want at least 1")
	}
	// Verify the part path uses canonical separators.
	for _, p := range parts {
		if strings.Contains(p.Path, "\\") {
			t.Errorf("part path %q contains backslash; want canonical path", p.Path)
		}
	}
}

func TestFSListPartsZeroByteFile(t *testing.T) {
	dir := t.TempDir()

	filePath := filepath.Join(dir, "empty.bin")
	if err := os.WriteFile(filePath, []byte{}, 0600); err != nil {
		t.Fatalf("cannot write empty test file: %v", err)
	}

	fs := &FS{Dir: dir}
	parts, err := fs.ListParts()
	if err != nil {
		t.Fatalf("ListParts() returned error: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("ListParts() returned %d parts for zero-byte file; want 1", len(parts))
	}
	p := parts[0]
	if p.Size != 0 {
		t.Errorf("part size = %d; want 0 for empty file", p.Size)
	}
	if p.Offset != 0 {
		t.Errorf("part offset = %d; want 0 for empty file", p.Offset)
	}
}

func TestFSWriteAndReadPart(t *testing.T) {
	dir := t.TempDir()
	fs := &FS{Dir: dir}
	if err := fs.Init(); err != nil {
		t.Fatalf("Init() error: %v", err)
	}
	defer fs.MustStop()

	content := []byte("test data for read/write")
	p := common.Part{
		Path:     "subdir/data.bin",
		FileSize: uint64(len(content)),
		Offset:   0,
		Size:     uint64(len(content)),
	}

	// Write the part.
	wc, err := fs.NewDirectWriteCloser(p)
	if err != nil {
		t.Fatalf("NewDirectWriteCloser() error: %v", err)
	}
	if _, err := wc.Write(content); err != nil {
		t.Fatalf("Write() error: %v", err)
	}
	if err := wc.Close(); err != nil {
		t.Fatalf("WriteCloser.Close() error: %v", err)
	}

	// Finalize the file.
	if err := fs.FinalizeFile(p); err != nil {
		t.Fatalf("FinalizeFile() error: %v", err)
	}

	// Read it back.
	rc, err := fs.NewReadCloser(p)
	if err != nil {
		t.Fatalf("NewReadCloser() error: %v", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll() error: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Fatalf("ReadCloser.Close() error: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("read content = %q; want %q", got, content)
	}
}

func TestFSDeletePath(t *testing.T) {
	dir := t.TempDir()

	content := []byte("delete me")
	filePath := filepath.Join(dir, "todel.bin")
	if err := os.WriteFile(filePath, content, 0600); err != nil {
		t.Fatalf("cannot write file: %v", err)
	}

	fs := &FS{Dir: dir}
	size, err := fs.DeletePath("todel.bin")
	if err != nil {
		t.Fatalf("DeletePath() error: %v", err)
	}
	if size != uint64(len(content)) {
		t.Errorf("DeletePath() returned size %d; want %d", size, len(content))
	}
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Error("file still exists after DeletePath()")
	}
}

func TestFSDeletePathNonExistent(t *testing.T) {
	dir := t.TempDir()
	fs := &FS{Dir: dir}
	size, err := fs.DeletePath("does_not_exist.bin")
	if err != nil {
		t.Fatalf("DeletePath() on non-existent file returned error: %v", err)
	}
	if size != 0 {
		t.Errorf("DeletePath() on non-existent file returned size %d; want 0", size)
	}
}

func TestFSRemoveEmptyDirs(t *testing.T) {
	dir := t.TempDir()
	// Create a nested empty directory structure.
	emptyDir := filepath.Join(dir, "a", "b", "c")
	if err := os.MkdirAll(emptyDir, 0700); err != nil {
		t.Fatalf("MkdirAll() error: %v", err)
	}

	fs := &FS{Dir: dir}
	if err := fs.RemoveEmptyDirs(); err != nil {
		t.Fatalf("RemoveEmptyDirs() error: %v", err)
	}
	// The nested empty dirs should be gone.
	if _, err := os.Stat(emptyDir); !os.IsNotExist(err) {
		t.Error("empty dir still exists after RemoveEmptyDirs()")
	}
}

func TestFSPreallocateFile(t *testing.T) {
	dir := t.TempDir()
	fs := &FS{Dir: dir, UseTmpFiles: true}

	p := common.Part{
		Path:     "prealloc.bin",
		FileSize: 1024,
		Offset:   0,
		Size:     1024,
	}
	if err := fs.PreallocateFile(p); err != nil {
		t.Fatalf("PreallocateFile() error: %v", err)
	}
}

func TestFSCleanupTmpFiles(t *testing.T) {
	dir := t.TempDir()
	fs := &FS{Dir: dir}

	// Create a stray .tmp file.
	tmpFile := filepath.Join(dir, "leftover.tmp")
	if err := os.WriteFile(tmpFile, []byte("junk"), 0600); err != nil {
		t.Fatalf("cannot write tmp file: %v", err)
	}

	if err := fs.CleanupTmpFiles(); err != nil {
		t.Fatalf("CleanupTmpFiles() error: %v", err)
	}

	// On Linux canPreallocate == true, so the file should be removed.
	if canPreallocate {
		if _, err := os.Stat(tmpFile); !os.IsNotExist(err) {
			t.Error("tmp file still exists after CleanupTmpFiles()")
		}
	}
}

func TestFSCleanupTmpFilesNonExistentDir(t *testing.T) {
	fs := &FS{Dir: "/nonexistent/dir/path"}
	if err := fs.CleanupTmpFiles(); err != nil {
		t.Fatalf("CleanupTmpFiles() on non-existent dir returned error: %v", err)
	}
}

func TestDirectWriteCloserOverwrite(t *testing.T) {
	dir := t.TempDir()
	fs := &FS{Dir: dir}

	content := []byte("first write")
	p := common.Part{
		Path:     "overwrite.bin",
		FileSize: uint64(len(content)),
		Offset:   0,
		Size:     uint64(len(content)),
	}

	for i := range 2 {
		wc, err := fs.NewDirectWriteCloser(p)
		if err != nil {
			t.Fatalf("iteration %d: NewDirectWriteCloser() error: %v", i, err)
		}
		if _, err := wc.Write(content); err != nil {
			t.Fatalf("iteration %d: Write() error: %v", i, err)
		}
		if err := wc.Close(); err != nil {
			t.Fatalf("iteration %d: Close() error: %v", i, err)
		}
	}
}

func TestLimitedReadCloserRespectsSize(t *testing.T) {
	dir := t.TempDir()
	fs := &FS{Dir: dir}

	content := []byte("abcdefghij") // 10 bytes
	p := common.Part{
		Path:     "limited.bin",
		FileSize: uint64(len(content)),
		Offset:   0,
		Size:     5, // only read first 5 bytes
	}

	// Write full content.
	fullPart := common.Part{
		Path:     "limited.bin",
		FileSize: uint64(len(content)),
		Offset:   0,
		Size:     uint64(len(content)),
	}
	wc, err := fs.NewDirectWriteCloser(fullPart)
	if err != nil {
		t.Fatalf("NewDirectWriteCloser() error: %v", err)
	}
	if _, err := wc.Write(content); err != nil {
		t.Fatalf("Write() error: %v", err)
	}
	if err := wc.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	// Read only 5 bytes via part with Size=5.
	rc, err := fs.NewReadCloser(p)
	if err != nil {
		t.Fatalf("NewReadCloser() error: %v", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll() error: %v", err)
	}
	_ = rc.Close()
	if len(got) != 5 {
		t.Fatalf("read %d bytes; want 5", len(got))
	}
	if !bytes.Equal(got, content[:5]) {
		t.Fatalf("read data = %q; want %q", got, content[:5])
	}
}

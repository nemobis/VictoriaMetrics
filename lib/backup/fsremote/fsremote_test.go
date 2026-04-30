package fsremote

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/backup/common"
)

func TestFSString(t *testing.T) {
	fs := &FS{Dir: "/some/dir"}
	got := fs.String()
	if got == "" {
		t.Fatal("String() returned empty string")
	}
	// Should include the dir
	want := `fsremote "/some/dir"`
	if got != want {
		t.Fatalf("String(): got %q, want %q", got, want)
	}
}

func TestFSMustStop(t *testing.T) {
	fs := &FS{Dir: "/tmp"}
	// Should not panic
	fs.MustStop()
}

func TestFSListPartsNonExistingDir(t *testing.T) {
	fs := &FS{Dir: "/tmp/non-existent-dir-for-test-12345"}
	parts, err := fs.ListParts()
	if err != nil {
		t.Fatalf("ListParts on non-existing dir should return nil error, got: %v", err)
	}
	if parts != nil {
		t.Fatalf("ListParts on non-existing dir should return nil parts, got: %v", parts)
	}
}

func TestFSListPartsEmpty(t *testing.T) {
	dir := t.TempDir()
	fs := &FS{Dir: dir}
	parts, err := fs.ListParts()
	if err != nil {
		t.Fatalf("ListParts on empty dir: %v", err)
	}
	if len(parts) != 0 {
		t.Fatalf("expected 0 parts in empty dir, got %d", len(parts))
	}
}

func TestFSUploadAndDownloadPart(t *testing.T) {
	dir := t.TempDir()
	fs := &FS{Dir: dir}

	data := []byte("hello, backup world!")
	p := common.Part{
		Path:     "testfile",
		FileSize: uint64(len(data)),
		Offset:   0,
		Size:     uint64(len(data)),
	}

	// Upload
	if err := fs.UploadPart(p, bytes.NewReader(data)); err != nil {
		t.Fatalf("UploadPart: %v", err)
	}

	// Download
	var buf bytes.Buffer
	if err := fs.DownloadPart(p, &buf); err != nil {
		t.Fatalf("DownloadPart: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), data) {
		t.Fatalf("DownloadPart: got %q, want %q", buf.Bytes(), data)
	}
}

func TestFSListPartsAfterUpload(t *testing.T) {
	dir := t.TempDir()
	fs := &FS{Dir: dir}

	data := []byte("test data")
	p := common.Part{
		Path:     "somefile",
		FileSize: uint64(len(data)),
		Offset:   0,
		Size:     uint64(len(data)),
	}
	if err := fs.UploadPart(p, bytes.NewReader(data)); err != nil {
		t.Fatalf("UploadPart: %v", err)
	}

	parts, err := fs.ListParts()
	if err != nil {
		t.Fatalf("ListParts: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}
	if parts[0].Path != p.Path {
		t.Errorf("path: got %q, want %q", parts[0].Path, p.Path)
	}
	if parts[0].Size != p.Size {
		t.Errorf("size: got %d, want %d", parts[0].Size, p.Size)
	}
}

func TestFSDeletePart(t *testing.T) {
	dir := t.TempDir()
	fs := &FS{Dir: dir}

	data := []byte("delete me")
	p := common.Part{
		Path:     "todelete",
		FileSize: uint64(len(data)),
		Offset:   0,
		Size:     uint64(len(data)),
	}
	if err := fs.UploadPart(p, bytes.NewReader(data)); err != nil {
		t.Fatalf("UploadPart: %v", err)
	}

	// Verify it's there
	parts, err := fs.ListParts()
	if err != nil {
		t.Fatalf("ListParts: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("expected 1 part before delete, got %d", len(parts))
	}

	// Delete
	if err := fs.DeletePart(p); err != nil {
		t.Fatalf("DeletePart: %v", err)
	}

	// Verify it's gone
	parts, err = fs.ListParts()
	if err != nil {
		t.Fatalf("ListParts after delete: %v", err)
	}
	if len(parts) != 0 {
		t.Fatalf("expected 0 parts after delete, got %d", len(parts))
	}
}

func TestFSUploadPartWrongSize(t *testing.T) {
	dir := t.TempDir()
	fs := &FS{Dir: dir}

	data := []byte("short")
	p := common.Part{
		Path:     "wrongsize",
		FileSize: 100,
		Offset:   0,
		Size:     100, // Claims 100 bytes but only 5 provided
	}
	err := fs.UploadPart(p, bytes.NewReader(data))
	if err == nil {
		t.Fatal("UploadPart should fail when data size doesn't match part size")
	}
}

func TestFSDownloadPartWrongSize(t *testing.T) {
	dir := t.TempDir()
	fs := &FS{Dir: dir}

	// Upload with correct size first
	data := []byte("hello")
	p := common.Part{
		Path:     "file",
		FileSize: uint64(len(data)),
		Offset:   0,
		Size:     uint64(len(data)),
	}
	if err := fs.UploadPart(p, bytes.NewReader(data)); err != nil {
		t.Fatalf("UploadPart: %v", err)
	}

	// Now try to download with wrong (larger) size
	pWrong := p
	pWrong.Size = uint64(len(data)) + 10
	var buf bytes.Buffer
	err := fs.DownloadPart(pWrong, &buf)
	if err == nil {
		t.Fatal("DownloadPart should fail when stored size doesn't match part size")
	}
}

func TestFSCopyPart(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()
	srcFS := &FS{Dir: srcDir}
	dstFS := &FS{Dir: dstDir}

	data := []byte("copy me")
	p := common.Part{
		Path:     "copyfile",
		FileSize: uint64(len(data)),
		Offset:   0,
		Size:     uint64(len(data)),
	}
	if err := srcFS.UploadPart(p, bytes.NewReader(data)); err != nil {
		t.Fatalf("UploadPart: %v", err)
	}
	if err := dstFS.CopyPart(srcFS, p); err != nil {
		t.Fatalf("CopyPart: %v", err)
	}

	// Verify the copy
	var buf bytes.Buffer
	if err := dstFS.DownloadPart(p, &buf); err != nil {
		t.Fatalf("DownloadPart after CopyPart: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), data) {
		t.Fatalf("CopyPart: got %q, want %q", buf.Bytes(), data)
	}
}

func TestFSCopyPartWrongType(t *testing.T) {
	dir := t.TempDir()
	dstFS := &FS{Dir: dir}

	p := common.Part{Path: "x", Size: 1}
	err := dstFS.CopyPart(nil, p)
	// nil is not *FS, should fail
	if err == nil {
		t.Fatal("CopyPart with non-FS srcFS should fail")
	}
}

func TestFSCreateAndReadFile(t *testing.T) {
	dir := t.TempDir()
	fs := &FS{Dir: dir}

	data := []byte("file content")
	if err := fs.CreateFile("subdir/test.txt", data); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}

	got, err := fs.ReadFile("subdir/test.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("ReadFile: got %q, want %q", got, data)
	}
}

func TestFSHasFile(t *testing.T) {
	dir := t.TempDir()
	fs := &FS{Dir: dir}

	// File does not exist
	ok, err := fs.HasFile("nonexistent.txt")
	if err != nil {
		t.Fatalf("HasFile(nonexistent): %v", err)
	}
	if ok {
		t.Fatal("HasFile should return false for non-existent file")
	}

	// Create file
	if err := fs.CreateFile("exists.txt", []byte("data")); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}
	ok, err = fs.HasFile("exists.txt")
	if err != nil {
		t.Fatalf("HasFile(exists): %v", err)
	}
	if !ok {
		t.Fatal("HasFile should return true for existing file")
	}
}

func TestFSHasFileOnDirectory(t *testing.T) {
	dir := t.TempDir()
	fs := &FS{Dir: dir}

	// Create a subdirectory
	subdir := filepath.Join(dir, "subdir")
	if err := os.Mkdir(subdir, 0700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	ok, err := fs.HasFile("subdir")
	if err == nil {
		t.Fatal("HasFile on a directory should return an error")
	}
	if ok {
		t.Fatal("HasFile on a directory should return false")
	}
}

func TestFSDeleteFile(t *testing.T) {
	dir := t.TempDir()
	fs := &FS{Dir: dir}

	// Create and delete
	if err := fs.CreateFile("todelete.txt", []byte("data")); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}
	if err := fs.DeleteFile("todelete.txt"); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	ok, err := fs.HasFile("todelete.txt")
	if err != nil {
		t.Fatalf("HasFile after delete: %v", err)
	}
	if ok {
		t.Fatal("HasFile should return false after DeleteFile")
	}
}

func TestFSDeleteFileNonExistent(t *testing.T) {
	dir := t.TempDir()
	fs := &FS{Dir: dir}

	// Deleting a non-existent file should not error
	if err := fs.DeleteFile("nonexistent.txt"); err != nil {
		t.Fatalf("DeleteFile(nonexistent): expected nil error, got: %v", err)
	}
}

func TestFSRemoveEmptyDirs(t *testing.T) {
	dir := t.TempDir()
	fs := &FS{Dir: dir}

	// Create empty subdirectory
	subdir := filepath.Join(dir, "emptysubdir")
	if err := os.Mkdir(subdir, 0700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	if err := fs.RemoveEmptyDirs(); err != nil {
		t.Fatalf("RemoveEmptyDirs: %v", err)
	}

	// The empty subdir should be removed
	if _, err := os.Stat(subdir); !os.IsNotExist(err) {
		t.Fatalf("expected emptysubdir to be removed, but it still exists")
	}
}

func TestFSPath(t *testing.T) {
	fs := &FS{Dir: "/base"}
	p := common.Part{
		Path:     "foo/bar",
		FileSize: 100,
		Offset:   0,
		Size:     100,
	}
	got := fs.path(p)
	if got == "" {
		t.Fatal("path() returned empty string")
	}
	// Should contain base dir
	if !filepath.IsAbs(got) {
		t.Fatalf("path() should return absolute path, got %q", got)
	}
}

func TestFSUploadAndListMultipleParts(t *testing.T) {
	dir := t.TempDir()
	fs := &FS{Dir: dir}

	data1 := []byte("part one data")
	data2 := []byte("second part data here")

	p1 := common.Part{
		Path:     "file1",
		FileSize: uint64(len(data1)),
		Offset:   0,
		Size:     uint64(len(data1)),
	}
	p2 := common.Part{
		Path:     "file2",
		FileSize: uint64(len(data2)),
		Offset:   0,
		Size:     uint64(len(data2)),
	}

	if err := fs.UploadPart(p1, bytes.NewReader(data1)); err != nil {
		t.Fatalf("UploadPart p1: %v", err)
	}
	if err := fs.UploadPart(p2, bytes.NewReader(data2)); err != nil {
		t.Fatalf("UploadPart p2: %v", err)
	}

	parts, err := fs.ListParts()
	if err != nil {
		t.Fatalf("ListParts: %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}
}

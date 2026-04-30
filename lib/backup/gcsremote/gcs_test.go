package gcsremote

import (
	"context"
	"strings"
	"testing"

	"cloud.google.com/go/storage"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/backup/common"
)

// newTestContext returns a background context for tests.
func newTestContext(t *testing.T) context.Context {
	t.Helper()
	return context.Background()
}

// ---------------------------------------------------------------------------
// String() tests — no GCS connection required
// ---------------------------------------------------------------------------

func TestFSString(t *testing.T) {
	fs := &FS{
		Bucket: "my-bucket",
		Dir:    "my/dir",
	}
	got := fs.String()
	if !strings.Contains(got, "my-bucket") {
		t.Errorf("String() = %q; expected to contain bucket name", got)
	}
	if !strings.Contains(got, "my/dir") {
		t.Errorf("String() = %q; expected to contain dir", got)
	}
}

func TestFSStringEmptyFields(t *testing.T) {
	fs := &FS{}
	got := fs.String()
	if got == "" {
		t.Error("String() returned empty string; expected non-empty")
	}
}

// ---------------------------------------------------------------------------
// MustStop() tests — no GCS connection required
// ---------------------------------------------------------------------------

func TestFSMustStopWithoutInit(t *testing.T) {
	// MustStop on an un-initialized FS must not panic.
	fs := &FS{
		Bucket: "bucket",
		Dir:    "dir",
	}
	fs.MustStop() // should not panic
}

func TestFSMustStopIdempotent(t *testing.T) {
	// Calling MustStop twice on an un-initialized FS must not panic.
	fs := &FS{
		Bucket: "bucket",
		Dir:    "dir",
	}
	fs.MustStop()
	fs.MustStop()
}

// ---------------------------------------------------------------------------
// Init() failure-path tests — no real GCS credentials needed
// ---------------------------------------------------------------------------

// TestFSInitInvalidCredsFile verifies that Init() returns an error when a
// non-existent credentials file is provided.
func TestFSInitInvalidCredsFile(t *testing.T) {
	fs := &FS{
		Bucket:        "test-bucket",
		Dir:           "test-dir",
		CredsFilePath: "/nonexistent/credentials.json",
	}
	ctx := newTestContext(t)
	err := fs.Init(ctx)
	if err == nil {
		fs.MustStop()
		t.Fatal("Init() expected error for invalid creds file, got nil")
	}
}

// ---------------------------------------------------------------------------
// Double-Init BUG guard test
// ---------------------------------------------------------------------------

// TestFSInitDoublePanics verifies the guard that prevents calling Init twice.
// We inject a non-nil bkt directly (white-box) to trigger the panic without
// needing a real GCS client.
func TestFSInitDoublePanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Error("expected panic when calling Init on an already-initialized FS, got none")
		}
	}()

	fs := &FS{
		Bucket: "bucket",
		Dir:    "dir",
	}

	// Inject a non-nil bkt to simulate an already-initialized FS.
	// storage.BucketHandle is a plain struct; taking its address gives a
	// non-nil *storage.BucketHandle without needing a real GCS client.
	var dummy storage.BucketHandle
	fs.bkt = &dummy

	// Init must panic with the BUG message because bkt != nil.
	ctx := newTestContext(t)
	_ = fs.Init(ctx) // should panic before returning
}

// ---------------------------------------------------------------------------
// Dir normalisation tests (observable after Init starts)
// ---------------------------------------------------------------------------

// TestFSDirNormalization checks that Init() strips leading slashes from Dir
// and appends a trailing slash.  Dir is normalised before the GCS client is
// created, so we can observe the result even when Init returns an error.
func TestFSDirNormalization(t *testing.T) {
	fs := &FS{
		Bucket:        "bucket",
		Dir:           "///some/path",
		CredsFilePath: "/nonexistent/creds.json",
	}
	ctx := newTestContext(t)
	_ = fs.Init(ctx) // will fail, but Dir was normalised first

	if strings.HasPrefix(fs.Dir, "/") {
		t.Errorf("Dir = %q; leading slashes should have been stripped", fs.Dir)
	}
	if !strings.HasSuffix(fs.Dir, "/") {
		t.Errorf("Dir = %q; should end with '/'", fs.Dir)
	}
}

// TestFSDirAlreadyNormalized ensures a properly-formed Dir is left unchanged.
func TestFSDirAlreadyNormalized(t *testing.T) {
	fs := &FS{
		Bucket:        "bucket",
		Dir:           "some/path/",
		CredsFilePath: "/nonexistent/creds.json",
	}
	ctx := newTestContext(t)
	_ = fs.Init(ctx)

	if fs.Dir != "some/path/" {
		t.Errorf("Dir = %q; want some/path/", fs.Dir)
	}
}

// ---------------------------------------------------------------------------
// RemoveEmptyDirs — no-op for GCS
// ---------------------------------------------------------------------------

func TestFSRemoveEmptyDirs(t *testing.T) {
	fs := &FS{Bucket: "bucket", Dir: "dir/"}
	if err := fs.RemoveEmptyDirs(); err != nil {
		t.Fatalf("RemoveEmptyDirs() returned unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// CopyPart type mismatch
// ---------------------------------------------------------------------------

// TestFSCopyPartWrongSourceType verifies that CopyPart returns an error when
// the source OriginFS is not a *gcsremote.FS.
func TestFSCopyPartWrongSourceType(t *testing.T) {
	fs := &FS{Bucket: "dst", Dir: "dir/"}
	// Inject a non-nil bkt so that the type-assertion branch is reached.
	var dummy storage.BucketHandle
	fs.bkt = &dummy

	ctx := context.Background()
	fs.ctx = ctx

	p := common.Part{
		Path:   "some/file",
		Size:   100,
		Offset: 0,
	}

	err := fs.CopyPart(&nonGCSOriginFS{}, p)
	if err == nil {
		t.Fatal("expected error for non-GCS source FS, got nil")
	}
	if !strings.Contains(err.Error(), "server-side copying") {
		t.Errorf("error = %q; want it to mention server-side copying", err.Error())
	}
}

// nonGCSOriginFS is a stub that satisfies common.OriginFS but is not *gcsremote.FS.
type nonGCSOriginFS struct{}

func (n *nonGCSOriginFS) String() string                      { return "nonGCS" }
func (n *nonGCSOriginFS) MustStop()                           {}
func (n *nonGCSOriginFS) ListParts() ([]common.Part, error)   { return nil, nil }

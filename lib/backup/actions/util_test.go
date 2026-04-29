package actions

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/backup/common"
)

// TestNewRemoteFSErrorCases verifies that NewRemoteFS returns meaningful errors
// for invalid or unsupported paths before any cloud connection is attempted.
func TestNewRemoteFSErrorCases(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name    string
		path    string
		wantErr string
	}{
		{
			name:    "empty path",
			path:    "",
			wantErr: "path cannot be empty",
		},
		{
			name:    "no scheme",
			path:    "/absolute/path/without/scheme",
			wantErr: "missing scheme",
		},
		{
			name:    "unsupported scheme",
			path:    "ftp://bucket/dir",
			wantErr: "unsupported scheme",
		},
		{
			name:    "gcs missing dir",
			path:    "gs://mybucket",
			wantErr: "missing directory on the gcs bucket",
		},
		{
			name:    "s3 missing dir",
			path:    "s3://mybucket",
			wantErr: "missing directory on the s3 bucket",
		},
		{
			name:    "azblob missing dir",
			path:    "azblob://mycontainer",
			wantErr: "missing directory on the AZBlob container",
		},
		{
			name:    "fs relative path",
			path:    "fs://relative/path",
			wantErr: "dir must be absolute",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewRemoteFS(ctx, tc.path, nil)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !containsSubstring(err.Error(), tc.wantErr) {
				t.Fatalf("expected error %q to contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestNewRemoteFSLocalFS verifies that the fs:// scheme creates a local
// filesystem remote without error when given an absolute path.
func TestNewRemoteFSLocalFS(t *testing.T) {
	ctx := context.Background()
	fs, err := NewRemoteFS(ctx, "fs:///tmp/test-backup", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fs == nil {
		t.Fatal("expected non-nil RemoteFS")
	}
}

// TestGetPartsSizeEmpty verifies that getPartsSize returns 0 for an empty
// parts slice.
func TestGetPartsSizeEmpty(t *testing.T) {
	if got := getPartsSize(nil); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}

// TestGetPartsSizeAccumulates verifies that getPartsSize sums all part sizes.
func TestGetPartsSizeAccumulates(t *testing.T) {
	parts := []common.Part{
		{Size: 100},
		{Size: 200},
		{Size: 50},
	}
	got := getPartsSize(parts)
	if got != 350 {
		t.Fatalf("expected 350, got %d", got)
	}
}

// TestRunParallelInternalEmpty verifies that runParallelInternal returns nil
// immediately when given an empty parts list.
func TestRunParallelInternalEmpty(t *testing.T) {
	called := false
	err := runParallelInternal(4, nil, func(p common.Part) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Fatal("f should not be called for empty parts list")
	}
}

// TestRunParallelInternalAllSucceed verifies that all parts are processed when
// f always returns nil.
func TestRunParallelInternalAllSucceed(t *testing.T) {
	parts := []common.Part{
		{Path: "a", Size: 1},
		{Path: "b", Size: 2},
		{Path: "c", Size: 3},
	}
	var count atomic.Int64
	err := runParallelInternal(2, parts, func(p common.Part) error {
		count.Add(1)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := count.Load(); got != int64(len(parts)) {
		t.Fatalf("expected %d calls, got %d", len(parts), got)
	}
}

// TestRunParallelInternalPropagatesError verifies that when f returns an error,
// runParallelInternal returns that error.
func TestRunParallelInternalPropagatesError(t *testing.T) {
	parts := []common.Part{
		{Path: "a"},
		{Path: "b"},
		{Path: "c"},
	}
	wantErr := errors.New("test error")
	err := runParallelInternal(1, parts, func(p common.Part) error {
		if p.Path == "a" {
			return wantErr
		}
		return nil
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected %v, got %v", wantErr, err)
	}
}

// TestRunParallelInternalZeroConcurrency verifies that zero or negative
// concurrency is clamped to 1 and all parts are processed.
func TestRunParallelInternalZeroConcurrency(t *testing.T) {
	parts := []common.Part{{Path: "a"}, {Path: "b"}}
	var count atomic.Int64
	err := runParallelInternal(0, parts, func(p common.Part) error {
		count.Add(1)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := count.Load(); got != 2 {
		t.Fatalf("expected 2 calls, got %d", got)
	}
}

// TestRunParallelPerPathInternalEmpty verifies that runParallelPerPathInternal
// returns nil immediately for an empty map.
func TestRunParallelPerPathInternalEmpty(t *testing.T) {
	err := runParallelPerPathInternal(context.Background(), 2, nil, func(_ []common.Part) error {
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestRunParallelPerPathInternalAllSucceed verifies that all path groups are
// processed.
func TestRunParallelPerPathInternalAllSucceed(t *testing.T) {
	perPath := map[string][]common.Part{
		"path1": {{Path: "a"}, {Path: "b"}},
		"path2": {{Path: "c"}},
	}
	var count atomic.Int64
	err := runParallelPerPathInternal(context.Background(), 2, perPath, func(parts []common.Part) error {
		count.Add(int64(len(parts)))
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := count.Load(); got != 3 {
		t.Fatalf("expected 3 total parts, got %d", got)
	}
}

// TestRunParallelPerPathInternalContextCancellation verifies that context
// cancellation stops processing.
func TestRunParallelPerPathInternalContextCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	// Fill the map so all workers are kept busy for longer than the timeout.
	perPath := make(map[string][]common.Part)
	for i := range 10 {
		perPath[string(rune('a'+i))] = []common.Part{{Path: "x"}}
	}
	err := runParallelPerPathInternal(ctx, 1, perPath, func(_ []common.Part) error {
		time.Sleep(50 * time.Millisecond)
		return nil
	})
	if err == nil {
		t.Fatal("expected context cancellation error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
}

// containsSubstring is a helper because strings.Contains is not in scope.
func containsSubstring(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub ||
		len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}

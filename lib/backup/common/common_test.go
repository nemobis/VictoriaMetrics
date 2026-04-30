package common

import (
	"fmt"
	"testing"
)

func TestPartString(t *testing.T) {
	p := Part{
		Path:     "foo/bar",
		FileSize: 1024,
		Offset:   0,
		Size:     512,
	}
	s := p.String()
	if s == "" {
		t.Fatal("expected non-empty string representation")
	}
	// Should contain the path
	if got := p.String(); got == "" {
		t.Fatal("String() returned empty")
	}
}

func TestPartRemotePath(t *testing.T) {
	p := Part{
		Path:     "foo/bar",
		FileSize: 100,
		Offset:   0,
		Size:     100,
	}
	got := p.RemotePath("prefix")
	want := fmt.Sprintf("prefix/foo/bar/%016X_%016X_%016X", uint64(100), uint64(0), uint64(100))
	if got != want {
		t.Fatalf("RemotePath: got %q, want %q", got, want)
	}
}

func TestPartRemotePathTrimsTrailingSlash(t *testing.T) {
	p := Part{
		Path:     "x",
		FileSize: 10,
		Offset:   0,
		Size:     10,
	}
	withSlash := p.RemotePath("prefix/")
	withoutSlash := p.RemotePath("prefix")
	if withSlash != withoutSlash {
		t.Fatalf("expected same result regardless of trailing slash; got %q vs %q", withSlash, withoutSlash)
	}
}

func TestPartLocalPath(t *testing.T) {
	p := Part{
		Path: "some/file",
	}
	got := p.LocalPath("/base")
	if got == "" {
		t.Fatal("LocalPath returned empty string")
	}
}

func TestToCanonicalPath(t *testing.T) {
	path := "foo/bar/baz"
	got := ToCanonicalPath(path)
	// On Linux separator is /, so it should be unchanged
	if got != path {
		t.Fatalf("ToCanonicalPath: got %q, want %q", got, path)
	}
}

func TestParseFromRemotePath(t *testing.T) {
	tests := []struct {
		name       string
		remotePath string
		wantOK     bool
		wantPath   string
		wantFS     uint64
		wantOffset uint64
		wantSize   uint64
	}{
		{
			name:       "valid path",
			remotePath: "some/path/0000000000000064_0000000000000000_0000000000000064",
			wantOK:     true,
			wantPath:   "some/path",
			wantFS:     100,
			wantOffset: 0,
			wantSize:   100,
		},
		{
			name:       "valid path with subdirectory",
			remotePath: "a/b/c/0000000000000001_0000000000000002_0000000000000003",
			wantOK:     true,
			wantPath:   "a/b/c",
			wantFS:     1,
			wantOffset: 2,
			wantSize:   3,
		},
		{
			name:       "invalid - no underscore",
			remotePath: "some/path/AAAA",
			wantOK:     false,
		},
		{
			name:       "invalid - empty",
			remotePath: "",
			wantOK:     false,
		},
		{
			name:       "invalid - only filename no path",
			remotePath: "0000000000000001_0000000000000002_0000000000000003",
			wantOK:     false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var p Part
			ok := p.ParseFromRemotePath(tc.remotePath)
			if ok != tc.wantOK {
				t.Fatalf("ParseFromRemotePath(%q): got ok=%v, want %v", tc.remotePath, ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if p.Path != tc.wantPath {
				t.Errorf("Path: got %q, want %q", p.Path, tc.wantPath)
			}
			if p.FileSize != tc.wantFS {
				t.Errorf("FileSize: got %d, want %d", p.FileSize, tc.wantFS)
			}
			if p.Offset != tc.wantOffset {
				t.Errorf("Offset: got %d, want %d", p.Offset, tc.wantOffset)
			}
			if p.Size != tc.wantSize {
				t.Errorf("Size: got %d, want %d", p.Size, tc.wantSize)
			}
		})
	}
}

func TestSortParts(t *testing.T) {
	parts := []Part{
		{Path: "b", Offset: 0},
		{Path: "a", Offset: 10},
		{Path: "a", Offset: 0},
		{Path: "b", Offset: 5},
	}
	SortParts(parts)
	expected := []Part{
		{Path: "a", Offset: 0},
		{Path: "a", Offset: 10},
		{Path: "b", Offset: 0},
		{Path: "b", Offset: 5},
	}
	for i, p := range parts {
		if p.Path != expected[i].Path || p.Offset != expected[i].Offset {
			t.Fatalf("SortParts[%d]: got {%q, %d}, want {%q, %d}", i, p.Path, p.Offset, expected[i].Path, expected[i].Offset)
		}
	}
}

func TestSortPartsEmpty(t *testing.T) {
	SortParts(nil)
	SortParts([]Part{})
}

func TestPartsDifference(t *testing.T) {
	a := []Part{
		{Path: "x", FileSize: 10, Offset: 0, Size: 10},
		{Path: "y", FileSize: 20, Offset: 0, Size: 20},
		{Path: "z", FileSize: 30, Offset: 0, Size: 30},
	}
	b := []Part{
		{Path: "y", FileSize: 20, Offset: 0, Size: 20},
	}
	diff := PartsDifference(a, b)
	if len(diff) != 2 {
		t.Fatalf("PartsDifference: got %d parts, want 2", len(diff))
	}
	for _, p := range diff {
		if p.Path == "y" {
			t.Fatal("PartsDifference: should not contain part from b")
		}
	}
}

func TestPartsDifferenceEmpty(t *testing.T) {
	a := []Part{{Path: "x", FileSize: 1, Offset: 0, Size: 1}}
	diff := PartsDifference(a, a)
	if len(diff) != 0 {
		t.Fatalf("PartsDifference with identical sets: got %d parts, want 0", len(diff))
	}
}

func TestPartsDifferenceNilB(t *testing.T) {
	a := []Part{{Path: "x", FileSize: 1, Offset: 0, Size: 1}}
	diff := PartsDifference(a, nil)
	if len(diff) != 1 {
		t.Fatalf("PartsDifference with nil b: got %d parts, want 1", len(diff))
	}
}

func TestPartsIntersect(t *testing.T) {
	a := []Part{
		{Path: "x", FileSize: 10, Offset: 0, Size: 10, ActualSize: 10},
		{Path: "y", FileSize: 20, Offset: 0, Size: 20, ActualSize: 20},
	}
	b := []Part{
		{Path: "x", FileSize: 10, Offset: 0, Size: 10, ActualSize: 10},
		{Path: "z", FileSize: 30, Offset: 0, Size: 30, ActualSize: 30},
	}
	inter := PartsIntersect(a, b)
	if len(inter) != 1 {
		t.Fatalf("PartsIntersect: got %d parts, want 1", len(inter))
	}
	if inter[0].Path != "x" {
		t.Fatalf("PartsIntersect: got path %q, want %q", inter[0].Path, "x")
	}
}

func TestPartsIntersectEmpty(t *testing.T) {
	a := []Part{{Path: "x", FileSize: 1, Offset: 0, Size: 1, ActualSize: 1}}
	b := []Part{{Path: "y", FileSize: 2, Offset: 0, Size: 2, ActualSize: 2}}
	inter := PartsIntersect(a, b)
	if len(inter) != 0 {
		t.Fatalf("PartsIntersect disjoint sets: got %d parts, want 0", len(inter))
	}
}

func TestPartsIntersectBothNil(t *testing.T) {
	inter := PartsIntersect(nil, nil)
	if len(inter) != 0 {
		t.Fatalf("PartsIntersect(nil, nil): got %d parts, want 0", len(inter))
	}
}

func TestPartKeyUniquenessForPartsJSON(t *testing.T) {
	p1 := Part{Path: "x/parts.json", FileSize: 1, Offset: 0, Size: 1, ActualSize: 1}
	p2 := Part{Path: "x/parts.json", FileSize: 1, Offset: 0, Size: 1, ActualSize: 1}
	// Both have the same fields but should get unique keys since they're parts.json
	k1 := p1.key()
	k2 := p2.key()
	if k1 == k2 {
		t.Fatal("parts.json entries should have unique keys; got same key for two calls")
	}
}

func TestPartKeyNormalPath(t *testing.T) {
	p1 := Part{Path: "x/data", FileSize: 1, Offset: 0, Size: 1, ActualSize: 1}
	p2 := Part{Path: "x/data", FileSize: 1, Offset: 0, Size: 1, ActualSize: 1}
	// Identical parts should have the same key
	k1 := p1.key()
	k2 := p2.key()
	if k1 != k2 {
		t.Fatalf("identical parts should have same key; got %q vs %q", k1, k2)
	}
}

func TestPartKeyDifferentParts(t *testing.T) {
	p1 := Part{Path: "x/data", FileSize: 1, Offset: 0, Size: 1, ActualSize: 1}
	p2 := Part{Path: "x/data", FileSize: 1, Offset: 1, Size: 1, ActualSize: 1}
	k1 := p1.key()
	k2 := p2.key()
	if k1 == k2 {
		t.Fatal("parts with different offsets should have different keys")
	}
}

func TestMaxPartSize(t *testing.T) {
	if MaxPartSize != 1024*1024*1024 {
		t.Fatalf("unexpected MaxPartSize: got %d, want %d", MaxPartSize, 1024*1024*1024)
	}
}

func TestParseFromRemotePathRoundTrip(t *testing.T) {
	original := Part{
		Path:     "some/file",
		FileSize: 12345,
		Offset:   100,
		Size:     200,
	}
	remotePath := original.RemotePath("")
	// remotePath will start with /some/file/... strip leading /
	// Actually RemotePath with empty prefix returns /some/file/...
	// ParseFromRemotePath strips leading slashes
	var parsed Part
	ok := parsed.ParseFromRemotePath(remotePath)
	if !ok {
		t.Fatalf("ParseFromRemotePath failed for %q", remotePath)
	}
	if parsed.Path != original.Path {
		t.Errorf("Path mismatch: got %q, want %q", parsed.Path, original.Path)
	}
	if parsed.FileSize != original.FileSize {
		t.Errorf("FileSize mismatch: got %d, want %d", parsed.FileSize, original.FileSize)
	}
	if parsed.Offset != original.Offset {
		t.Errorf("Offset mismatch: got %d, want %d", parsed.Offset, original.Offset)
	}
	if parsed.Size != original.Size {
		t.Errorf("Size mismatch: got %d, want %d", parsed.Size, original.Size)
	}
}

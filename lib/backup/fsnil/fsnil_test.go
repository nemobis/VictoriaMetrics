package fsnil

import (
	"testing"
)

func TestFSString(t *testing.T) {
	fs := &FS{}
	if got := fs.String(); got != "fsnil" {
		t.Errorf("String() = %q, want %q", got, "fsnil")
	}
}

func TestFSMustStop(t *testing.T) {
	fs := &FS{}
	// MustStop is a no-op; just verify it doesn't panic.
	fs.MustStop()
}

func TestFSListParts(t *testing.T) {
	fs := &FS{}
	parts, err := fs.ListParts()
	if err != nil {
		t.Fatalf("ListParts() returned unexpected error: %v", err)
	}
	if parts != nil {
		t.Fatalf("ListParts() = %v, want nil", parts)
	}
}

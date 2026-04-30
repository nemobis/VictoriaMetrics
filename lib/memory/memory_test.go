package memory

import (
	"sync"
	"testing"
)

// resetState resets package-level state so initOnce re-runs.
// Safe within a single test binary; tests must not run in parallel with each other.
func resetState() {
	once = sync.Once{}
	allowedMemory = 0
	remainingMemory = 0
}

// TestMain ensures flag.Parse is called (the test framework does this, but
// the memory package requires it before calling Allowed/Remaining).
func TestMain(m *testing.M) {
	// testing.M.Run() calls flag.Parse internally before running tests, which
	// satisfies the memory package's requirement.
	m.Run()
}

func TestAllowedIsPositive(t *testing.T) {
	resetState()
	v := Allowed()
	if v <= 0 {
		t.Fatalf("Allowed() must be positive, got %d", v)
	}
}

func TestRemainingIsPositive(t *testing.T) {
	resetState()
	v := Remaining()
	if v <= 0 {
		t.Fatalf("Remaining() must be positive, got %d", v)
	}
}

func TestAllowedPlusRemainingEqualsLimit(t *testing.T) {
	resetState()
	a := Allowed()
	r := Remaining()
	if a+r != memoryLimit {
		t.Fatalf("Allowed(%d) + Remaining(%d) != memoryLimit(%d)", a, r, memoryLimit)
	}
}

func TestAllowedIdempotent(t *testing.T) {
	resetState()
	a1 := Allowed()
	a2 := Allowed()
	if a1 != a2 {
		t.Fatalf("Allowed() returned different values: %d vs %d", a1, a2)
	}
}

func TestRemainingIdempotent(t *testing.T) {
	resetState()
	r1 := Remaining()
	r2 := Remaining()
	if r1 != r2 {
		t.Fatalf("Remaining() returned different values: %d vs %d", r1, r2)
	}
}

func TestMemoryLimitPositive(t *testing.T) {
	resetState()
	Allowed() // trigger initOnce
	if memoryLimit <= 0 {
		t.Fatalf("memoryLimit must be positive, got %d", memoryLimit)
	}
}

func TestAllowedBelowMemoryLimit(t *testing.T) {
	resetState()
	a := Allowed()
	if a >= memoryLimit {
		t.Fatalf("Allowed(%d) must be less than memoryLimit(%d)", a, memoryLimit)
	}
}

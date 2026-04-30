package slicesutil

import (
	"testing"
)

// ---------- ExtendCapacity ----------

func TestExtendCapacityNoop(t *testing.T) {
	// Slice already has enough capacity – no allocation expected.
	a := make([]int, 3, 10)
	a[0], a[1], a[2] = 1, 2, 3
	b := ExtendCapacity(a, 5) // needs len(3)+5=8 <= cap(10)
	if len(b) != 3 {
		t.Fatalf("expected len 3, got %d", len(b))
	}
	if cap(b) < 8 {
		t.Fatalf("expected cap >= 8, got %d", cap(b))
	}
	// Existing elements must be preserved.
	for i, v := range []int{1, 2, 3} {
		if b[i] != v {
			t.Fatalf("element %d: want %d, got %d", i, v, b[i])
		}
	}
}

func TestExtendCapacityGrows(t *testing.T) {
	a := make([]int, 3, 3)
	b := ExtendCapacity(a, 10) // needs 13 > cap(3) – must grow
	if len(b) != 3 {
		t.Fatalf("expected len 3, got %d", len(b))
	}
	if cap(b) < 13 {
		t.Fatalf("expected cap >= 13, got %d", cap(b))
	}
}

func TestExtendCapacityZeroItemsToAdd(t *testing.T) {
	a := make([]int, 4, 4)
	b := ExtendCapacity(a, 0)
	if len(b) != 4 {
		t.Fatalf("expected len 4, got %d", len(b))
	}
}

func TestExtendCapacityEmptySlice(t *testing.T) {
	var a []string
	b := ExtendCapacity(a, 5)
	if len(b) != 0 {
		t.Fatalf("expected len 0, got %d", len(b))
	}
	if cap(b) < 5 {
		t.Fatalf("expected cap >= 5, got %d", cap(b))
	}
}

// ---------- SetLength ----------

func TestSetLengthGrows(t *testing.T) {
	var a []int
	b := SetLength(a, 5)
	if len(b) != 5 {
		t.Fatalf("expected len 5, got %d", len(b))
	}
}

func TestSetLengthShrinks(t *testing.T) {
	a := make([]int, 10)
	for i := range a {
		a[i] = i
	}
	b := SetLength(a, 3)
	if len(b) != 3 {
		t.Fatalf("expected len 3, got %d", len(b))
	}
	// Original values must still be accessible.
	for i := 0; i < 3; i++ {
		if b[i] != i {
			t.Fatalf("element %d: want %d, got %d", i, i, b[i])
		}
	}
}

func TestSetLengthSameLength(t *testing.T) {
	a := []int{7, 8, 9}
	b := SetLength(a, 3)
	if len(b) != 3 {
		t.Fatalf("expected len 3, got %d", len(b))
	}
	for i, v := range []int{7, 8, 9} {
		if b[i] != v {
			t.Fatalf("element %d: want %d, got %d", i, v, b[i])
		}
	}
}

func TestSetLengthZero(t *testing.T) {
	a := []byte{1, 2, 3}
	b := SetLength(a, 0)
	if len(b) != 0 {
		t.Fatalf("expected len 0, got %d", len(b))
	}
}

// ---------- Buffer ----------

func TestBufferReset(t *testing.T) {
	var buf Buffer[int]
	buf.B = append(buf.B, 1, 2, 3)
	buf.Reset()
	if len(buf.B) != 0 {
		t.Fatalf("expected empty buffer after Reset, got len=%d", len(buf.B))
	}
}

func TestBufferPoolGetPut(t *testing.T) {
	var bp BufferPool[string]

	b := bp.Get()
	if b == nil {
		t.Fatal("Get returned nil")
	}
	b.B = append(b.B, "hello", "world")
	bp.Put(b)

	// After Put the buffer should be reset.
	b2 := bp.Get()
	if len(b2.B) != 0 {
		t.Fatalf("expected empty buffer from pool, got len=%d", len(b2.B))
	}
	bp.Put(b2)
}

func TestBufferPoolGetNilPool(t *testing.T) {
	// Fresh pool must return a non-nil buffer.
	var bp BufferPool[float64]
	b := bp.Get()
	if b == nil {
		t.Fatal("Get on empty pool returned nil")
	}
	bp.Put(b)
}

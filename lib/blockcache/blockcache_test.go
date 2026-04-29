package blockcache

import (
	"fmt"
	"sync"
	"testing"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/cgroup"
)

func TestCache(t *testing.T) {
	sizeMaxBytes := 64 * 1024
	// Multiply sizeMaxBytes by the square of available CPU cores
	// in order to get proper distribution of sizes between cache shards.
	// See https://github.com/VictoriaMetrics/VictoriaMetrics/issues/2204
	cpus := cgroup.AvailableCPUs()
	sizeMaxBytes *= cpus * cpus
	getMaxSize := func() int {
		return sizeMaxBytes
	}
	c := NewCache(getMaxSize)
	defer c.MustStop()
	if n := c.SizeBytes(); n != 0 {
		t.Fatalf("unexpected SizeBytes(); got %d; want %d", n, 0)
	}
	if n := c.SizeMaxBytes(); n != sizeMaxBytes {
		t.Fatalf("unexpected SizeMaxBytes(); got %d; want %d", n, sizeMaxBytes)
	}
	offset := uint64(1234)
	part := (any)("foobar")
	k := Key{
		Offset: offset,
		Part:   part,
	}
	var b testBlock
	blockSize := b.SizeBytes()
	// Put a single entry into cache
	c.TryPutBlock(k, &b)
	if n := c.Len(); n != 1 {
		t.Fatalf("unexpected number of items in the cache; got %d; want %d", n, 1)
	}
	if n := c.SizeBytes(); n != blockSize {
		t.Fatalf("unexpected SizeBytes(); got %d; want %d", n, blockSize)
	}
	if n := c.Requests(); n != 0 {
		t.Fatalf("unexpected number of requests; got %d; want %d", n, 0)
	}
	if n := c.Misses(); n != 0 {
		t.Fatalf("unexpected number of misses; got %d; want %d", n, 0)
	}
	// Obtain this entry from the cache
	if b1 := c.GetBlock(k); b1 != &b {
		t.Fatalf("unexpected block obtained; got %v; want %v", b1, &b)
	}
	if n := c.Requests(); n != 1 {
		t.Fatalf("unexpected number of requests; got %d; want %d", n, 1)
	}
	if n := c.Misses(); n != 0 {
		t.Fatalf("unexpected number of misses; got %d; want %d", n, 0)
	}
	// Obtain non-existing entry from the cache
	if b1 := c.GetBlock(Key{Offset: offset + 1}); b1 != nil {
		t.Fatalf("unexpected non-nil block obtained for non-existing key: %v", b1)
	}
	if n := c.Requests(); n != 2 {
		t.Fatalf("unexpected number of requests; got %d; want %d", n, 2)
	}
	if n := c.Misses(); n != 1 {
		t.Fatalf("unexpected number of misses; got %d; want %d", n, 1)
	}
	// Remove entries for the given part from the cache
	c.RemoveBlocksForPart(part)
	if n := c.SizeBytes(); n != 0 {
		t.Fatalf("unexpected SizeBytes(); got %d; want %d", n, 0)
	}
	// Verify that the entry has been removed from the cache
	if b1 := c.GetBlock(k); b1 != nil {
		t.Fatalf("unexpected non-nil block obtained after removing all the blocks for the part; got %v", b1)
	}
	if n := c.Requests(); n != 3 {
		t.Fatalf("unexpected number of requests; got %d; want %d", n, 3)
	}
	if n := c.Misses(); n != 2 {
		t.Fatalf("unexpected number of misses; got %d; want %d", n, 2)
	}
	for i := range *missesBeforeCaching {
		// Store the missed entry to the cache. It shouldn't be stored because of the previous cache miss
		c.TryPutBlock(k, &b)
		if n := c.SizeBytes(); n != 0 {
			t.Fatalf("unexpected SizeBytes(); got %d; want %d", n, 0)
		}
		// Verify that the entry wasn't stored to the cache.
		if b1 := c.GetBlock(k); b1 != nil {
			t.Fatalf("unexpected non-nil block obtained after removing all the blocks for the part; got %v", b1)
		}
		if n := c.Requests(); n != uint64(4+i) {
			t.Fatalf("unexpected number of requests; got %d; want %d", n, 4+i)
		}
		if n := c.Misses(); n != uint64(3+i) {
			t.Fatalf("unexpected number of misses; got %d; want %d", n, 3+i)
		}
	}
	// Store the entry again. Now it must be stored because of the second cache miss.
	c.TryPutBlock(k, &b)
	if n := c.SizeBytes(); n != blockSize {
		t.Fatalf("unexpected SizeBytes(); got %d; want %d", n, blockSize)
	}
	if b1 := c.GetBlock(k); b1 != &b {
		t.Fatalf("unexpected block obtained; got %v; want %v", b1, &b)
	}
	if n := c.Requests(); n != uint64(4+*missesBeforeCaching) {
		t.Fatalf("unexpected number of requests; got %d; want %d", n, 4+*missesBeforeCaching)
	}
	if n := c.Misses(); n != uint64(2+*missesBeforeCaching) {
		t.Fatalf("unexpected number of misses; got %d; want %d", n, 2+*missesBeforeCaching)
	}

	// Manually clean the cache. The entry shouldn't be deleted because it was recently accessed.
	c.cleanPerKeyMisses()
	c.cleanByTimeout()
	if n := c.SizeBytes(); n != blockSize {
		t.Fatalf("unexpected SizeBytes(); got %d; want %d", n, blockSize)
	}
}

// TestCachePeriodicAccessNeverCached verifies that a block accessed regularly
// at intervals longer than the cleanPerKeyMisses period eventually gets cached.
//
// Each "access" below is a GetBlock+TryPutBlock pair, exactly as the callers
// in part_search.go do: read from disk when GetBlock misses, then offer the
// result back to the cache via TryPutBlock.
//
// With the bug, cleanPerKeyMisses resets the perKeyMisses counter to zero
// unconditionally.  A block accessed slower than missesBeforeCaching+1 times
// per 3-minute window (the default) therefore never reaches the caching
// threshold and is re-read from disk on every single access forever.
func TestCachePeriodicAccessNeverCached(t *testing.T) {
	sizeMaxBytes := 64 * 1024
	cpus := cgroup.AvailableCPUs()
	sizeMaxBytes *= cpus * cpus

	c := NewCache(func() int { return sizeMaxBytes })
	defer c.MustStop()

	part := (any)("testpart")
	k := Key{Part: part, Offset: 42}
	b := &testBlock{}

	// access is the helper that simulates one "read-and-try-cache" cycle.
	// It returns true when the block was found in cache (cache hit).
	access := func() bool {
		got := c.GetBlock(k)
		if got != nil {
			return true
		}
		c.TryPutBlock(k, b)
		return false
	}

	// Access 1: first miss, counter → 1, not cached.
	if access() {
		t.Fatal("block must not be in cache on first access")
	}

	// Simulate the background cleanPerKeyMisses timer firing between accesses.
	// Bug: this wipes the counter back to zero, so the next TryPutBlock sees
	// misses == 1 again instead of 2, and the block is never admitted.
	c.cleanPerKeyMisses()

	// Access 2: after the reset the bug restarts the counter at 1; the fix
	// preserves it at 1 from before the reset and increments it to 2.
	if access() {
		t.Fatal("block must not be in cache yet after second access")
	}

	// Access 3: GetBlock still misses (block not in cache yet), then TryPutBlock
	// is called with the freshly read block.
	//   buggy path – counter is 2 (reset+1+1), still ≤ missesBeforeCaching, NOT admitted.
	//   fixed path – counter is 3 (preserved 1 + 2 increments), > missesBeforeCaching, ADMITTED.
	access()

	// After access 3, with the fix TryPutBlock has admitted the block to the
	// cache, so the very next GetBlock must be a hit.
	// With the bug the counter was reset by cleanPerKeyMisses, so the block
	// was never admitted and this GetBlock returns nil.
	if got := c.GetBlock(k); got == nil {
		t.Fatalf("block must be cached after %d misses spread across a cleanPerKeyMisses call; "+
			"cleanPerKeyMisses must not discard pending miss counts for active blocks",
			*missesBeforeCaching+1)
	}
}

func TestCacheConcurrentAccess(_ *testing.T) {
	const sizeMaxBytes = 16 * 1024 * 1024
	getMaxSize := func() int {
		return sizeMaxBytes
	}
	c := NewCache(getMaxSize)
	defer c.MustStop()

	workers := 5
	var wg sync.WaitGroup
	for worker := range workers {
		wg.Go(func() {
			testCacheSetGet(c, worker)
		})
	}
	wg.Wait()
}

func testCacheSetGet(c *Cache, worker int) {
	for i := range 1000 {
		part := (any)(i)
		b := testBlock{}
		k := Key{
			Offset: uint64(worker*1000 + i),
			Part:   part,
		}
		c.TryPutBlock(k, &b)
		if b1 := c.GetBlock(k); b1 != &b {
			panic(fmt.Errorf("unexpected block obtained; got %v; want %v", b1, &b))
		}
		if b1 := c.GetBlock(Key{}); b1 != nil {
			panic(fmt.Errorf("unexpected non-nil block obtained: %v", b1))
		}
	}
}

type testBlock struct{}

func (tb *testBlock) SizeBytes() int {
	return 42
}

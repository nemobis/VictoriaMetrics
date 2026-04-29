//go:build synctest

package workingsetcache

import (
	"fmt"
	"os"
	"testing"
	"testing/synctest"
	"time"

	"github.com/VictoriaMetrics/fastcache"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/fs"
)

func TestCacheModeTransition(t *testing.T) {
	var cs fastcache.Stats
	assertCachesMaxSize := func(t *testing.T, c *Cache, maxPrevSize, maxCurrSize uint64) {
		t.Helper()

		c.mu.Lock()
		defer c.mu.Unlock()

		prev := c.prev.Load()
		cs.Reset()
		prev.UpdateStats(&cs)
		if cs.MaxBytesSize != maxPrevSize {
			t.Fatalf("unexpected prev cache maxSizeBytes: got %d, want %d", cs.MaxBytesSize, maxPrevSize)
		}

		curr := c.curr.Load()
		cs.Reset()
		curr.UpdateStats(&cs)
		if cs.MaxBytesSize != maxCurrSize {
			t.Fatalf("unexpected curr cache maxSizeBytes: got %d, want %d", cs.MaxBytesSize, maxCurrSize)
		}
	}
	const (
		cacheSize = 256 * 1024 * 1024
		// fastcache.chunkSize * fastcache.bucketsCount
		minCacheSize = 64 * 1024 * 512
		// check interval is ~1500ms with 10% jitter
		cacheSizeCheckInterval = time.Millisecond * 2000
	)

	baseKey := make([]byte, 8096)
	value := make([]byte, 12096)

	fillCacheToFull := func(t *testing.T, c *Cache) {
		t.Helper()
		for i := range 12096 {
			key := append(baseKey[:len(baseKey):len(baseKey)], fmt.Sprintf("idx_%d", i)...)
			c.Set(key, value)
		}
	}
	synctest.Test(t, func(t *testing.T) {
		fs.MustRemoveDir(t.Name())
		c := Load(t.Name(), cacheSize)
		defer c.Stop()

		assertMode(t, c, modeSplit)
		assertStats(t, c, fastcache.Stats{})

		synctest.Wait()
		assertCachesMaxSize(t, c, cacheSize/2, cacheSize/2)

		// fill curr cache up to 100% in order to transit it into modeSwitching
		fillCacheToFull(t, c)
		time.Sleep(cacheSizeCheckInterval)
		synctest.Wait()

		assertMode(t, c, modeSwitching)

		// curr cache must use 100% of cache size during modeSwitching
		assertCachesMaxSize(t, c, cacheSize/2, cacheSize)

		// reset cache concurrently, switching into modeWhole must not happen
		c.Reset()
		assertCachesMaxSize(t, c, cacheSize/2, cacheSize/2)
		assertMode(t, c, modeSplit)

		time.Sleep(cacheSizeCheckInterval)
		synctest.Wait()
		assertMode(t, c, modeSplit)

		// fill curr cache up to 100% in order to transit it into modeSwitching
		// instead it should return back to modeSwitching
		fillCacheToFull(t, c)

		time.Sleep(cacheSizeCheckInterval)
		synctest.Wait()
		assertMode(t, c, modeSwitching)

		// curr cache must use 100% of cache size during modeSwitching
		assertCachesMaxSize(t, c, cacheSize/2, cacheSize)

		// fill cache up to 100% in order to transit into modeWhole
		fillCacheToFull(t, c)

		time.Sleep(cacheSizeCheckInterval)
		synctest.Wait()
		assertMode(t, c, modeWhole)

		// curr cache must use 100% of cache size modeWhole
		// prev cache must use minimal amount of memory
		assertCachesMaxSize(t, c, minCacheSize, cacheSize)

		// reset cache, it must return into modeSplit
		c.Reset()
		assertCachesMaxSize(t, c, cacheSize/2, cacheSize/2)
		assertMode(t, c, modeSplit)

		// check if expiration worker operates correctly
		// it must rotate prev and curr

		// add item to curr and check it at prev after rotation
		key, value := []byte(`key1`), []byte(`value1`)
		c.Set(key, value)

		time.Sleep(35 * time.Minute)
		synctest.Wait()
		assertMode(t, c, modeSplit)
		assertCachesMaxSize(t, c, cacheSize/2, cacheSize/2)

		prev := c.prev.Load()
		result := prev.Get(nil, key)
		if string(result) != string(value) {
			t.Fatalf("key=%q must exist at prev cache", string(key))
		}
		curr := c.curr.Load()
		result = curr.Get(result[:0], key)
		if len(result) != 0 {
			t.Fatalf("key=%q must not exist at curr cache after rotation", string(key))
		}

		// check if size watcher operates correctly after Reset
		// fill it and check transition modes
		fillCacheToFull(t, c)
		time.Sleep(cacheSizeCheckInterval)
		assertMode(t, c, modeSwitching)
		fillCacheToFull(t, c)
		time.Sleep(cacheSizeCheckInterval)
		assertMode(t, c, modeWhole)

		assertCachesMaxSize(t, c, minCacheSize, cacheSize)
	})
}

func TestSetGetStatsInSplitMode_newInmemoryCache(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := New(1024)
		defer c.Stop()
		testSetGetStatsInSplitMode(t, c)
	})
}

func TestSetGetStatsInSplitMode_cacheLoadedFromEmptyFile(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := Load(t.Name(), 1024)
		defer c.Stop()
		testSetGetStatsInSplitMode(t, c)
	})
}

func testSetGetStatsInSplitMode(t *testing.T, c *Cache) {
	var (
		k1, v1  = []byte("k1"), []byte("v1")
		k2, v2  = []byte("k2"), []byte("v2")
		kAbsent = []byte("absent")
		dst     []byte
	)

	assertMode(t, c, modeSplit)
	assertStats(t, c, fastcache.Stats{})

	c.Set(k1, v1)
	assertStats(t, c, fastcache.Stats{
		EntriesCount: 1,
		SetCalls:     1,
	})
	c.Set(k2, v2)
	assertStats(t, c, fastcache.Stats{
		EntriesCount: 2,
		SetCalls:     2,
	})

	c.Get(dst[:0], k1)
	assertStats(t, c, fastcache.Stats{
		EntriesCount: 2,
		SetCalls:     2,
		GetCalls:     1,
	})

	c.Get(dst[:0], kAbsent)
	assertStats(t, c, fastcache.Stats{
		EntriesCount: 2,
		SetCalls:     2,
		GetCalls:     2,
		Misses:       1,
	})

	// Wait until prev and curr cache are rotated.
	// k1 and k2 are now in prev, curr is empty.
	time.Sleep(*cacheExpireDuration + time.Minute)
	synctest.Wait()

	c.Get(dst[:0], k1)
	assertStats(t, c, fastcache.Stats{
		EntriesCount: 3,
		SetCalls:     3,
		GetCalls:     3,
		Misses:       1,
	})

	c.Get(dst[:0], k1)
	assertStats(t, c, fastcache.Stats{
		EntriesCount: 3,
		SetCalls:     3,
		GetCalls:     4,
		Misses:       1,
	})

	// Wait until prev and curr caches are rotated. k1 is now in prev, k2 is
	// gone, curr is empty
	time.Sleep(*cacheExpireDuration + time.Minute)
	synctest.Wait()

	c.Get(dst[:0], k2)
	assertStats(t, c, fastcache.Stats{
		EntriesCount: 1,
		SetCalls:     3,
		GetCalls:     5,
		Misses:       2,
	})

	// Wait until prev and curr caches are rotated. The both caches should
	// become empty.
	time.Sleep(*cacheExpireDuration + time.Minute)
	synctest.Wait()

	c.Get(dst[:0], k1)
	assertStats(t, c, fastcache.Stats{
		EntriesCount: 0,
		SetCalls:     3,
		GetCalls:     6,
		Misses:       3,
	})
}

func TestSetBigGetBigStatsInSplitMode_newInmemoryCache(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := New(1024 * 1024)
		defer c.Stop()
		testSetBigGetBigStatsInSplitMode(t, c)
	})
}

func TestSetBigGetBigStatsInSplitMode_cacheLoadedFromEmptyFile(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := Load(t.Name(), 1024*1024)
		defer c.Stop()
		testSetBigGetBigStatsInSplitMode(t, c)
	})
}

func testSetBigGetBigStatsInSplitMode(t *testing.T, c *Cache) {
	const maxSubvalueLen = 64*1024 - 16 - 4 - 1

	v := func(seed, size int) []byte {
		var buf []byte
		for i := range size {
			buf = append(buf, byte(i+seed))
		}
		return buf
	}

	var (
		k1, v1  = []byte("k1"), v(1, maxSubvalueLen-1)
		k2, v2  = []byte("k2"), v(2, maxSubvalueLen)
		k3, v3  = []byte("k3"), v(3, maxSubvalueLen+1)
		k4, v4  = []byte("k4"), v(4, 2*maxSubvalueLen)
		k5, v5  = []byte("k5"), v(5, 2*maxSubvalueLen+1)
		kAbsent = []byte("absent")
		dst     []byte
	)

	assertMode(t, c, modeSplit)
	assertStats(t, c, fastcache.Stats{})

	c.SetBig(k1, v1)
	assertStats(t, c, fastcache.Stats{
		EntriesCount: 2, // SetBig creates at least 2 entries per call.
		SetCalls:     2,
	})
	c.SetBig(k2, v2)
	assertStats(t, c, fastcache.Stats{
		EntriesCount: 4,
		SetCalls:     4,
	})
	c.SetBig(k3, v3)
	assertStats(t, c, fastcache.Stats{
		EntriesCount: 7,
		SetCalls:     7,
	})
	c.SetBig(k4, v4)
	assertStats(t, c, fastcache.Stats{
		EntriesCount: 10,
		SetCalls:     10,
	})
	c.SetBig(k5, v5)
	assertStats(t, c, fastcache.Stats{
		EntriesCount: 14,
		SetCalls:     14,
	})
	c.Get(dst[:0], k1)
	assertStats(t, c, fastcache.Stats{
		EntriesCount: 14,
		SetCalls:     14,
		GetCalls:     1,
	})
	c.Get(dst[:0], k3)
	assertStats(t, c, fastcache.Stats{
		EntriesCount: 14,
		SetCalls:     14,
		GetCalls:     2,
	})
	c.Get(dst[:0], kAbsent)
	assertStats(t, c, fastcache.Stats{
		EntriesCount: 14,
		SetCalls:     14,
		GetCalls:     3,
		Misses:       1,
	})

	// Wait until prev and curr cache are rotated.
	// k1-5 are now in prev, curr is empty.
	time.Sleep(*cacheExpireDuration + time.Minute)
	synctest.Wait()
	assertStats(t, c, fastcache.Stats{
		EntriesCount: 14,
		SetCalls:     14,
		GetCalls:     3,
		Misses:       1,
	})

	c.GetBig(dst[:0], k1)
	assertStats(t, c, fastcache.Stats{
		EntriesCount: 16,
		SetCalls:     16,
		GetCalls:     4,
		Misses:       1,
	})

	c.GetBig(dst[:0], k3)
	assertStats(t, c, fastcache.Stats{
		EntriesCount: 19,
		SetCalls:     19,
		GetCalls:     5,
		Misses:       1,
	})

	c.GetBig(dst[:0], k5)
	assertStats(t, c, fastcache.Stats{
		EntriesCount: 23,
		SetCalls:     23,
		GetCalls:     6,
		Misses:       1,
	})

	// Wait until prev and curr caches are rotated.
	// k1,3,5 are now in prev, k2,4 are gone, curr is empty.
	time.Sleep(*cacheExpireDuration + time.Minute)
	synctest.Wait()
	assertStats(t, c, fastcache.Stats{
		EntriesCount: 9, // 14-2-3
		SetCalls:     23,
		GetCalls:     6,
		Misses:       1,
	})

	c.GetBig(dst[:0], k2)
	assertStats(t, c, fastcache.Stats{
		EntriesCount: 9,
		SetCalls:     23,
		GetCalls:     7,
		Misses:       2,
	})

	c.GetBig(dst[:0], k4)
	assertStats(t, c, fastcache.Stats{
		EntriesCount: 9,
		SetCalls:     23,
		GetCalls:     8,
		Misses:       3,
	})

	// Wait until prev and curr caches are rotated.
	// The both caches should become empty.
	time.Sleep(*cacheExpireDuration + time.Minute)
	synctest.Wait()
	assertStats(t, c, fastcache.Stats{
		EntriesCount: 0,
		SetCalls:     23,
		GetCalls:     8,
		Misses:       3,
	})

	c.GetBig(dst[:0], k1)
	assertStats(t, c, fastcache.Stats{
		EntriesCount: 0,
		SetCalls:     23,
		GetCalls:     9,
		Misses:       4,
	})
}

func TestSetGetStatsInWholeMode_cacheLoadedFromNonEmptyFile(t *testing.T) {
	defer removeAll(t)
	synctest.Test(t, func(t *testing.T) {
		var (
			k1, v1 = []byte("k1"), []byte("v1")
			k2, v2 = []byte("k2"), []byte("v2")
			k3     = []byte("k3")
			dst    []byte
		)

		// Cache loaded from file operates in modeWhole only if the file was
		// not empty.
		c := Load(t.Name(), 1024)
		c.Set(k1, v1)
		c.MustSave(t.Name())
		c.Stop()
		c = Load(t.Name(), 1024)
		defer c.Stop()
		assertMode(t, c, modeWhole)
		assertStats(t, c, fastcache.Stats{
			EntriesCount: 1,
		})

		c.Set(k2, v2)
		assertStats(t, c, fastcache.Stats{
			EntriesCount: 2,
			SetCalls:     1,
		})

		c.Get(dst[:0], k1)
		assertStats(t, c, fastcache.Stats{
			EntriesCount: 2,
			SetCalls:     1,
			GetCalls:     1,
		})
		c.Get(dst[:0], k2)
		assertStats(t, c, fastcache.Stats{
			EntriesCount: 2,
			SetCalls:     1,
			GetCalls:     2,
		})

		c.Get(dst[:0], k3)
		assertStats(t, c, fastcache.Stats{
			EntriesCount: 2,
			SetCalls:     1,
			GetCalls:     3,
			Misses:       1,
		})

		// In modeWhole cache does not expire. Wait for expiration duration
		// anyway to confirm that data is still there.
		time.Sleep(*cacheExpireDuration + time.Minute)
		synctest.Wait()

		c.Get(dst[:0], k1)
		assertStats(t, c, fastcache.Stats{
			EntriesCount: 2,
			SetCalls:     1,
			GetCalls:     4,
			Misses:       1,
		})

		c.Get(dst[:0], k1)
		assertStats(t, c, fastcache.Stats{
			EntriesCount: 2,
			SetCalls:     1,
			GetCalls:     5,
			Misses:       1,
		})

		// In modeWhole cache does not expire. Wait for expiration duration
		// anyway to confirm that data is still there.
		time.Sleep(*cacheExpireDuration + time.Minute)
		synctest.Wait()

		c.Get(dst[:0], k2)
		assertStats(t, c, fastcache.Stats{
			EntriesCount: 2,
			SetCalls:     1,
			GetCalls:     6,
			Misses:       1,
		})

		// In modeWhole cache does not expire. Wait for expiration duration
		// anyway to confirm that data is still there.
		time.Sleep(*cacheExpireDuration + time.Minute)
		synctest.Wait()

		c.Get(dst[:0], k1)
		assertStats(t, c, fastcache.Stats{
			EntriesCount: 2,
			SetCalls:     1,
			GetCalls:     7,
			Misses:       1,
		})

	})
}

func TestSetBigGetBigStatsInWholeMode_cacheLoadedFromNonEmptyFile(t *testing.T) {
	// maxSubvalueLen is used for calculating how many big entries have been copied
	// from previous to current fastcache instance.
	//
	// This value is implementation detail of fastcache (see fastcache/bigcache.go).
	// However it needs to be known here in order to accurately calculate the number
	// of copied entries.
	const maxSubvalueLen = 64*1024 - 16 - 4 - 1

	defer removeAll(t)
	synctest.Test(t, func(t *testing.T) {
		v := func(seed, size int) []byte {
			var buf []byte
			for i := range size {
				buf = append(buf, byte(i+seed))
			}
			return buf
		}

		var (
			k1, v1  = []byte("k1"), v(1, maxSubvalueLen-1)
			k2, v2  = []byte("k2"), v(2, maxSubvalueLen)
			k3, v3  = []byte("k3"), v(3, maxSubvalueLen+1)
			k4, v4  = []byte("k4"), v(4, 2*maxSubvalueLen)
			k5, v5  = []byte("k5"), v(5, 2*maxSubvalueLen+1)
			kAbsent = []byte("absent")
			dst     []byte
		)

		// Cache loaded from file operates in modeWhole only if the file was not empty.
		c := Load(t.Name(), 1024*1024)
		assertMode(t, c, modeSplit)
		c.SetBig(k1, v1)
		assertStats(t, c, fastcache.Stats{
			EntriesCount: 2,
			SetCalls:     2,
		})
		c.MustSave(t.Name())
		c.Stop()
		c = Load(t.Name(), 1024*1024)
		defer c.Stop()
		assertMode(t, c, modeWhole)
		assertStats(t, c, fastcache.Stats{
			EntriesCount: 2,
		})

		c.SetBig(k2, v2)
		assertStats(t, c, fastcache.Stats{
			EntriesCount: 4,
			SetCalls:     2,
		})
		c.SetBig(k3, v3)
		assertStats(t, c, fastcache.Stats{
			EntriesCount: 7,
			SetCalls:     5,
		})
		c.SetBig(k4, v4)
		assertStats(t, c, fastcache.Stats{
			EntriesCount: 10,
			SetCalls:     8,
		})
		c.SetBig(k5, v5)
		assertStats(t, c, fastcache.Stats{
			EntriesCount: 14,
			SetCalls:     12,
		})
		c.GetBig(dst[:0], k1)
		assertStats(t, c, fastcache.Stats{
			EntriesCount: 14,
			SetCalls:     12,
			GetCalls:     2,
		})
		c.GetBig(dst[:0], k3)
		assertStats(t, c, fastcache.Stats{
			EntriesCount: 14,
			SetCalls:     12,
			GetCalls:     5,
		})
		c.GetBig(dst[:0], kAbsent)
		assertStats(t, c, fastcache.Stats{
			EntriesCount: 14,
			SetCalls:     12,
			GetCalls:     6,
			Misses:       1,
		})

		// In modeWhole cache does not expire. Wait for expiration duration
		// anyway to confirm that data is still there.
		time.Sleep(*cacheExpireDuration + time.Minute)
		synctest.Wait()
		assertStats(t, c, fastcache.Stats{
			EntriesCount: 14,
			SetCalls:     12,
			GetCalls:     6,
			Misses:       1,
		})
		c.GetBig(dst[:0], k1)
		assertStats(t, c, fastcache.Stats{
			EntriesCount: 14,
			SetCalls:     12,
			GetCalls:     8,
			Misses:       1,
		})

		c.GetBig(dst[:0], k3)
		assertStats(t, c, fastcache.Stats{
			EntriesCount: 14,
			SetCalls:     12,
			GetCalls:     11,
			Misses:       1,
		})

		c.GetBig(dst[:0], k5)
		assertStats(t, c, fastcache.Stats{
			EntriesCount: 14,
			SetCalls:     12,
			GetCalls:     15,
			Misses:       1,
		})

		// In modeWhole cache does not expire. Wait for expiration duration
		// anyway to confirm that data is still there.
		time.Sleep(*cacheExpireDuration + time.Minute)
		synctest.Wait()
		assertStats(t, c, fastcache.Stats{
			EntriesCount: 14,
			SetCalls:     12,
			GetCalls:     15,
			Misses:       1,
		})

		c.GetBig(dst[:0], k2)
		assertStats(t, c, fastcache.Stats{
			EntriesCount: 14,
			SetCalls:     12,
			GetCalls:     17,
			Misses:       1,
		})

		c.GetBig(dst[:0], k4)
		assertStats(t, c, fastcache.Stats{
			EntriesCount: 14,
			SetCalls:     12,
			GetCalls:     20,
			Misses:       1,
		})

		// In modeWhole cache does not expire. Wait for expiration duration
		// anyway to confirm that data is still there.
		time.Sleep(*cacheExpireDuration + time.Minute)
		synctest.Wait()

		c.GetBig(dst[:0], k1)
		assertStats(t, c, fastcache.Stats{
			EntriesCount: 14,
			SetCalls:     12,
			GetCalls:     22,
			Misses:       1,
		})

	})
}

// assertMode checks that the cache mode matches the expected one.
func assertMode(t *testing.T, c *Cache, want uint32) {
	t.Helper()
	if got := c.mode.Load(); got != want {
		t.Fatalf("unexpected cache mode: got %d, want %d", got, want)
	}
}

// assertMode checks that the cache stats matches the expected one.
func assertStats(t *testing.T, c *Cache, want fastcache.Stats) {
	t.Helper()
	var got fastcache.Stats
	c.UpdateStats(&got)
	ignoreFields := cmpopts.IgnoreFields(fastcache.Stats{}, "BytesSize", "MaxBytesSize")
	if diff := cmp.Diff(want, got, ignoreFields); diff != "" {
		t.Fatalf("unexpected stats (-want, +got):\n%s", diff)
	}
}

// TestGetPromotionAfterConcurrentRotation verifies that when a key is found in
// prev during Get / Has / GetBig, it is promoted into the *current* curr even
// if a cache rotation happens between the miss on curr and the promotion write.
//
// The race scenario:
//
//  1. Get() snapshots curr = C1, then misses on C1.
//  2. The expiration goroutine rotates: C1 → prev, a reset C0 → curr.
//  3. Get() finds the key in prev (C1) and promotes it.
//     BUG (before fix): promotion uses the stale C1 snapshot and writes to C1
//     (which is now prev). The entry is lost at the next rotation.
//     FIXED: promotion calls c.curr.Load() at write time, so it writes to the
//     new curr (C0) and the entry survives.
//
// The test uses beforePromotionHook to inject the rotation deterministically.
func TestGetPromotionAfterConcurrentRotation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		key := []byte("k1")
		value := []byte("v1")

		c := New(1024 * 1024)
		defer c.Stop()
		assertMode(t, c, modeSplit)

		// Put key in curr.
		c.Set(key, value)

		// Rotate: key moves to prev; curr becomes empty.
		time.Sleep(*cacheExpireDuration + time.Minute)
		synctest.Wait()

		// Inject a second rotation just before the promotion write in Get().
		// This simulates the expirationWatcher firing in between.
		c.beforePromotionHook = func() {
			c.beforePromotionHook = nil // fire once
			c.mu.Lock()
			prev := c.prev.Load()
			curr := c.curr.Load()
			c.updateCacheStatsHistoryBeforeRotationLocked(prev, curr)
			c.prev.Store(curr)
			prev.Reset()
			c.curr.Store(prev)
			c.mu.Unlock()
		}

		// Get() should: miss curr (C1), find in prev (C0 which had the key),
		// trigger the hook (rotation: C1→prev, reset C0→curr), then promote
		// into the new curr (C0, now reset) rather than the stale C1.
		result := c.Get(nil, key)
		if string(result) != string(value) {
			t.Fatalf("Get() must return the value; got %q", result)
		}

		// After the promotion the key must be in the new curr (which is now C0,
		// reset by the hook). With the old code the promotion went to C1 (now
		// prev), so it would not be in curr.
		curr := c.curr.Load()
		if got := curr.Get(nil, key); string(got) != string(value) {
			t.Fatalf("promoted key must be in curr after a concurrent rotation; got %q", got)
		}
	})
}

func TestHasPromotionAfterConcurrentRotation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		key := []byte("k1")
		value := []byte("v1")

		c := New(1024 * 1024)
		defer c.Stop()
		assertMode(t, c, modeSplit)

		c.Set(key, value)
		time.Sleep(*cacheExpireDuration + time.Minute)
		synctest.Wait()

		c.beforePromotionHook = func() {
			c.beforePromotionHook = nil
			c.mu.Lock()
			prev := c.prev.Load()
			curr := c.curr.Load()
			c.updateCacheStatsHistoryBeforeRotationLocked(prev, curr)
			c.prev.Store(curr)
			prev.Reset()
			c.curr.Store(prev)
			c.mu.Unlock()
		}

		if !c.Has(key) {
			t.Fatalf("Has() must return true for existing key")
		}

		curr := c.curr.Load()
		if got := curr.Get(nil, key); string(got) != string(value) {
			t.Fatalf("promoted key must be in curr after a concurrent rotation in Has(); got %q", got)
		}
	})
}

func TestGetBigPromotionAfterConcurrentRotation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Use a value large enough to exercise SetBig / GetBig.
		const bigSize = 128 * 1024
		key := []byte("k1")
		value := make([]byte, bigSize)
		for i := range value {
			value[i] = byte(i)
		}

		c := New(4 * 1024 * 1024)
		defer c.Stop()
		assertMode(t, c, modeSplit)

		c.SetBig(key, value)
		time.Sleep(*cacheExpireDuration + time.Minute)
		synctest.Wait()

		c.beforePromotionHook = func() {
			c.beforePromotionHook = nil
			c.mu.Lock()
			prev := c.prev.Load()
			curr := c.curr.Load()
			c.updateCacheStatsHistoryBeforeRotationLocked(prev, curr)
			c.prev.Store(curr)
			prev.Reset()
			c.curr.Store(prev)
			c.mu.Unlock()
		}

		result := c.GetBig(nil, key)
		if string(result) != string(value) {
			t.Fatalf("GetBig() must return the value after concurrent rotation")
		}

		curr := c.curr.Load()
		if got := curr.GetBig(nil, key); string(got) != string(value) {
			t.Fatalf("promoted key must be in curr after a concurrent rotation in GetBig()")
		}
	})
}

// removeAll removes the contents of t.Name() directory if the test succeeded.
// For this to work, a test is expected to store its data in t.Name() dir.
// In case of test failure the directory is not removed to allow for manual
// inspection of the directory.
func removeAll(t *testing.T) {
	if !t.Failed() {
		_ = os.RemoveAll(t.Name())
	}
}

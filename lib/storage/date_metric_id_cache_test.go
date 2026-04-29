package storage

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func newDateMetricIDCacheShard() *dateMetricIDCacheShard {
	var c dateMetricIDCacheShard
	c.prev = newByDateMetricIDMap()
	c.next = newByDateMetricIDMap()
	c.curr.Store(newByDateMetricIDMap())
	return &c
}

func TestDateMetricIDCacheShardSerial(t *testing.T) {
	c := newDateMetricIDCacheShard()
	if err := testDateMetricIDCacheShard(c, false); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
}

func TestDateMetricIDCacheShardConcurrent(t *testing.T) {
	c := newDateMetricIDCacheShard()
	ch := make(chan error, 5)
	for range 5 {
		go func() {
			ch <- testDateMetricIDCacheShard(c, true)
		}()
	}
	for range 5 {
		select {
		case err := <-ch:
			if err != nil {
				t.Fatalf("unexpected error: %s", err)
			}
		case <-time.After(time.Second * 5):
			t.Fatalf("timeout")
		}
	}
}

func testDateMetricIDCacheShard(c *dateMetricIDCacheShard, concurrent bool) error {
	type dmk struct {
		date     uint64
		metricID uint64
	}
	m := make(map[dmk]bool)
	for i := range int(1e5) {
		date := uint64(i) % 2
		metricID := uint64(i) % 1237
		if !concurrent && c.Has(date, metricID) {
			if !m[dmk{date, metricID}] {
				return fmt.Errorf("c.Has(%d, %d) must return false, but returned true", date, metricID)
			}
			continue
		}
		c.Set(date, metricID)
		m[dmk{date, metricID}] = true
		if !concurrent && !c.Has(date, metricID) {
			return fmt.Errorf("c.Has(%d, %d) must return true, but returned false", date, metricID)
		}
		if i%11234 == 0 {
			c.mu.Lock()
			c.syncLocked()
			c.mu.Unlock()
		}
		if i%34323 == 0 {
			// Two rotations are needed to clear the cache.
			c.rotate()
			c.rotate()
			m = make(map[dmk]bool)
		}
	}

	// Verify fast path after sync.
	for i := range int(1e5) {
		date := uint64(i) % 2
		metricID := uint64(i) % 123
		c.Set(date, metricID)
	}
	c.mu.Lock()
	c.syncLocked()
	c.mu.Unlock()
	for i := range int(1e5) {
		date := uint64(i) % 2
		metricID := uint64(i) % 123
		if !concurrent && !c.Has(date, metricID) {
			return fmt.Errorf("c.Has(%d, %d) must return true after sync", date, metricID)
		}
	}

	// Verify that cache becomes empty after two rotations.
	if n := c.Stats().Size; !concurrent && n < 123 {
		return fmt.Errorf("c.EntriesCount must return at least 123; returned %d", n)
	}
	c.rotate()
	if n := c.Stats().Size; !concurrent && n < 123 {
		return fmt.Errorf("c.EntriesCount must return at least 123; returned %d", n)
	}
	c.rotate()
	if n := c.Stats().Size; !concurrent && n > 0 {
		return fmt.Errorf("c.EntriesCount must return 0 after reset; returned %d", n)
	}
	return nil
}

func TestDateMetricIDCacheIsConsistent(_ *testing.T) {
	const (
		generation  = 1
		date        = 1
		concurrency = 2
		numMetrics  = 100000
	)
	dmc := newDateMetricIDCache()
	defer dmc.MustStop()
	var wg sync.WaitGroup
	for i := range concurrency {
		wg.Go(func() {
			for id := uint64(i * numMetrics); id < uint64((i+1)*numMetrics); id++ {
				dmc.Set(date, id)
				if !dmc.Has(date, id) {
					panic(fmt.Errorf("dmc.Has(metricID=%d): unexpected cache miss after adding the entry to cache", id))
				}
			}
		})
	}
	wg.Wait()
}

// TestDateMetricIDCacheStatsIncludesMutablePart verifies that Stats().Size
// accounts for entries that have been Set() but not yet synced from the mutable
// "next" map into the immutable "curr" map.
//
// Before commit dc5d7aa4c (properly report dateMetricIDCache stats, fixes
// issue #10064) Stats() only reported entries in the immutable part.  Entries
// in the mutable "next" part were invisible until the next sync, which could
// make the cache appear empty even when it held thousands of entries, giving
// misleading vm_cache_entries and vm_cache_size_bytes metrics.
func TestDateMetricIDCacheStatsIncludesMutablePart(t *testing.T) {
	dmc := newDateMetricIDCache()
	defer dmc.MustStop()

	const (
		date     = 12345
		n        = 1000
		firstID  = uint64(0)
	)

	// Add entries; they land in the mutable "next" map, not yet in "curr".
	for i := range uint64(n) {
		dmc.Set(date, firstID+i)
	}

	// Stats must immediately reflect all entries, not just the immutable part.
	stats := dmc.Stats()
	if stats.Size < n {
		t.Fatalf("Stats().Size should be >= %d immediately after Set(); got %d — mutable part is not being counted", n, stats.Size)
	}
}

// TestDateMetricIDCacheRotationPeriodAtLeastOneHour verifies that the shard
// rotation period is at least 30 minutes.
//
// Commit cd2e11b7c (increase rotation time for daily metricID cache, fixes
// issue #10064) raised the rotation period from 10 minutes to 1 hour because
// the short rotation caused index pre-created for the next day to fall out of
// cache before midnight, resulting in CPU spikes.  This test guards against
// a regression back to the old 10-minute (or shorter) period.
func TestDateMetricIDCacheRotationPeriodAtLeastOneHour(t *testing.T) {
	dmc := newDateMetricIDCache()
	defer dmc.MustStop()

	const minPeriod = 30 * time.Minute
	if dmc.rotationPeriod < minPeriod {
		t.Fatalf("rotationPeriod is %v; want >= %v — regression from cd2e11b7c which raised it to 1h to avoid midnight CPU spikes (issue #10064)", dmc.rotationPeriod, minPeriod)
	}
}

// TestDateMetricIDCacheStatsSizeBytesNonZero verifies that Stats().SizeBytes
// is greater than zero after entries have been added and synced into the
// immutable "curr" map.
//
// Commit dc5d7aa4c (properly report dateMetricIDCache stats, issue #10064)
// fixed Stats() to report SizeBytes for both mutable and immutable parts.
// Before that fix, SizeBytes was always 0 until entries were synced, giving
// misleading utilisation metrics.
func TestDateMetricIDCacheStatsSizeBytesNonZero(t *testing.T) {
	dmc := newDateMetricIDCache()
	defer dmc.MustStop()

	const (
		date     = 99999
		n        = 500
	)

	for i := range uint64(n) {
		dmc.Set(date, i)
	}

	stats := dmc.Stats()
	if stats.SizeBytes == 0 {
		t.Fatalf("Stats().SizeBytes must be > 0 after Set(); got 0 — mutable part SizeBytes is not being counted (regression of dc5d7aa4c)")
	}
}

func TestDateMetricIDCache_Size(t *testing.T) {
	dmc := newDateMetricIDCache()
	defer dmc.MustStop()
	for i := range 100_000 {
		date := 12345 + uint64(i%30)
		metricID := uint64(i)
		dmc.Set(date, metricID)

		if got, want := dmc.Stats().Size, uint64(i+1); got != want {
			t.Fatalf("unexpected size: got %d, want %d", got, want)
		}
	}

	// Retrieve all entries and check the cache size again.
	for i := range 100_000 {
		date := 12345 + uint64(i%30)
		metricID := uint64(i)
		if !dmc.Has(date, metricID) {
			t.Fatalf("entry not in cache: (date=%d, metricID=%d)", date, metricID)
		}
	}
	if got, want := dmc.Stats().Size, uint64(100_000); got != want {
		t.Fatalf("unexpected size: got %d, want %d", got, want)
	}
}

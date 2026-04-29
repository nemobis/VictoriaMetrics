# Regression Testing Guide for VictoriaMetrics Contributors

A practical guide for writing regression tests when fixing bugs in VictoriaMetrics.
Extracted from the existing test suite and from analysis of the last two years of
bug-fix commits.

---

## Table of Contents

1. [Why write regression tests for bug fixes?](#1-why-write-regression-tests-for-bug-fixes)
2. [Core patterns found in the existing test suite](#2-core-patterns-found-in-the-existing-test-suite)
   - [Pattern A — State-machine / rotation boundary tests](#pattern-a--state-machine--rotation-boundary-tests)
   - [Pattern B — "Stats must reflect all parts" tests](#pattern-b--stats-must-reflect-all-parts-tests)
   - [Pattern C — Sequence / stateful-flow tests](#pattern-c--sequencestateful-flow-tests)
   - [Pattern D — Cache-key correctness tests](#pattern-d--cache-key-correctness-tests)
   - [Pattern E — Token / resource-accounting tests](#pattern-e--token--resource-accounting-tests)
   - [Pattern F — synctest deterministic-time tests](#pattern-f--synctest-deterministic-time-tests)
3. [What to include in every regression test](#3-what-to-include-in-every-regression-test)
4. [Commit-analysis appendix](#4-commit-analysis-appendix)

---

## 1. Why write regression tests for bug fixes?

VictoriaMetrics' git history shows a clear pattern: the same areas break
repeatedly.  In the past two years (April 2024 – April 2026) at least **1 079**
commits matched patterns like `fix`, `bug`, `race`, `panic`, `deadlock`,
`incorrect`, `perf`, or `slow`.  Of those, a significant fraction were
*regressions* — bugs that had been fixed before but crept back.

A regression test:
- Proves the bug existed before the fix (it should fail on the unfixed code).
- Documents the *intended behaviour* in executable form.
- Catches the same mistake if it is re-introduced, even months later.

---

## 2. Core patterns found in the existing test suite

### Pattern A — State-machine / rotation boundary tests

Many bugs happen at the boundary of a state transition (cache rotation,
mode switch, shard rotation).  Tests that force a rotation and assert invariants
immediately after have caught the most regressions.

**Archetype:**
```go
// TestResetRestoresSplitModeAndWorkers — lib/workingsetcache/cache_synctest_test.go
// Source: commit cea9505ba, PR #9769
//
// Reset() in modeSwitching left expirationWatcher returning early;
// subsequent rotations and mode transitions never fired.
synctest.Test(t, func(t *testing.T) {
    c := workingsetcache.New(maxBytes)

    // Drive cache to modeSwitching by filling >90 % of curr.
    fillCache(c, ...)

    // Reset while in switching mode.
    c.Reset()

    // Advance time past one full rotation period.
    time.Sleep(2 * workingsetcache.RotationInterval)
    synctest.Wait()

    // After reset the rotation goroutine must still be alive.
    var s workingsetcache.Stats
    c.UpdateStats(&s)
    if s.GetCalls == 0 {
        t.Fatal("cache appears dead after Reset in modeSwitching")
    }
})
```

**Where to use:** Any time a `Reset()`, `Stop()`, or config-reload path
touches long-lived goroutines or swaps internal pointers.

---

### Pattern B — "Stats must reflect all parts" tests

Several bugs caused metrics (`vm_cache_entries`, `vm_cache_size_bytes`, …) to
show 0 even when the cache held data, because only one of the two or three
internal data structures was counted.

**Archetype:**
```go
// TestDateMetricIDCacheStatsIncludesMutablePart
//   — lib/storage/date_metric_id_cache_test.go
// Source: commit dc5d7aa4c, issue #10064
//
// Stats() only counted the immutable "curr" map; entries in the mutable
// "next" map were invisible until the next sync.
func TestDateMetricIDCacheStatsIncludesMutablePart(t *testing.T) {
    dmc := newDateMetricIDCache()
    defer dmc.MustStop()

    for i := range uint64(1000) {
        dmc.Set(date, i) // lands in the mutable "next" map
    }

    stats := dmc.Stats()
    if stats.Size < 1000 {
        t.Fatalf("Stats().Size=%d; want >=1000 — mutable part not counted "+
            "(regression of dc5d7aa4c)", stats.Size)
    }
}
```

**Where to use:** Any cache with a multi-layer structure (curr/prev/next,
mutable/immutable, in-memory/on-disk) where stats are computed separately.
Always check that stats reflect data immediately after `Set`, not only
after a sync/flush.

---

### Pattern C — Sequence / stateful-flow tests

Some bugs only manifest after a specific sequence of operations.  Write the
test as a numbered sequence of steps with assertions after each step.

**Archetype:**
```go
// TestScrapeWorkLastScrapeOnlyUpdatedOnSuccess
//   — lib/promscrape/scrapework_test.go
// Source: commit 0a256002e, issue #10653
//
// Previously the last-scrape body was overwritten with "" on failure,
// so a subsequent successful scrape could not detect disappeared metrics.
func TestScrapeWorkLastScrapeOnlyUpdatedOnSuccess(t *testing.T) {
    sw := &scrapeWork{Config: &ScrapeWork{}}

    // Step 1: successful scrape with metric_a + metric_b.
    doScrape(sw, "metric_a 1\nmetric_b 2\n", /*fail=*/false)

    // Step 2: failed scrape — lastScrape must NOT be cleared.
    doScrape(sw, "", /*fail=*/true)
    if sw.loadLastScrape(nil) == nil {
        t.Fatal("last scrape cleared on failure — regression of 0a256002e")
    }

    // Step 3: successful scrape with only metric_b.
    // Stale marker for metric_a must be emitted (diff against step 1).
    result := doScrape(sw, "metric_b 3\n", /*fail=*/false)
    if countStaleMarkers(result) == 0 {
        t.Fatal("no stale marker for disappeared metric_a")
    }
}
```

**Where to use:** Any time correct behaviour depends on state accumulated
across multiple calls (e.g. scrape history, previous query result, cache
warm-up).  Number your steps in comments.

---

### Pattern D — Cache-key correctness tests

Cache bugs often involve keys that are too narrow (missing a field) or that
collide across tenants/arguments.

**Archetype:**
```go
// TestRollupResultCacheCacheTagFiltersKey
//   — app/vmselect/promql/rollup_result_cache_test.go
// Source: commit f9015da6e, issue #9001
//
// extra_filters / tenant labels were excluded from the cache key;
// queries from different tenants could share cached results.
func TestRollupResultCacheCacheTagFiltersKey(t *testing.T) {
    key1 := rollupResultCacheKey{extraFilters: `{tenant="A"}`}
    key2 := rollupResultCacheKey{extraFilters: `{tenant="B"}`}
    if key1.Marshal() == key2.Marshal() {
        t.Fatal("different extra_filters must produce different cache keys "+
            "(regression of f9015da6e, issue #9001)")
    }
}
```

**Where to use:** Any change to a cache-key struct.  Enumerate every field
that must distinguish entries and add a sub-test for each.

---

### Pattern E — Token / resource-accounting tests

Semaphores, reference counts, and token pools must be balanced: every
acquire must have exactly one release.  Tests for these are white-box and
live in the same package as the code under test.

**Archetype:**
```go
// TestReaderPutReaderDoesNotLeakTokenAfterFailedIncConcurrency
//   — lib/writeconcurrencylimiter/concurrencylimiter_test.go
// Source: commit d07c1c73d, issue #10784
//
// PutReader unconditionally called DecConcurrency even after Read()
// had already released the token; slots leaked until deadlock.
func TestReaderPutReaderDoesNotLeakTokenAfterFailedIncConcurrency(t *testing.T) {
    origCh := concurrencyLimitCh
    defer func() { concurrencyLimitCh = origCh }()
    concurrencyLimitCh = make(chan struct{}, 2)

    // Simulate state after Read() released the token and IncConcurrency failed.
    r := &Reader{r: bytes.NewReader(nil), increasedConcurrency: false}

    // Fill channel to capacity to simulate other goroutines.
    concurrencyLimitCh <- struct{}{}
    concurrencyLimitCh <- struct{}{}
    initialLen := len(concurrencyLimitCh)

    PutReader(r) // must NOT drain the channel

    if len(concurrencyLimitCh) != initialLen {
        t.Fatal("token leaked (regression of d07c1c73d)")
    }
    <-concurrencyLimitCh
    <-concurrencyLimitCh
}
```

**Technique:** Swap the global semaphore channel with a test-controlled one
in the same package, then verify `len(ch)` before and after.  Always
`defer` the restore.

---

### Pattern F — synctest deterministic-time tests

Bugs that only appear when a goroutine rotation fires between two operations
(e.g. a cache rotation between a cache miss and a promotion write) are
impossible to trigger reliably with real clocks.  Use Go's
`testing/synctest` package (Go ≥ 1.25) and an injectable hook.

**Archetype:**
```go
// TestGetPromotionAfterConcurrentRotation
//   — lib/workingsetcache/cache_synctest_test.go
// Source: commit 86d8748ee (fix/workingsetcache-stale-curr-promotion)
//
// Get() snapshotted curr at entry and promoted into the stale snapshot
// after a rotation; promoted entries went into the new prev and were
// wiped at the next rotation.
synctest.Test(t, func(t *testing.T) {
    c := workingsetcache.New(maxBytes)
    c.Set(key, value)
    // Advance time so the entry migrates to prev.
    time.Sleep(workingsetcache.RotationInterval + 1)
    synctest.Wait()

    // Inject a rotation between the prev-hit and the promotion write.
    c.BeforePromotionHook = func() {
        time.Sleep(workingsetcache.RotationInterval + 1)
        synctest.Wait()
    }

    result := c.Get(nil, key)
    if result == nil {
        t.Fatal("entry lost after concurrent rotation during promotion")
    }

    // Verify the entry landed in the *new* curr, not in prev.
    c.BeforePromotionHook = nil
    time.Sleep(workingsetcache.RotationInterval + 1)
    synctest.Wait()
    if c.Get(nil, key) == nil {
        t.Fatal("promoted entry not in curr; will be lost at next rotation")
    }
})
```

**When to use:** Any race that requires goroutine interleaving at a
specific point.  Add a `beforeXxxHook func()` field (nil in production) to
the struct under test and call the hook at the critical point.  Use
`synctest.Wait()` to drain all goroutines between injected sleeps.

---

## 3. What to include in every regression test

| Element | Why |
|---------|-----|
| **Doc comment with commit ID** | Allows `git show <id>` to see the original bug description. |
| **Issue/PR reference** | Links to the full discussion and reproduction steps. |
| **One-sentence description of the original bug** | Helps future readers understand what would break if the test failed. |
| **Failure message that names the regression** | e.g. `"regression of commit abc1234"` — makes CI failures self-explanatory. |
| **Minimal setup** | Only create what is needed to exercise the bug; keep tests fast. |
| **Cleanup** | Use `defer testRemoveAll(t)` / `defer c.Stop()` / channel restore, etc. |

**Template:**

```go
// TestFooBarDoesNotWibble verifies that <one sentence summary>.
//
// Before commit <hash> (<subject>), <what went wrong>.
// <Optional: issue / PR reference>.
func TestFooBarDoesNotWibble(t *testing.T) {
    // Arrange: minimal fixture.
    ...

    // Act.
    result := SomeFunc(input)

    // Assert.
    if result != expected {
        t.Fatalf("SomeFunc(%v) = %v; want %v (regression of commit <hash>)", input, result, expected)
    }
}
```

---

## 4. Commit-analysis appendix

The table below lists the files with the highest density of bug-fix commits
in the last year (April 2024 – April 2026) together with the number of test
functions present before this work.  Files are sorted by the ratio of
fix commits to test functions — higher means more undertested.

### Files in `lib/` and `app/` with most fix commits vs. tests

| File | Fix commits (1y) | Test functions (before) | Representative untested bugs |
|------|-----------------|------------------------|------------------------------|
| `lib/workingsetcache/cache.go` | 12 | 0 (before PR) | Stale curr on promotion (`86d8748ee`), stats underflow (`f0b08dbd9`), Reset in switching mode (`cea9505ba`) |
| `lib/storage/index_db.go` | 46 | 12 | UpdateMetrics zero-cache stats (`f0b251d96`, `ab1429c89`), double-counting `vm_deleted_metrics_total` (`df7b752c7`) |
| `lib/storage/storage.go` | 49 | 44 | Cardinality limiter perf regression (`f668e5d9c`), data race at startFreeDiskSpaceWatcher (`f95b483a1`) |
| `lib/streamaggr/streamaggr.go` | 22 | 2 | Threshold update with dedup+windows (`511517f49`) |
| `lib/storage/partition.go` | 22 | 9 | Empty merge result panic (`4e50d6eed`) — now tested |
| `app/vmagent/remotewrite/remotewrite.go` | 22 | 4 | Nil dedup panic on shutdown (`7dc18bf67`) |
| `app/vmselect/promql/eval.go` | 19 | 5 | `@` modifier with NaN timestamp panic (`7dfaef908`) |
| `lib/mergeset/table.go` | 21 | 7 | Deadlock on panic during merge (`2a0e382a9`), inmemoryPart refCount (`2bb03f6e3`) |
| `lib/promscrape/scrapework.go` | 25 | 8 | Last scrape cleared on failure (`0a256002e`) — now tested |
| `lib/writeconcurrencylimiter/concurrencylimiter.go` | 1 | 0 (before PR) | Token leak causing deadlock (`d07c1c73d`) — now tested |
| `app/vmauth/main.go` | 33 | 9 | Various auth-config bugs |
| `lib/httpserver/httpserver.go` | 17 | 5 | CORS preflight not short-circuited (`686c9a21f`) — now tested |
| `lib/blockcache/blockcache.go` | 2 | 2 | `cleanPerKeyMisses` wiped pending counters (`9b6a2f914`) — now tested |
| `app/vmselect/promql/rollup_result_cache.go` | 18 | 3 | Multi-arg function key (`3e09d38f2`), tenant key (`f9015da6e`) — now tested |
| `lib/storage/date_metric_id_cache.go` | 3 | 4 | Stats undercount (`dc5d7aa4c`), rotation period too short (`cd2e11b7c`) — now tested |

### Selected bug summaries (no test at time of fix)

| Commit | File | Bug summary |
|--------|------|-------------|
| `d07c1c73d` | `lib/writeconcurrencylimiter` | `PutReader` always decremented the semaphore even after `Read()` had already released the token; slots leaked to zero causing permanent deadlock on vmstorage. Fix: track `increasedConcurrency` per Reader. |
| `0a256002e` | `lib/promscrape/scrapework.go` | Last-scrape body overwritten with `""` on scrape failure; subsequent successful scrapes could not detect disappeared metrics, omitting stale markers. |
| `f0b251d96` | `lib/storage/index_db.go` | `UpdateMetrics` for per-indexDB caches used `SizeBytes > m.SizeBytes` (both 0 → condition always false); `SizeMaxBytes` was never written when all caches were empty. |
| `ab1429c89` | `lib/storage/index_db.go` | Follow-up: used `SizeBytes == 0` as "is this the first instance?" indicator, but `tagFiltersCache` is frequently reset (SizeBytes goes to 0), so later instances could overwrite gauges already set. |
| `511517f49` | `lib/streamaggr/streamaggr.go` | During initial flush with deduplication+windows enabled, `minDeadline` was set to the NEXT dedup-interval upper bound; all samples in subsequent intervals were silently ignored. |
| `f668e5d9c` | `lib/storage/storage.go` | Cardinality limiter exceeded → index lookup performed for every series regardless, reversing the limiter's purpose; fix moves the cardinality check before the index lookup. |
| `2a0e382a9` | `lib/storage/partition.go` + `lib/mergeset/table.go` | A panic inside a merge goroutine left the merge mutex permanently locked, deadlocking all future merges. |
| `f95b483a1` | `lib/storage/storage.go` | `startFreeDiskSpaceWatcher` was called before `s.table` was initialised; if `openTable` was slow, the watcher could read a nil/partial `table` pointer. |
| `7dc18bf67` | `app/vmagent/remotewrite` | `deduplicatorGlobal.MustStop()` called unconditionally on shutdown even when global deduplication was not configured (`deduplicatorGlobal == nil`). |
| `7dfaef908` | `app/vmselect/promql/eval.go` | `selector @ another_selector` extracted the timestamp via `int64(NaN*1000)` when `another_selector` had no data before `start`; behaviour is platform-dependent and can panic or produce max-int64. |
| `4e50d6eed` | `lib/storage/partition.go` | `mustMergeInmemoryPartsFinal` returned a zero-blocks part when retention filters removed all rows; callers panicked dereferencing the empty part. Test was added with the fix. |
| `2bb03f6e3` | `lib/storage/partition.go` + `lib/mergeset/table.go` | `inmemoryPart` reference count could go negative under concurrent merge + removal, causing use-after-free. |

### Guidance on priority

When deciding which bugs to write tests for first, use this ranking:

1. **Behavioural correctness bugs** that are silent (wrong results, no panic) —
   highest risk of reintroduction without detection.
2. **Panic/nil-dereference bugs** — easy to trigger, high visible impact.
3. **Metric reporting bugs** — affect alerting and dashboards; regressions often
   go unnoticed for a long time.
4. **Performance regressions** — important but harder to express as deterministic
   unit tests; prefer benchmarks or integration tests.
5. **Race conditions** — use `synctest` hooks where possible; otherwise add a
   `-race` integration test with a sleep-based probe as a fallback.

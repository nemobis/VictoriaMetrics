# VictoriaMetrics Cache Analysis & Bug Fixes

A systematic investigation of VictoriaMetrics' in-memory caching subsystem,
covering architecture, identified bugs, applied fixes, and regression test
coverage derived from git history analysis.

---

## Table of Contents

1. [Cache Architecture Overview](#1-cache-architecture-overview)
2. [Why Memory Is Sometimes Left Unused](#2-why-memory-is-sometimes-left-unused)
3. [Bugs Found & Fixed](#3-bugs-found--fixed)
   - [Bug 1 — blockcache: pending admission counters wiped on periodic cleanup](#bug-1--blockcache-pending-admission-counters-wiped-on-periodic-cleanup)
   - [Bug 2 — workingsetcache stats underflow (false alarm)](#bug-2--workingsetcache-stats-underflow-false-alarm)
   - [Bug 3 — workingsetcache: stale curr pointer in Get / Has / GetBig](#bug-3--workingsetcache-stale-curr-pointer-in-get--has--getbig)
4. [Test Validation](#4-test-validation)
5. [Git-History Analysis: Files Most in Need of Tests](#5-git-history-analysis-files-most-in-need-of-tests)
6. [Regression Tests Added (branch: test/regression-cache-bugs)](#6-regression-tests-added-branch-testregression-cache-bugs)
7. [User Prompts](#7-user-prompts)

---

## 1. Cache Architecture Overview

vmstorage uses two distinct caching layers that together determine how much
disk I/O is avoided.

### 1.1 `lib/workingsetcache` — working-set cache

Wraps two [fastcache](https://github.com/VictoriaMetrics/fastcache) instances
(`curr` and `prev`) to implement a sliding-window eviction policy.

| Mode | Description |
|------|-------------|
| `modeSplit` | curr and prev each hold ½ of `maxBytes`. Rotation every ~30 min moves curr→prev and resets a new curr. Entries in prev are promoted to curr on access. |
| `modeSwitching` | Triggered when curr exceeds 90 % capacity. curr is expanded to full `maxBytes`; prev is kept read-only for promotion. |
| `modeWhole` | curr uses 100 % of `maxBytes`; prev is empty. Entered from modeSwitching when curr again exceeds 90 %. |

The `prevCacheWatcher` goroutine drops prev early if it serves <0.1 % of
requests over a 60-second window, reclaiming memory before the next rotation.

**Registered caches (and their RAM allocations as a fraction of total):**

| Cache | Purpose | Size |
|-------|---------|------|
| `storage/tsid` | MetricName → TSID lookup | 37 % |
| `storage/metricID` | MetricID set membership | 6.25 % |
| `storage/metricName` | MetricID → MetricName | 10 % |
| `storage/indexBlocks` | Index block cache (storage layer) | 10 % |
| `indexdb/dataBlocks` | Merged-set data block cache | 25 % |
| `indexdb/indexBlocks` | Merged-set index block cache | 10 % |
| `indexdb/dataBlocksSparse` | Sparse data block cache | 5 % |
| `promql/rollupResult` | Query result cache (vmselect) | 1/16 of RAM |

### 1.2 `lib/blockcache` — admission-controlled block cache

LRU cache sharded by CPU count × min(CPUs, 16).  Before admitting a block it
requires `missesBeforeCaching` (default 2) cache misses, so one-time-wonder
blocks never evict hot entries.

### 1.3 No application-level data block cache

`MustReadBlock()` in `lib/storage/search.go` reads timestamp/value blocks
**directly from disk** on every call.  Repeated reads of the same data are
served entirely by the OS page cache, not by any VictoriaMetrics layer.

---

## 2. Why Memory Is Sometimes Left Unused

1. **Split-mode headroom** — In `modeSplit` each half-cache is intentionally
   kept at ½ of the configured limit to leave room for the working set of the
   next rotation window.

2. **prevCacheWatcher eviction** — If a workload changes abruptly, prev can be
   dropped long before the next scheduled rotation, leaving the freed half-cache
   unreferenced until the next allocation.

3. **Admission gate in blockcache** — Blocks that are read fewer than
   `missesBeforeCaching+1` times never enter the cache, so transient scans do
   not pollute it. This can make the cache appear underutilised under bursty
   workloads.

4. **fastcache minimum granularity** — fastcache rounds allocations up to
   internal chunk boundaries (~32 MiB minimum), so small configured limits
   result in apparent over-provisioning while the stated `SizeMaxBytes` is
   lower.

---

## 3. Bugs Found & Fixed

### Bug 1 — blockcache: pending admission counters wiped on periodic cleanup

**Branch:** `fix/blockcache-never-cached-on-periodic-access`
**Commit:** `9b6a2f914`

#### Root cause

`cache.cleanPerKeyMisses()` unconditionally replaced the `perKeyMisses` map
with a fresh empty map every ~3 minutes, discarding all pending miss counts.
A block that was accessed at intervals slightly longer than the admission
threshold (e.g. every 2 minutes with `missesBeforeCaching=2`) would never
accumulate enough misses to be admitted — it was read from disk every single
time, forever.

A secondary issue: `RemoveBlocksForPart()` did not clean `perKeyMisses`
entries belonging to the removed part, keeping a reference to the closed
part's memory alive in the GC for up to 3 minutes.

#### Fix

`cleanPerKeyMisses` now preserves entries with `0 < misses <= threshold`
(blocks that have missed at least once but are not yet admitted). Entries with
`misses == 0` (already cached and reset after admission) are dropped.

`RemoveBlocksForPart` now iterates `perKeyMisses` and removes all entries
whose `Key.Part` matches the removed part.

#### Side effects

- Slightly higher memory usage for `perKeyMisses` between cleanup cycles
  (pending counters are now retained).
- Blocks that had their counter reset by `cleanPerKeyMisses` and were then
  removed via `RemoveBlocksForPart` are re-admitted after only
  `missesBeforeCaching` more misses instead of `2×missesBeforeCaching`.

---

### Bug 2 — workingsetcache stats underflow (false alarm)

**Branch:** `fix/workingsetcache-stats-underflow`
No code changes.

`updateCacheStatsHistoryBeforeRotationLocked` contains:

```go
atomic.AddUint64(&csHistory.Misses, csPrev.Misses - csCurr.Misses)
```

This was initially identified as a uint64 underflow bug.  Investigation
revealed it is **intentional modular arithmetic**: every `Get()` miss on curr
also queries prev, so both miss counters increment together.  At rotation time
`csPrev.Misses - csCurr.Misses` (mod 2⁶⁴) added to `csCurr.Misses` via
`UpdateStats` correctly recovers the true prev-only miss count.  Changing this
to saturating subtraction broke existing tests.

---

### Bug 3 — workingsetcache: stale curr pointer in Get / Has / GetBig

**Branch:** `fix/workingsetcache-stale-curr-promotion`
**Commit:** `86d8748ee`

#### Root cause

All three functions snapshot `curr := c.curr.Load()` at entry, then reuse
that snapshot when promoting an entry found in `prev` back into the live cache:

```go
curr := c.curr.Load()          // snapshot at T₀
result := curr.Get(dst, key)   // miss on curr
...
result  = prev.Get(dst, key)   // hit in prev
curr.Set(key, result[len(dst):]) // promote — but curr may be stale!
```

If `expirationWatcher` rotates the caches between the miss on curr and the
promotion write (rotating old-curr → prev and a reset cache → curr), the
promoted entry lands in the old curr (which is now prev) and is wiped at the
next rotation.  The practical effect is that frequently-accessed entries whose
access interval straddles a rotation boundary are never promoted to curr and
must be re-read from disk every rotation cycle (~30 minutes).

#### Fix

Replace the stale `curr.Set/SetBig(...)` calls with fresh
`c.curr.Load().Set/SetBig(...)` calls at the point of promotion in `Get`,
`Has`, and `GetBig`.

A `beforePromotionHook` field (nil in production) was added to `Cache` to
allow synctest tests to inject a rotation at exactly the right moment.

#### Side effects

- One extra atomic load per promoted entry (negligible).
- Promoted entries now survive one extra rotation cycle (~30 min) in the race
  scenario, improving cache hit rates for entries accessed around rotation time.

---

## 4. Test Validation

All three fix branches were validated with `make test-full` and `make check-all`
inside a `golang:1.26.2` container (`golangci-lint v2.9.0`, `govulncheck`):

| Branch | test-full | golangci-lint | govulncheck |
|--------|-----------|---------------|-------------|
| `fix/blockcache-never-cached-on-periodic-access` | ✅ PASS | ✅ 0 issues | ✅ no vulns |
| `fix/workingsetcache-stats-underflow` | ✅ PASS | ✅ 0 issues | ✅ no vulns |
| `fix/workingsetcache-stale-curr-promotion` | ✅ PASS | ✅ 0 issues | ✅ no vulns |

---

## 5. Git-History Analysis: Files Most in Need of Tests

The following ranking was produced by counting commits whose message matched
`fix|bug|race|panic|deadlock|corrupt|overflow|leak` for each `.go` source file
(excluding docs, comments, and mechanical go-fix commits), then cross-
referencing with the number of `func Test…` functions in the corresponding
`_test.go` file.

### All files

| File | Fix commits | Tests | Fix rate |
|------|-------------|-------|----------|
| `lib/storage/storage.go` | 107 | 44 | 35 % |
| `lib/storage/index_db.go` | 84 | 12 | — |
| `app/vmselect/prometheus/prometheus.go` | 71 | — | — |
| `lib/promscrape/config.go` | 68 | 13 | — |
| `lib/storage/partition.go` | 59 | 9 | — |
| `lib/promscrape/scrapework.go` | 56 | 8 | — |
| `lib/mergeset/table.go` | 54 | 7 | — |
| `app/vmagent/remotewrite/remotewrite.go` | 52 | 4 | — |
| `app/vmselect/promql/eval.go` | 44 | 5 | — |

### Cache files specifically

| File | Fix commits | Tests (before this work) | Notes |
|------|-------------|--------------------------|-------|
| `lib/workingsetcache/cache.go` | 17 | **0** | Worst ratio; every fix was behavioral |
| `app/vmselect/promql/rollup_result_cache.go` | 18 | 3 | Multi-tenant key bugs, marshal panics |
| `lib/storage/date_metric_id_cache.go` | 3 | 4 | 75 % of all commits were fixes |
| `lib/blockcache/blockcache.go` | 2 | 2 | Fixed in this work |
| `lib/lrucache/lrucache.go` | 1 | 2 | Low risk |
| `lib/storage/metric_id_cache.go` | 0 | 2 | No bug history |
| `app/vmselect/promql/parse_cache.go` | 0 | 2 | No churn |

---

## 6. Regression Tests Added (branch: `test/regression-cache-bugs`)

### Commits

- `ab6495aa9` — first batch (7 tests)
- `36dbb31b9` — second batch (3 tests)

### `lib/workingsetcache/cache_synctest_test.go`

| Test | Bug | Source commit |
|------|-----|---------------|
| `TestStatsByteSizeNonZeroAfterSet` | BytesSize reported as 0 even when cache held entries; remained 0 after rotation when entries moved to prev | `04c24fc83`, `3e6fc445a` |
| `TestStatsCountersNeverDecreaseAfterRotation` | GetCalls/SetCalls/Misses dropped to zero after rotation, corrupting the cache-hit-ratio Grafana panel that uses `rate(vm_cache_misses_total)` | `f0b08dbd9` |
| `TestResetRestoresSplitModeAndWorkers` | Reset() in modeSwitching left expirationWatcher returning early instead of continuing; subsequent rotations and mode transitions never fired | `cea9505ba`, PR #9769 |

### `app/vmselect/promql/rollup_result_cache_test.go`

| Test | Bug | Source commit / issue |
|------|-----|-----------------------|
| `TestRollupResultCacheMultiArgFuncKey` | Only the first argument was included in the cache key for multi-arg functions; `quantile_over_time(0.99, …)` and `quantile_over_time(0.95, …)` shared the same entry | `3e09d38f2` |
| `TestRollupResultCacheCacheTagFiltersKey` | `extra_filters` / tenant labels were excluded from the cache key; queries from different tenants could return each other's cached results | `f9015da6e`, issue #9001 |
| `TestRollupResultCacheInitStopWithDisableCache` | When `-search.disableCache=true`, `InitRollupResultCache` called `c.Stop()` but did not nil the pointer; `StopRollupResultCache` then called `c.Stop()` again → panic | `90a4b00b1` |
| `TestResetRollupResultCacheInvalidatesEntries` | After `ResetRollupResultCache()` all previously cached entries must be inaccessible; tests the prefix-counter mechanism that replaced the slower full `c.Reset()` | `92531a38c` |

### `lib/storage/date_metric_id_cache_test.go`

| Test | Bug | Source commit / issue |
|------|-----|-----------------------|
| `TestDateMetricIDCacheStatsIncludesMutablePart` | `Stats()` only counted the immutable `curr` map; entries in the mutable `next` map were invisible until the next sync, making `vm_cache_entries` appear near-zero after ingestion bursts | `dc5d7aa4c`, issue #10064 |
| `TestDateMetricIDCacheRotationPeriodAtLeastOneHour` | Shard rotation period was 10 minutes; next-day index records pre-created 1 hour before midnight expired before midnight, causing CPU spikes at 00:00 | `cd2e11b7c`, issue #10064 |
| `TestDateMetricIDCacheStatsSizeBytesNonZero` | `Stats().SizeBytes` was 0 even when entries existed in the mutable part, giving misleading memory-utilisation metrics | `dc5d7aa4c`, issue #10064 |

---

## 7. User Prompts

The following are the verbatim prompts provided by the human during this
session.  They are reproduced here so that the human contribution to this
analysis can be distinguished from the automated work.

---

> **Prompt 1**
>
> Can you describe how VictoriaMetrics decides what to cache in memory,
> especially for vmstorage, to reduce the amount of reads from disk? Why is
> memory sometimes left unused? Can you find any bugs in the implementation
> which would cause some data to be read over and over again from disk?

---

> **Prompt 2**
>
> For each of these bugs, can you create a branch, add tests to verify the
> behaviour and prove the current code is wrong, and then propose a patch to
> fix it, create a git commit which explains the issue briefly and how it's
> fixed and what could be some side effects of the fix?

---

> **Prompt 3** *(interruption during tool use)*
>
> can you run the tests with podman

---

> **Prompt 4** *(interruption during tool use — correcting tooling approach)*
>
> The Makefile in this project has some suggestions on how to run tests

---

> **Prompt 5**
>
> Now in all three branches make sure there were no negative side effects by
> running all tests in a podman container. To run tests and code checks
> locally, execute commands `make test-full` and `make check-all`.

---

> **Prompt 6** *(interruption — rejecting a host-side wrapper script)*
>
> This script would install go packages in my home. I told you to only run
> these commands in podman containers.

---

> **Prompt 7** *(interruption — rejecting git worktree creation)*
>
> just do it one by one

---

> **Prompt 8**
>
> Ok. Now can you recap again the files which were in direst needs of
> additional tests?

---

> **Prompt 9** *(clarification)*
>
> I meant earlier when you looked at git history to find frequent sources of
> bugs and bug fixes

---

> **Prompt 10**
>
> Now extract a description of the expected behaviour from commit messages for
> all the bug fixes you found, create a branch and add tests for all of them.
> Make sure to add code comments which explain where the expected behaviour was
> determined from (git commit ID or pull request/issue ID).

---

> **Prompt 11** *(sent while work was in progress)*
>
> Can you come up with more tests to make sure we have enough test coverage to
> avoid regressions for all the bug fixes you found?

---

> **Prompt 12**
>
> Create a markdown file which summarises this information and include a
> section with all the prompts I gave in this session, so that the human
> contribution can be inspected

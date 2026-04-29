package promql

import (
	"fmt"
	"testing"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/fs"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/storage"
	"github.com/VictoriaMetrics/metricsql"
)

func TestRollupResultCacheInitStop(t *testing.T) {
	t.Run("inmemory", func(_ *testing.T) {
		for range 5 {
			InitRollupResultCache("")
			StopRollupResultCache()
		}
	})
	t.Run("file-based", func(_ *testing.T) {
		cacheFilePath := "test-rollup-result-cache"
		for range 3 {
			InitRollupResultCache(cacheFilePath)
			StopRollupResultCache()
		}
		fs.MustRemoveDir(cacheFilePath)
		fs.MustRemovePath(cacheFilePath + ".key.prefix")
	})
}

func TestRollupResultCache(t *testing.T) {
	InitRollupResultCache("")
	defer StopRollupResultCache()

	ResetRollupResultCache()
	window := int64(456)
	ec := &EvalConfig{
		Start:              1000,
		End:                2000,
		Step:               200,
		MaxPointsPerSeries: 1e4,

		MayCache: true,
	}
	me := &metricsql.MetricExpr{
		LabelFilterss: [][]metricsql.LabelFilter{
			{
				{
					Label: "aaa",
					Value: "xxx",
				},
			},
		},
	}
	fe := &metricsql.FuncExpr{
		Name: "foo",
		Args: []metricsql.Expr{me},
	}
	ae := &metricsql.AggrFuncExpr{
		Name: "foobar",
		Args: []metricsql.Expr{fe},
	}

	// Try obtaining an empty value.
	t.Run("empty", func(t *testing.T) {
		tss, newStart := rollupResultCacheV.GetSeries(nil, ec, fe, window)
		if newStart != ec.Start {
			t.Fatalf("unexpected newStart; got %d; want %d", newStart, ec.Start)
		}
		if len(tss) != 0 {
			t.Fatalf("got %d timeseries, while expecting zero", len(tss))
		}
	})

	// Store timeseries overlapping with start
	t.Run("start-overlap-no-ae", func(t *testing.T) {
		ResetRollupResultCache()
		tss := []*timeseries{
			{
				Timestamps: []int64{800, 1000, 1200},
				Values:     []float64{0, 1, 2},
			},
		}
		rollupResultCacheV.PutSeries(nil, ec, fe, window, tss)
		tss, newStart := rollupResultCacheV.GetSeries(nil, ec, fe, window)
		if newStart != 1400 {
			t.Fatalf("unexpected newStart; got %d; want %d", newStart, 1400)
		}
		tssExpected := []*timeseries{
			{
				Timestamps: []int64{1000, 1200},
				Values:     []float64{1, 2},
			},
		}
		testTimeseriesEqual(t, tss, tssExpected)
	})
	t.Run("start-overlap-with-ae", func(t *testing.T) {
		ResetRollupResultCache()
		tss := []*timeseries{
			{
				Timestamps: []int64{800, 1000, 1200},
				Values:     []float64{0, 1, 2},
			},
		}
		rollupResultCacheV.PutSeries(nil, ec, ae, window, tss)
		tss, newStart := rollupResultCacheV.GetSeries(nil, ec, ae, window)
		if newStart != 1400 {
			t.Fatalf("unexpected newStart; got %d; want %d", newStart, 1400)
		}
		tssExpected := []*timeseries{
			{
				Timestamps: []int64{1000, 1200},
				Values:     []float64{1, 2},
			},
		}
		testTimeseriesEqual(t, tss, tssExpected)
	})

	// Store timeseries overlapping with end
	t.Run("end-overlap", func(t *testing.T) {
		ResetRollupResultCache()
		tss := []*timeseries{
			{
				Timestamps: []int64{1800, 2000, 2200, 2400},
				Values:     []float64{333, 0, 1, 2},
			},
		}
		rollupResultCacheV.PutSeries(nil, ec, fe, window, tss)
		tss, newStart := rollupResultCacheV.GetSeries(nil, ec, fe, window)
		if newStart != 1000 {
			t.Fatalf("unexpected newStart; got %d; want %d", newStart, 1000)
		}
		if len(tss) != 0 {
			t.Fatalf("got %d timeseries, while expecting zero", len(tss))
		}
	})

	// Store timeseries covered by [start ... end]
	t.Run("full-cover", func(t *testing.T) {
		ResetRollupResultCache()
		tss := []*timeseries{
			{
				Timestamps: []int64{1200, 1400, 1600},
				Values:     []float64{0, 1, 2},
			},
		}
		rollupResultCacheV.PutSeries(nil, ec, fe, window, tss)
		tss, newStart := rollupResultCacheV.GetSeries(nil, ec, fe, window)
		if newStart != 1000 {
			t.Fatalf("unexpected newStart; got %d; want %d", newStart, 1000)
		}
		if len(tss) != 0 {
			t.Fatalf("got %d timeseries, while expecting zero", len(tss))
		}
	})

	// Store timeseries below start
	t.Run("before-start", func(t *testing.T) {
		ResetRollupResultCache()
		tss := []*timeseries{
			{
				Timestamps: []int64{200, 400, 600},
				Values:     []float64{0, 1, 2},
			},
		}
		rollupResultCacheV.PutSeries(nil, ec, fe, window, tss)
		tss, newStart := rollupResultCacheV.GetSeries(nil, ec, fe, window)
		if newStart != 1000 {
			t.Fatalf("unexpected newStart; got %d; want %d", newStart, 1000)
		}
		if len(tss) != 0 {
			t.Fatalf("got %d timeseries, while expecting zero", len(tss))
		}
	})

	// Store timeseries after end
	t.Run("after-end", func(t *testing.T) {
		ResetRollupResultCache()
		tss := []*timeseries{
			{
				Timestamps: []int64{2200, 2400, 2600},
				Values:     []float64{0, 1, 2},
			},
		}
		rollupResultCacheV.PutSeries(nil, ec, fe, window, tss)
		tss, newStart := rollupResultCacheV.GetSeries(nil, ec, fe, window)
		if newStart != 1000 {
			t.Fatalf("unexpected newStart; got %d; want %d", newStart, 1000)
		}
		if len(tss) != 0 {
			t.Fatalf("got %d timeseries, while expecting zero", len(tss))
		}
	})

	// Store timeseries bigger than the interval [start ... end]
	t.Run("bigger-than-start-end", func(t *testing.T) {
		ResetRollupResultCache()
		tss := []*timeseries{
			{
				Timestamps: []int64{800, 1000, 1200, 1400, 1600, 1800, 2000, 2200},
				Values:     []float64{0, 1, 2, 3, 4, 5, 6, 7},
			},
		}
		rollupResultCacheV.PutSeries(nil, ec, fe, window, tss)
		tss, newStart := rollupResultCacheV.GetSeries(nil, ec, fe, window)
		if newStart != 2200 {
			t.Fatalf("unexpected newStart; got %d; want %d", newStart, 2200)
		}
		tssExpected := []*timeseries{
			{
				Timestamps: []int64{1000, 1200, 1400, 1600, 1800, 2000},
				Values:     []float64{1, 2, 3, 4, 5, 6},
			},
		}
		testTimeseriesEqual(t, tss, tssExpected)
	})

	// Store timeseries matching the interval [start ... end]
	t.Run("start-end-match", func(t *testing.T) {
		ResetRollupResultCache()
		tss := []*timeseries{
			{
				Timestamps: []int64{1000, 1200, 1400, 1600, 1800, 2000},
				Values:     []float64{1, 2, 3, 4, 5, 6},
			},
		}
		rollupResultCacheV.PutSeries(nil, ec, fe, window, tss)
		tss, newStart := rollupResultCacheV.GetSeries(nil, ec, fe, window)
		if newStart != 2200 {
			t.Fatalf("unexpected newStart; got %d; want %d", newStart, 2200)
		}
		tssExpected := []*timeseries{
			{
				Timestamps: []int64{1000, 1200, 1400, 1600, 1800, 2000},
				Values:     []float64{1, 2, 3, 4, 5, 6},
			},
		}
		testTimeseriesEqual(t, tss, tssExpected)
	})

	// Store big timeseries, so their marshaled size exceeds 64Kb.
	t.Run("big-timeseries", func(t *testing.T) {
		ResetRollupResultCache()
		var tss []*timeseries
		for i := range 1000 {
			ts := &timeseries{
				Timestamps: []int64{1000, 1200, 1400, 1600, 1800, 2000},
				Values:     []float64{1, 2, 3, 4, 5, 6},
			}
			ts.MetricName.MetricGroup = fmt.Appendf(nil, "metric %d", i)
			tss = append(tss, ts)
		}
		rollupResultCacheV.PutSeries(nil, ec, fe, window, tss)
		tssResult, newStart := rollupResultCacheV.GetSeries(nil, ec, fe, window)
		if newStart != 2200 {
			t.Fatalf("unexpected newStart; got %d; want %d", newStart, 2200)
		}
		testTimeseriesEqual(t, tssResult, tss)
	})

	// Store series with identical naming (they shouldn't be stored)
	t.Run("duplicate-series", func(t *testing.T) {
		ResetRollupResultCache()
		tss := []*timeseries{
			{
				Timestamps: []int64{800, 1000, 1200},
				Values:     []float64{0, 1, 2},
			},
			{
				Timestamps: []int64{800, 1000, 1200},
				Values:     []float64{0, 1, 2},
			},
		}
		rollupResultCacheV.PutSeries(nil, ec, fe, window, tss)
		tssResult, newStart := rollupResultCacheV.GetSeries(nil, ec, fe, window)
		if newStart != ec.Start {
			t.Fatalf("unexpected newStart; got %d; want %d", newStart, ec.Start)
		}
		if len(tssResult) != 0 {
			t.Fatalf("unexpected non-empty series returned")
		}
	})

	// Store multiple time series
	t.Run("multi-timeseries", func(t *testing.T) {
		ResetRollupResultCache()
		tss1 := []*timeseries{
			{
				Timestamps: []int64{800, 1000, 1200},
				Values:     []float64{0, 1, 2},
			},
		}
		tss2 := []*timeseries{
			{
				Timestamps: []int64{1800, 2000, 2200, 2400},
				Values:     []float64{333, 0, 1, 2},
			},
		}
		tss3 := []*timeseries{
			{
				Timestamps: []int64{1200, 1400, 1600},
				Values:     []float64{0, 1, 2},
			},
		}
		rollupResultCacheV.PutSeries(nil, ec, fe, window, tss1)
		rollupResultCacheV.PutSeries(nil, ec, fe, window, tss2)
		rollupResultCacheV.PutSeries(nil, ec, fe, window, tss3)
		tss, newStart := rollupResultCacheV.GetSeries(nil, ec, fe, window)
		if newStart != 1400 {
			t.Fatalf("unexpected newStart; got %d; want %d", newStart, 1400)
		}
		tssExpected := []*timeseries{
			{
				Timestamps: []int64{1000, 1200},
				Values:     []float64{1, 2},
			},
		}
		testTimeseriesEqual(t, tss, tssExpected)
	})

}

// TestRollupResultCacheMultiArgFuncKey verifies that two calls to a multi-arg
// rollup function with different argument values produce separate cache entries
// and do not return results for each other.
//
// Before commit 3e09d38f2 (fix results caching for multi-arg rollup functions
// such as quantile_over_time) only the first argument of the function was
// included in the cache key, so quantile_over_time(0.99, ...) and
// quantile_over_time(0.95, ...) shared the same key and would incorrectly
// return each other's cached results.
func TestRollupResultCacheMultiArgFuncKey(t *testing.T) {
	InitRollupResultCache("")
	defer StopRollupResultCache()
	ResetRollupResultCache()

	ec := &EvalConfig{
		Start:              1000,
		End:                2000,
		Step:               200,
		MaxPointsPerSeries: 1e4,
		MayCache:           true,
	}
	window := int64(500)

	// Build quantile_over_time(0.99, foo) and quantile_over_time(0.95, foo).
	metricExpr := &metricsql.MetricExpr{
		LabelFilterss: [][]metricsql.LabelFilter{{{Label: "__name__", Value: "foo"}}},
	}
	makeQuantile := func(q float64) *metricsql.FuncExpr {
		return &metricsql.FuncExpr{
			Name: "quantile_over_time",
			Args: []metricsql.Expr{
				&metricsql.NumberExpr{N: q},
				metricExpr,
			},
		}
	}
	expr99 := makeQuantile(0.99)
	expr95 := makeQuantile(0.95)

	tss99 := []*timeseries{{
		Timestamps: []int64{1000, 1200, 1400, 1600, 1800, 2000},
		Values:     []float64{99, 99, 99, 99, 99, 99},
	}}

	// Store result for quantile 0.99.
	rollupResultCacheV.PutSeries(nil, ec, expr99, window, tss99)

	// Getting quantile 0.99 must hit.
	gotTss, newStart := rollupResultCacheV.GetSeries(nil, ec, expr99, window)
	if newStart == ec.Start {
		t.Fatalf("expected cache hit for expr99 (newStart should be > ec.Start); got newStart=%d ec.Start=%d", newStart, ec.Start)
	}
	_ = gotTss

	// Getting quantile 0.95 must miss — before commit 3e09d38f2 this would
	// incorrectly hit the 0.99 entry.
	_, newStart = rollupResultCacheV.GetSeries(nil, ec, expr95, window)
	if newStart != ec.Start {
		t.Fatalf("expected cache miss for expr95 (different quantile arg); got newStart=%d, want %d", newStart, ec.Start)
	}
}

// TestRollupResultCacheCacheTagFiltersKey verifies that two queries that differ
// only in their CacheTagFilters (extra_filters / tenant labels) produce
// separate cache entries.
//
// Before commit f9015da6e (include extra_filters in rollupCache key for
// multi-tenant support, fixes issue #9001) CacheTagFilters were not included
// in the cache key, so queries from different tenants could incorrectly share
// cached results, leaking data across tenant boundaries.
func TestRollupResultCacheCacheTagFiltersKey(t *testing.T) {
	InitRollupResultCache("")
	defer StopRollupResultCache()
	ResetRollupResultCache()

	ec := &EvalConfig{
		Start:              1000,
		End:                2000,
		Step:               200,
		MaxPointsPerSeries: 1e4,
		MayCache:           true,
	}
	window := int64(500)
	expr := &metricsql.FuncExpr{
		Name: "rate",
		Args: []metricsql.Expr{
			&metricsql.MetricExpr{
				LabelFilterss: [][]metricsql.LabelFilter{{{Label: "__name__", Value: "requests"}}},
			},
		},
	}

	tenant1Filters := [][]storage.TagFilter{{{Key: []byte("vm_account_id"), Value: []byte("1")}}}
	tenant2Filters := [][]storage.TagFilter{{{Key: []byte("vm_account_id"), Value: []byte("2")}}}

	ecTenant1 := *ec
	ecTenant1.CacheTagFilters = tenant1Filters
	ecTenant2 := *ec
	ecTenant2.CacheTagFilters = tenant2Filters

	tssTenant1 := []*timeseries{{
		Timestamps: []int64{1000, 1200, 1400, 1600, 1800, 2000},
		Values:     []float64{1, 1, 1, 1, 1, 1},
	}}

	// Cache result for tenant 1.
	rollupResultCacheV.PutSeries(nil, &ecTenant1, expr, window, tssTenant1)

	// Getting with tenant 1 filters must hit.
	_, newStart := rollupResultCacheV.GetSeries(nil, &ecTenant1, expr, window)
	if newStart == ec.Start {
		t.Fatalf("expected cache hit for tenant1; got newStart=%d ec.Start=%d", newStart, ec.Start)
	}

	// Getting with tenant 2 filters must miss — before commit f9015da6e this
	// would incorrectly return tenant 1's data.
	_, newStart = rollupResultCacheV.GetSeries(nil, &ecTenant2, expr, window)
	if newStart != ec.Start {
		t.Fatalf("expected cache miss for tenant2 (different CacheTagFilters); got newStart=%d, want %d", newStart, ec.Start)
	}
}

// TestRollupResultCacheInitStopWithDisableCache verifies that cycling
// InitRollupResultCache / StopRollupResultCache does not panic when the
// -search.disableCache flag is set.
//
// Before commit 90a4b00b1 (fix panic on -search.disableCache),
// InitRollupResultCache called c.Stop() on the internal cache when
// disableCache=true, but did not set c to nil.  StopRollupResultCache then
// called c.Stop() a second time, which panicked because the cache's stop
// channel had already been closed.
func TestRollupResultCacheInitStopWithDisableCache(t *testing.T) {
	old := *disableCache
	*disableCache = true
	defer func() { *disableCache = old }()

	// Must not panic.
	for range 3 {
		InitRollupResultCache("")
		StopRollupResultCache()
	}
}

func TestMergeSeries(t *testing.T) {
	ec := &EvalConfig{
		Start:              1000,
		End:                2000,
		Step:               200,
		MaxPointsPerSeries: 1e4,
	}
	bStart := int64(1400)

	t.Run("bStart=ec.Start", func(t *testing.T) {
		a := []*timeseries{}
		b := []*timeseries{
			{
				Timestamps: []int64{1000, 1200, 1400, 1600, 1800, 2000},
				Values:     []float64{1, 2, 3, 4, 5, 6},
			},
		}
		tss, ok := mergeSeries(nil, a, b, 1000, ec)
		if !ok {
			t.Fatalf("unexpected failure to merge series")
		}
		tssExpected := []*timeseries{
			{
				Timestamps: []int64{1000, 1200, 1400, 1600, 1800, 2000},
				Values:     []float64{1, 2, 3, 4, 5, 6},
			},
		}
		testTimeseriesEqual(t, tss, tssExpected)
	})
	t.Run("a-empty", func(t *testing.T) {
		a := []*timeseries{}
		b := []*timeseries{
			{
				Timestamps: []int64{1400, 1600, 1800, 2000},
				Values:     []float64{3, 4, 5, 6},
			},
		}
		tss, ok := mergeSeries(nil, a, b, bStart, ec)
		if !ok {
			t.Fatalf("unexpected failure to merge series")
		}
		tssExpected := []*timeseries{
			{
				Timestamps: []int64{1000, 1200, 1400, 1600, 1800, 2000},
				Values:     []float64{nan, nan, 3, 4, 5, 6},
			},
		}
		testTimeseriesEqual(t, tss, tssExpected)
	})
	t.Run("b-empty", func(t *testing.T) {
		a := []*timeseries{
			{
				Timestamps: []int64{1000, 1200},
				Values:     []float64{2, 1},
			},
		}
		b := []*timeseries{}
		tss, ok := mergeSeries(nil, a, b, bStart, ec)
		if !ok {
			t.Fatalf("unexpected failure to merge series")
		}
		tssExpected := []*timeseries{
			{
				Timestamps: []int64{1000, 1200, 1400, 1600, 1800, 2000},
				Values:     []float64{2, 1, nan, nan, nan, nan},
			},
		}
		testTimeseriesEqual(t, tss, tssExpected)
	})
	t.Run("non-empty", func(t *testing.T) {
		a := []*timeseries{
			{
				Timestamps: []int64{1000, 1200},
				Values:     []float64{2, 1},
			},
		}
		b := []*timeseries{
			{
				Timestamps: []int64{1400, 1600, 1800, 2000},
				Values:     []float64{3, 4, 5, 6},
			},
		}
		tss, ok := mergeSeries(nil, a, b, bStart, ec)
		if !ok {
			t.Fatalf("unexpected failure to merge series")
		}
		tssExpected := []*timeseries{
			{
				Timestamps: []int64{1000, 1200, 1400, 1600, 1800, 2000},
				Values:     []float64{2, 1, 3, 4, 5, 6},
			},
		}
		testTimeseriesEqual(t, tss, tssExpected)
	})
	t.Run("non-empty-distinct-metric-names", func(t *testing.T) {
		a := []*timeseries{
			{
				Timestamps: []int64{1000, 1200},
				Values:     []float64{2, 1},
			},
		}
		a[0].MetricName.MetricGroup = []byte("bar")
		b := []*timeseries{
			{
				Timestamps: []int64{1400, 1600, 1800, 2000},
				Values:     []float64{3, 4, 5, 6},
			},
		}
		b[0].MetricName.MetricGroup = []byte("foo")
		tss, ok := mergeSeries(nil, a, b, bStart, ec)
		if !ok {
			t.Fatalf("unexpected failure to merge series")
		}
		tssExpected := []*timeseries{
			{
				MetricName: storage.MetricName{
					MetricGroup: []byte("foo"),
				},
				Timestamps: []int64{1000, 1200, 1400, 1600, 1800, 2000},
				Values:     []float64{nan, nan, 3, 4, 5, 6},
			},
			{
				MetricName: storage.MetricName{
					MetricGroup: []byte("bar"),
				},
				Timestamps: []int64{1000, 1200, 1400, 1600, 1800, 2000},
				Values:     []float64{2, 1, nan, nan, nan, nan},
			},
		}
		testTimeseriesEqual(t, tss, tssExpected)
	})
	t.Run("duplicate-series-a", func(t *testing.T) {
		a := []*timeseries{
			{
				Timestamps: []int64{1000, 1200},
				Values:     []float64{2, 1},
			},
			{
				Timestamps: []int64{1000, 1200},
				Values:     []float64{3, 3},
			},
		}
		b := []*timeseries{
			{
				Timestamps: []int64{1400, 1600, 1800, 2000},
				Values:     []float64{3, 4, 5, 6},
			},
		}
		tss, ok := mergeSeries(nil, a, b, bStart, ec)
		if ok {
			t.Fatalf("expecting failure to merge series")
		}
		testTimeseriesEqual(t, tss, nil)
	})
	t.Run("duplicate-series-b", func(t *testing.T) {
		a := []*timeseries{
			{
				Timestamps: []int64{1000, 1200},
				Values:     []float64{1, 2},
			},
		}
		b := []*timeseries{
			{
				Timestamps: []int64{1400, 1600, 1800, 2000},
				Values:     []float64{3, 4, 5, 6},
			},
			{
				Timestamps: []int64{1400, 1600, 1800, 2000},
				Values:     []float64{13, 14, 15, 16},
			},
		}
		tss, ok := mergeSeries(nil, a, b, bStart, ec)
		if ok {
			t.Fatalf("expecting failure to merge series")
		}
		testTimeseriesEqual(t, tss, nil)
	})
}

func testTimeseriesEqual(t *testing.T, tss, tssExpected []*timeseries) {
	t.Helper()
	if len(tss) != len(tssExpected) {
		t.Fatalf(`unexpected timeseries count; got %d; want %d`, len(tss), len(tssExpected))
	}
	for i, ts := range tss {
		tsExpected := tssExpected[i]
		testMetricNamesEqual(t, &ts.MetricName, &tsExpected.MetricName, i)
		testRowsEqual(t, ts.Values, ts.Timestamps, tsExpected.Values, tsExpected.Timestamps)
	}
}

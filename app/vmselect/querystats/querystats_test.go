package querystats

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

// resetQueryStats resets the package-level singleton so each test starts fresh.
func resetQueryStats(recordsCount int) {
	initOnce = sync.Once{}
	qsTracker = nil
	*lastQueriesCount = recordsCount
}

func TestEnabled(t *testing.T) {
	// With positive count, Enabled() should return true.
	resetQueryStats(100)
	if !Enabled() {
		t.Fatal("expected Enabled() == true when lastQueriesCount > 0")
	}

	// With zero count, Enabled() should return false.
	resetQueryStats(0)
	if Enabled() {
		t.Fatal("expected Enabled() == false when lastQueriesCount == 0")
	}
}

func TestRegisterQueryAndWriteJSONQueryStats(t *testing.T) {
	resetQueryStats(100)
	// minQueryDuration defaults to 1ms, minQueryMemoryUsage defaults to 1024.
	// Use a startTime far enough in the past so duration > minQueryDuration.
	startTime := time.Now().Add(-100 * time.Millisecond)
	RegisterQuery("test_query{}", 60_000, startTime, 2048)

	var buf bytes.Buffer
	WriteJSONQueryStats(&buf, 10, time.Hour)
	output := buf.String()

	if !strings.Contains(output, `"topN":"10"`) {
		t.Errorf("expected topN in output; got: %s", output)
	}
	if !strings.Contains(output, "topByCount") {
		t.Errorf("expected topByCount in output; got: %s", output)
	}
	if !strings.Contains(output, "topByAvgDuration") {
		t.Errorf("expected topByAvgDuration in output; got: %s", output)
	}
	if !strings.Contains(output, "topBySumDuration") {
		t.Errorf("expected topBySumDuration in output; got: %s", output)
	}
	if !strings.Contains(output, "topByAvgMemoryUsage") {
		t.Errorf("expected topByAvgMemoryUsage in output; got: %s", output)
	}
	if !strings.Contains(output, "test_query") {
		t.Errorf("expected registered query in output; got: %s", output)
	}
}

func TestRegisterQuery_BelowMinDuration(t *testing.T) {
	resetQueryStats(100)
	// Use a startTime in the future so duration < 0 < minQueryDuration.
	startTime := time.Now().Add(time.Hour)
	RegisterQuery("skipped_query{}", 60_000, startTime, 2048)

	var buf bytes.Buffer
	WriteJSONQueryStats(&buf, 10, time.Hour)
	output := buf.String()

	if strings.Contains(output, "skipped_query") {
		t.Errorf("query with negative duration should not be tracked; got: %s", output)
	}
}

func TestRegisterQuery_BelowMinMemoryUsage(t *testing.T) {
	resetQueryStats(100)
	// Memory usage below 1024 should be skipped.
	startTime := time.Now().Add(-100 * time.Millisecond)
	RegisterQuery("low_mem_query{}", 60_000, startTime, 10)

	var buf bytes.Buffer
	WriteJSONQueryStats(&buf, 10, time.Hour)
	output := buf.String()

	if strings.Contains(output, "low_mem_query") {
		t.Errorf("query with low memory usage should not be tracked; got: %s", output)
	}
}

func TestRegisterQuery_ExpiredMaxLifetime(t *testing.T) {
	resetQueryStats(100)
	startTime := time.Now().Add(-200 * time.Millisecond)
	RegisterQuery("expired_query{}", 60_000, startTime, 2048)

	var buf bytes.Buffer
	// maxLifetime of 1 nanosecond: registered entries should be expired.
	WriteJSONQueryStats(&buf, 10, time.Nanosecond)
	output := buf.String()

	if strings.Contains(output, "expired_query") {
		t.Errorf("expired query should not appear in stats; got: %s", output)
	}
}

func TestRegisterQuery_MultipleQueries_TopN(t *testing.T) {
	resetQueryStats(200)
	startTime := time.Now().Add(-100 * time.Millisecond)

	// Register 5 distinct queries.
	queries := []string{"q1{}", "q2{}", "q3{}", "q4{}", "q5{}"}
	for _, q := range queries {
		RegisterQuery(q, 60_000, startTime, 2048)
	}

	// Request topN=3 — output should contain at most 3 entries per category.
	var buf bytes.Buffer
	WriteJSONQueryStats(&buf, 3, time.Hour)
	output := buf.String()

	if !strings.Contains(output, "topByCount") {
		t.Errorf("expected topByCount in output; got: %s", output)
	}
}

func TestRegisterQuery_SameQueryMultipleTimes(t *testing.T) {
	resetQueryStats(100)
	startTime := time.Now().Add(-100 * time.Millisecond)

	// Register the same query 3 times.
	for i := 0; i < 3; i++ {
		RegisterQuery("repeated_query{}", 30_000, startTime, 4096)
	}

	var buf bytes.Buffer
	WriteJSONQueryStats(&buf, 10, time.Hour)
	output := buf.String()

	if !strings.Contains(output, "repeated_query") {
		t.Errorf("expected repeated_query in output; got: %s", output)
	}
	// Count=3 should appear.
	if !strings.Contains(output, `"count":3`) {
		t.Errorf("expected count 3 for repeated query; got: %s", output)
	}
}

func TestRegisterQuery_CircularBuffer(t *testing.T) {
	// Use a small ring buffer of size 2.
	resetQueryStats(2)
	startTime := time.Now().Add(-100 * time.Millisecond)

	// Register 3 queries — the oldest should be overwritten.
	RegisterQuery("old_query{}", 60_000, startTime, 2048)
	RegisterQuery("new_query1{}", 60_000, startTime, 2048)
	RegisterQuery("new_query2{}", 60_000, startTime, 2048)

	var buf bytes.Buffer
	WriteJSONQueryStats(&buf, 10, time.Hour)
	output := buf.String()

	// old_query was overwritten; new queries should be present.
	if strings.Contains(output, "old_query") {
		t.Errorf("old_query should have been overwritten in circular buffer; got: %s", output)
	}
}

func TestWriteJSONQueryStats_EmptyTracker(t *testing.T) {
	resetQueryStats(100)

	var buf bytes.Buffer
	WriteJSONQueryStats(&buf, 10, time.Hour)
	output := buf.String()

	// Should produce valid (empty) JSON structure.
	if !strings.HasPrefix(output, `{"topN":`) {
		t.Errorf("expected JSON object output; got: %s", output)
	}
	if !strings.HasSuffix(output, `}`) {
		t.Errorf("expected output to end with }; got: %s", output)
	}
}

func TestRegisterQuery_TimeRangeConversion(t *testing.T) {
	resetQueryStats(100)
	startTime := time.Now().Add(-100 * time.Millisecond)

	// timeRangeMsecs = 120000 => timeRangeSecs = 120
	RegisterQuery("range_query{}", 120_000, startTime, 2048)

	var buf bytes.Buffer
	WriteJSONQueryStats(&buf, 10, time.Hour)
	output := buf.String()

	if !strings.Contains(output, `"timeRangeSeconds":120`) {
		t.Errorf("expected timeRangeSeconds=120; got: %s", output)
	}
}

func TestWriteJSONQueryStats_ConcurrentAccess(t *testing.T) {
	resetQueryStats(500)
	var wg sync.WaitGroup

	// Concurrent writers.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			startTime := time.Now().Add(-50 * time.Millisecond)
			RegisterQuery("concurrent_query{}", 60_000, startTime, 2048)
		}()
	}

	// Concurrent readers.
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var buf bytes.Buffer
			WriteJSONQueryStats(&buf, 5, time.Hour)
		}()
	}

	wg.Wait()
}

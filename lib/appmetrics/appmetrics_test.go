package appmetrics

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// TestWritePrometheusMetrics_ContainsExpectedMetrics verifies that WritePrometheusMetrics
// writes output containing the well-known vm_* gauge metrics.
func TestWritePrometheusMetrics_ContainsExpectedMetrics(t *testing.T) {
	var buf bytes.Buffer
	WritePrometheusMetrics(&buf)
	output := buf.String()

	if len(output) == 0 {
		t.Fatal("WritePrometheusMetrics wrote empty output")
	}

	mustContain := []string{
		"vm_app_version{",
		"vm_allowed_memory_bytes",
		"vm_available_memory_bytes",
		"vm_available_cpu_cores",
		"vm_gogc",
		"vm_app_start_timestamp",
		"vm_app_uptime_seconds",
	}
	for _, s := range mustContain {
		if !strings.Contains(output, s) {
			t.Errorf("expected metric %q not found in output", s)
		}
	}
}

// TestWritePrometheusMetrics_FlagMetrics verifies that flag metrics are present.
func TestWritePrometheusMetrics_FlagMetrics(t *testing.T) {
	var buf bytes.Buffer
	WritePrometheusMetrics(&buf)
	output := buf.String()

	// The writePrometheusMetrics function always exports at least one flag
	// (the -version flag from buildinfo), so "flag{name=" must appear.
	if !strings.Contains(output, `flag{name=`) {
		t.Error(`expected "flag{name=" in output but not found`)
	}
}

// TestWritePrometheusMetrics_Caching verifies that rapid back-to-back calls
// return identical bytes (the 1-second cache must serve the same snapshot).
func TestWritePrometheusMetrics_Caching(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	WritePrometheusMetrics(&buf1)
	WritePrometheusMetrics(&buf2)
	if !bytes.Equal(buf1.Bytes(), buf2.Bytes()) {
		t.Error("expected identical output from two rapid consecutive calls (cache hit), but got different output")
	}
}

// TestWritePrometheusMetrics_CacheExpiry verifies that after more than 1 second
// the metrics cache is refreshed and vm_app_uptime_seconds increments.
func TestWritePrometheusMetrics_CacheExpiry(t *testing.T) {
	// Force the cache to be considered stale so the next call always
	// regenerates it from scratch.
	metricsCacheLock.Lock()
	metricsCacheLastUpdateTime = time.Time{}
	metricsCacheLock.Unlock()

	var buf1 bytes.Buffer
	WritePrometheusMetrics(&buf1)

	// Wait long enough for the 1-second cache TTL to expire.
	time.Sleep(1100 * time.Millisecond)

	var buf2 bytes.Buffer
	WritePrometheusMetrics(&buf2)

	// The two snapshots should now differ because uptime has advanced.
	if bytes.Equal(buf1.Bytes(), buf2.Bytes()) {
		t.Error("expected refreshed (different) output after cache expiry, but got identical output")
	}
}

// TestWritePrometheusMetrics_MultipleCallsNoPanic ensures that multiple
// concurrent-safe sequential calls do not panic.
func TestWritePrometheusMetrics_MultipleCallsNoPanic(t *testing.T) {
	for i := 0; i < 5; i++ {
		var buf bytes.Buffer
		WritePrometheusMetrics(&buf)
	}
}

// TestWriteOSMetrics_DoesNotPanic simply exercises writeOSMetrics to make
// sure it does not panic on the current platform.
func TestWriteOSMetrics_DoesNotPanic(t *testing.T) {
	var buf bytes.Buffer
	writeOSMetrics(&buf)
	// On Linux the output must contain vm_os_info with os="linux"
	output := buf.String()
	if len(output) > 0 && !strings.Contains(output, "vm_os_info{") {
		t.Errorf("unexpected content in writeOSMetrics output: %q", output)
	}
}

// TestStartTimeIsReasonable checks that the package-level startTime was set to
// a time in the past (and not the zero value).
func TestStartTimeIsReasonable(t *testing.T) {
	if startTime.IsZero() {
		t.Fatal("startTime must not be the zero value")
	}
	if !startTime.Before(time.Now()) {
		t.Errorf("startTime %v should be before now", startTime)
	}
}

package timeserieslimits

import (
	"strings"
	"testing"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/prompb"
)

// makeLabels is a helper that converts a name→value map into []prompb.Label.
func makeLabels(pairs ...string) []prompb.Label {
	if len(pairs)%2 != 0 {
		panic("makeLabels: odd number of arguments")
	}
	labels := make([]prompb.Label, 0, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		labels = append(labels, prompb.Label{Name: pairs[i], Value: pairs[i+1]})
	}
	return labels
}

// resetLimits restores package-level vars to a neutral state after each test.
func resetLimits() {
	maxLabelsPerTimeseries = 0
	maxLabelNameLen = 0
	maxLabelValueLen = 0
	enabled = false
}

// TestEnabledUsesOrNotAnd verifies that Enabled() returns true when ANY single
// limit is set, not only when ALL limits are set.
//
// Before commit dbed0de65 (lib/timeserieslimits: follow-up for 564e6ea02) the
// condition was:
//
//	enabled = maxLabelsPerTimeseries > 0 && maxLabelNameLen > 0 && maxLabelValueLen > 0
//
// Using && instead of || meant that setting only one limit left enabled=false,
// so IsExceeding() was never called and series were never dropped.
// The fix changed the condition to ||.
func TestEnabledUsesOrNotAnd(t *testing.T) {
	defer resetLimits()

	// Only maxLabelsPerTimeseries set — must be enabled.
	Init(5, 0, 0)
	if !Enabled() {
		t.Fatal("Enabled() must return true when only maxLabelsPerTimeseries is set " +
			"(regression of commit dbed0de65)")
	}
	resetLimits()

	// Only maxLabelNameLen set — must be enabled.
	Init(0, 64, 0)
	if !Enabled() {
		t.Fatal("Enabled() must return true when only maxLabelNameLen is set " +
			"(regression of commit dbed0de65)")
	}
	resetLimits()

	// Only maxLabelValueLen set — must be enabled.
	Init(0, 0, 256)
	if !Enabled() {
		t.Fatal("Enabled() must return true when only maxLabelValueLen is set " +
			"(regression of commit dbed0de65)")
	}
	resetLimits()

	// All limits zero — must be disabled.
	Init(0, 0, 0)
	if Enabled() {
		t.Fatal("Enabled() must return false when all limits are zero")
	}
}

// TestIsExceedingTooManyLabels verifies that IsExceeding returns true when the
// number of labels exceeds maxLabelsPerTimeseries.
//
// Added in commit 564e6ea02 (app/{vminsert,vmagent}: drop time series on
// exceeding labels limits, issues #6928 and #7661).
func TestIsExceedingTooManyLabels(t *testing.T) {
	defer resetLimits()
	Init(3, 0, 0)

	// Exactly at the limit — must not be rejected.
	at := makeLabels("__name__", "m", "a", "1", "b", "2")
	if IsExceeding(at) {
		t.Fatalf("IsExceeding must return false for %d labels with limit %d", len(at), maxLabelsPerTimeseries)
	}

	// One over the limit — must be rejected.
	over := makeLabels("__name__", "m", "a", "1", "b", "2", "c", "3")
	if !IsExceeding(over) {
		t.Fatalf("IsExceeding must return true for %d labels with limit %d", len(over), maxLabelsPerTimeseries)
	}
}

// TestIsExceedingTooLongLabelName verifies that IsExceeding returns true when
// any label name exceeds maxLabelNameLen.
func TestIsExceedingTooLongLabelName(t *testing.T) {
	defer resetLimits()
	Init(0, 10, 0)

	// Label name exactly at limit — allowed.
	ok := makeLabels(strings.Repeat("a", 10), "v")
	if IsExceeding(ok) {
		t.Fatal("IsExceeding must return false for label name at the limit")
	}

	// Label name one character over the limit — rejected.
	bad := makeLabels(strings.Repeat("a", 11), "v")
	if !IsExceeding(bad) {
		t.Fatal("IsExceeding must return true for label name exceeding maxLabelNameLen")
	}
}

// TestIsExceedingTooLongLabelValue verifies that IsExceeding returns true when
// any label value exceeds maxLabelValueLen.
func TestIsExceedingTooLongLabelValue(t *testing.T) {
	defer resetLimits()
	Init(0, 0, 10)

	// Label value exactly at limit — allowed.
	ok := makeLabels("k", strings.Repeat("v", 10))
	if IsExceeding(ok) {
		t.Fatal("IsExceeding must return false for label value at the limit")
	}

	// Label value one character over the limit — rejected.
	bad := makeLabels("k", strings.Repeat("v", 11))
	if !IsExceeding(bad) {
		t.Fatal("IsExceeding must return true for label value exceeding maxLabelValueLen")
	}
}

// TestIsExceedingDisabledWhenLimitsAreZero verifies that when all limits are
// zero IsExceeding always returns false, regardless of the input.
func TestIsExceedingDisabledWhenLimitsAreZero(t *testing.T) {
	defer resetLimits()
	Init(0, 0, 0)

	// Huge number of labels, long name, long value — all should pass.
	labels := make([]prompb.Label, 0, 100)
	for i := range 100 {
		labels = append(labels, prompb.Label{
			Name:  strings.Repeat("n", 1000),
			Value: strings.Repeat("v", 10000),
		})
		_ = i
	}
	if IsExceeding(labels) {
		t.Fatal("IsExceeding must return false when all limits are 0 (limits disabled)")
	}
}

// TestIsExceedingOnlyChecksEnabledLimits verifies that a zero limit for one
// dimension does not cause rejection on that dimension while another limit is
// active.
func TestIsExceedingOnlyChecksEnabledLimits(t *testing.T) {
	defer resetLimits()
	// maxLabelNameLen=0 means "no limit on name length"; maxLabelValueLen=5.
	Init(0, 0, 5)

	// Long name, short value — must pass (name limit is disabled).
	labels := makeLabels(strings.Repeat("n", 1000), "ok")
	if IsExceeding(labels) {
		t.Fatal("IsExceeding must not reject on label name when maxLabelNameLen=0")
	}

	// Long name, long value — must be rejected (value limit is active).
	labels2 := makeLabels(strings.Repeat("n", 1000), strings.Repeat("v", 6))
	if !IsExceeding(labels2) {
		t.Fatal("IsExceeding must reject when label value exceeds maxLabelValueLen")
	}
}

// TestIsExceedingEmptyLabels verifies that an empty label slice is never
// rejected regardless of the limit configuration.
func TestIsExceedingEmptyLabels(t *testing.T) {
	defer resetLimits()
	Init(1, 1, 1)

	if IsExceeding([]prompb.Label{}) {
		t.Fatal("IsExceeding must return false for an empty label slice")
	}
}

package common

import (
	"testing"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/prompb"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/timeserieslimits"
)

// TestAddLabelSkipsEmptyValue verifies that AddLabel silently drops labels
// whose value is empty, as documented in issue #600.
//
// The metric name (__name__) uses Name=="" with a non-empty Value and must
// NOT be dropped.
func TestAddLabelSkipsEmptyValue(t *testing.T) {
	var ctx InsertCtx
	ctx.Reset(0)

	// Empty value — must be skipped.
	ctx.AddLabel("job", "")
	if len(ctx.Labels) != 0 {
		t.Fatalf("expected 0 labels, got %d", len(ctx.Labels))
	}

	// Non-empty value — must be kept.
	ctx.AddLabel("job", "test")
	if len(ctx.Labels) != 1 {
		t.Fatalf("expected 1 label, got %d", len(ctx.Labels))
	}
	if ctx.Labels[0].Name != "job" || ctx.Labels[0].Value != "test" {
		t.Fatalf("unexpected label: %+v", ctx.Labels[0])
	}

	// __name__ uses Name=="" — empty Name with non-empty Value must be kept.
	ctx.AddLabel("", "my_metric")
	if len(ctx.Labels) != 2 {
		t.Fatalf("expected 2 labels, got %d", len(ctx.Labels))
	}
}

// TestAddLabelBytesSkipsEmptyValue mirrors TestAddLabelSkipsEmptyValue for the
// byte-slice variant AddLabelBytes.
func TestAddLabelBytesSkipsEmptyValue(t *testing.T) {
	var ctx InsertCtx
	ctx.Reset(0)

	ctx.AddLabelBytes([]byte("instance"), []byte(""))
	if len(ctx.Labels) != 0 {
		t.Fatalf("expected 0 labels after empty-value add, got %d", len(ctx.Labels))
	}

	ctx.AddLabelBytes([]byte("instance"), []byte("host1"))
	if len(ctx.Labels) != 1 {
		t.Fatalf("expected 1 label, got %d", len(ctx.Labels))
	}
}

// TestInsertCtxResetClearsState verifies that Reset() zeroes Labels and
// pre-allocates the requested row capacity.
func TestInsertCtxResetClearsState(t *testing.T) {
	var ctx InsertCtx
	ctx.Reset(0)

	ctx.AddLabel("a", "1")
	ctx.AddLabel("b", "2")
	if len(ctx.Labels) != 2 {
		t.Fatalf("expected 2 labels before reset, got %d", len(ctx.Labels))
	}

	ctx.Reset(10)
	if len(ctx.Labels) != 0 {
		t.Fatalf("expected 0 labels after reset, got %d", len(ctx.Labels))
	}
	if cap(ctx.mrs) < 10 {
		t.Fatalf("expected mrs capacity >= 10 after Reset(10), got %d", cap(ctx.mrs))
	}
}

// TestTryPrepareLabelsReturnsFalseForEmpty verifies that TryPrepareLabels
// returns false when the label set is empty (e.g. all labels were stripped by
// relabeling or were never added).
func TestTryPrepareLabelsReturnsFalseForEmpty(t *testing.T) {
	var ctx InsertCtx
	ctx.Reset(0)

	// No labels — must return false.
	if ctx.TryPrepareLabels(false) {
		t.Fatal("TryPrepareLabels must return false for empty Labels")
	}
}

// TestTryPrepareLabelsReturnsTrueWithLabels verifies that TryPrepareLabels
// returns true when at least one label is present and limits are not exceeded.
func TestTryPrepareLabelsReturnsTrueWithLabels(t *testing.T) {
	// Ensure limits are disabled so they don't interfere.
	timeserieslimits.Init(0, 0, 0)

	var ctx InsertCtx
	ctx.Reset(0)
	ctx.AddLabel("", "my_metric")
	ctx.AddLabel("job", "test")

	if !ctx.TryPrepareLabels(false) {
		t.Fatal("TryPrepareLabels must return true when labels are present and limits disabled")
	}
}

// TestTryPrepareLabelsDropsWhenLimitExceeded verifies that TryPrepareLabels
// returns false when the timeseries label-count limit is exceeded.
func TestTryPrepareLabelsDropsWhenLimitExceeded(t *testing.T) {
	// Allow only 1 label; we will add 2.
	timeserieslimits.Init(1, 0, 0)
	defer timeserieslimits.Init(0, 0, 0) // reset after test

	var ctx InsertCtx
	ctx.Reset(0)
	ctx.AddLabel("", "my_metric")
	ctx.AddLabel("extra", "label")

	if ctx.TryPrepareLabels(false) {
		t.Fatal("TryPrepareLabels must return false when label-count limit is exceeded")
	}
}

// TestSortedLabelsSort verifies that sortedLabels.Less returns the correct
// ordering and that sort.Sort produces lexicographically sorted labels.
func TestSortedLabelsSort(t *testing.T) {
	sl := sortedLabels([]prompb.Label{
		{Name: "z", Value: "3"},
		{Name: "a", Value: "1"},
		{Name: "m", Value: "2"},
	})

	if !sl.Less(1, 0) { // "a" < "z"
		t.Error("Less(1,0) must be true: 'a' < 'z'")
	}
	if sl.Less(0, 1) { // "z" is NOT < "a"
		t.Error("Less(0,1) must be false: 'z' is not < 'a'")
	}

	// Verify Swap.
	sl.Swap(0, 2)
	if sl[0].Name != "m" || sl[2].Name != "z" {
		t.Errorf("Swap(0,2) failed: got [0]=%q [2]=%q", sl[0].Name, sl[2].Name)
	}
}

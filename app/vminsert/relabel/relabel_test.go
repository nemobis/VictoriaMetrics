package relabel

import (
	"testing"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/prompb"
)

// makeLabels is a helper that converts alternating name/value pairs to []prompb.Label.
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

// TestApplyRelabelingNoOpWhenDisabled verifies that ApplyRelabeling returns the
// input labels unchanged when neither usePromCompatibleNaming nor any relabeling
// rules are configured.
func TestApplyRelabelingNoOpWhenDisabled(t *testing.T) {
	// Ensure clean state: no relabeling rules, naming compat off.
	pcsGlobal.Store(nil)
	*usePromCompatibleNaming = false

	var ctx Ctx
	input := makeLabels("", "my_metric", "job", "test", "instance", "host1")
	got := ctx.ApplyRelabeling(input)

	if len(got) != len(input) {
		t.Fatalf("expected %d labels, got %d", len(input), len(got))
	}
	for i, l := range got {
		if l.Name != input[i].Name || l.Value != input[i].Value {
			t.Errorf("label[%d] mismatch: got {%q,%q}, want {%q,%q}",
				i, l.Name, l.Value, input[i].Name, input[i].Value)
		}
	}
}

// TestApplyRelabelingPromCompatibleNamingMetricName verifies that dots in the
// metric name (stored as the label with Name=="") are replaced with underscores
// when -usePromCompatibleNaming is set.
func TestApplyRelabelingPromCompatibleNamingMetricName(t *testing.T) {
	pcsGlobal.Store(nil)
	*usePromCompatibleNaming = true
	defer func() { *usePromCompatibleNaming = false }()

	var ctx Ctx
	// Name=="" means __name__; dots should become underscores.
	labels := makeLabels("", "foo.bar.baz")
	got := ctx.ApplyRelabeling(labels)

	if len(got) != 1 {
		t.Fatalf("unexpected label count: %d", len(got))
	}
	if got[0].Value != "foo_bar_baz" {
		t.Errorf("metric name sanitization: got %q, want %q", got[0].Value, "foo_bar_baz")
	}
}

// TestApplyRelabelingPromCompatibleNamingLabelName verifies that dots in label
// names are replaced with underscores when -usePromCompatibleNaming is set.
func TestApplyRelabelingPromCompatibleNamingLabelName(t *testing.T) {
	pcsGlobal.Store(nil)
	*usePromCompatibleNaming = true
	defer func() { *usePromCompatibleNaming = false }()

	var ctx Ctx
	labels := makeLabels("", "m", "a.b", "v1", "x.y.z", "v2")
	got := ctx.ApplyRelabeling(labels)

	if len(got) != 3 {
		t.Fatalf("unexpected label count: %d", len(got))
	}
	want := []prompb.Label{
		{Name: "", Value: "m"},
		{Name: "a_b", Value: "v1"},
		{Name: "x_y_z", Value: "v2"},
	}
	for i, w := range want {
		if got[i].Name != w.Name || got[i].Value != w.Value {
			t.Errorf("label[%d]: got {%q,%q}, want {%q,%q}",
				i, got[i].Name, got[i].Value, w.Name, w.Value)
		}
	}
}

// TestApplyRelabelingPromCompatibleNamingCombined verifies a full example where
// both the metric name and a label name contain dots.  This mirrors the
// docstring example: foo.bar{a.b='c'} → foo_bar{a_b='c'}.
func TestApplyRelabelingPromCompatibleNamingCombined(t *testing.T) {
	pcsGlobal.Store(nil)
	*usePromCompatibleNaming = true
	defer func() { *usePromCompatibleNaming = false }()

	var ctx Ctx
	labels := makeLabels("", "foo.bar", "a.b", "c")
	got := ctx.ApplyRelabeling(labels)

	if len(got) != 2 {
		t.Fatalf("unexpected label count: %d", len(got))
	}
	if got[0].Value != "foo_bar" {
		t.Errorf("metric name: got %q, want %q", got[0].Value, "foo_bar")
	}
	if got[1].Name != "a_b" {
		t.Errorf("label name: got %q, want %q", got[1].Name, "a_b")
	}
	if got[1].Value != "c" {
		t.Errorf("label value: got %q, want %q", got[1].Value, "c")
	}
}

// TestApplyRelabelingEmptyLabels verifies that an empty label slice is handled
// without panicking.
func TestApplyRelabelingEmptyLabels(t *testing.T) {
	pcsGlobal.Store(nil)
	*usePromCompatibleNaming = false

	var ctx Ctx
	got := ctx.ApplyRelabeling([]prompb.Label{})
	if len(got) != 0 {
		t.Fatalf("expected empty result, got %d labels", len(got))
	}
}

// TestCtxResetClearsTmpLabels verifies that Reset() empties tmpLabels so
// subsequent ApplyRelabeling calls do not see stale labels from previous calls.
func TestCtxResetClearsTmpLabels(t *testing.T) {
	pcsGlobal.Store(nil)
	*usePromCompatibleNaming = false

	var ctx Ctx

	// First call populates tmpLabels.
	ctx.ApplyRelabeling(makeLabels("", "m1", "a", "1"))

	// Reset clears tmpLabels.
	ctx.Reset()
	if len(ctx.tmpLabels) != 0 {
		t.Fatalf("after Reset: tmpLabels not empty (%d elements)", len(ctx.tmpLabels))
	}
}

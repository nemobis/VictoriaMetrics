package formatutil

import (
	"testing"
)

func TestHumanizeBytes(t *testing.T) {
	tests := []struct {
		input float64
		want  string
	}{
		{0, "0"},
		{1, "1"},
		{1023, "1023"},
		{1024, "1ki"},
		{1024 * 1024, "1Mi"},
		{1024 * 1024 * 1024, "1Gi"},
		{1024 * 1024 * 1024 * 1024, "1Ti"},
		{1.5 * 1024, "1.5ki"},
		{-1024, "-1ki"},
		{512, "512"},
		{1024 * 1024 * 512, "512Mi"},
	}
	for _, tc := range tests {
		got := HumanizeBytes(tc.input)
		if got != tc.want {
			t.Errorf("HumanizeBytes(%v) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestHumanizeBytesLargeValues(t *testing.T) {
	// Values at each threshold boundary
	result := HumanizeBytes(float64(1024))
	if result == "" {
		t.Fatal("expected non-empty result for 1024 bytes")
	}

	// Ensure negative values also work
	neg := HumanizeBytes(-1024 * 1024)
	if neg != "-1Mi" {
		t.Errorf("HumanizeBytes(-1Mi) = %q, want %q", neg, "-1Mi")
	}
}

package buildinfo

import (
	"testing"
)

func TestShortVersionEmpty(t *testing.T) {
	// When Version is empty, ShortVersion should return empty string.
	orig := Version
	defer func() { Version = orig }()

	Version = ""
	got := ShortVersion()
	if got != "" {
		t.Fatalf("expected empty ShortVersion for empty Version, got %q", got)
	}
}

func TestShortVersionBasic(t *testing.T) {
	orig := Version
	defer func() { Version = orig }()

	cases := []struct {
		version string
		want    string
	}{
		{"v1.93.0", "v1.93.0"},
		{"v1.93.0-enterprise", "v1.93.0-enterprise"},
		{"v1.93.0-cluster", "v1.93.0-cluster"},
		{"v1.93.0-enterprise-cluster", "v1.93.0-enterprise-cluster"},
		// Version strings with extra build info prefix/suffix
		{"VictoriaMetrics/v1.2.3 (go1.21)", "v1.2.3"},
		// No valid version present
		{"unknown", ""},
		{"", ""},
	}

	for _, tc := range cases {
		Version = tc.version
		got := ShortVersion()
		if got != tc.want {
			t.Errorf("ShortVersion(%q) = %q, want %q", tc.version, got, tc.want)
		}
	}
}

func TestShortVersionRegexEdgeCases(t *testing.T) {
	orig := Version
	defer func() { Version = orig }()

	// Must start with 'v' and have three numeric segments
	Version = "1.2.3" // no leading 'v'
	got := ShortVersion()
	if got != "" {
		t.Errorf("expected empty for version without 'v' prefix, got %q", got)
	}

	// enterprise flag
	Version = "v10.200.300-enterprise"
	got = ShortVersion()
	if got != "v10.200.300-enterprise" {
		t.Errorf("unexpected ShortVersion for enterprise: %q", got)
	}

	// cluster flag
	Version = "v10.200.300-cluster"
	got = ShortVersion()
	if got != "v10.200.300-cluster" {
		t.Errorf("unexpected ShortVersion for cluster: %q", got)
	}

	// enterprise-cluster combined
	Version = "v10.200.300-enterprise-cluster"
	got = ShortVersion()
	if got != "v10.200.300-enterprise-cluster" {
		t.Errorf("unexpected ShortVersion for enterprise-cluster: %q", got)
	}
}
